package service

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/ent/shoporderaction"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

const (
	shopDaypassCheckoutKey = "daypass_checkout"
	shopLegacyCardBatch    = 128
)

type ShopDaypassCheckout struct {
	shopaction.DaypassCardSelection
	KeySuffix string `json:"key_suffix,omitempty"`
}

type ShopDaypassCards struct {
	ServerTime int64             `json:"server_time"`
	Cards      []ShopDaypassCard `json:"cards"`
}

func snapshotDaypassCheckout(f *dbent.ShopFulfillment) (*ShopDaypassCheckout, error) {
	raw, ok := f.ProductSnapshot[shopDaypassCheckoutKey]
	if !ok {
		return nil, nil
	}
	var checkout ShopDaypassCheckout
	encoded, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(encoded, &checkout) != nil || !checkout.Valid() ||
		(checkout.Action == shopaction.DaypassNewCard && checkout.KeySuffix != "") ||
		(checkout.Action == shopaction.DaypassRenewCard && (!shopKeySuffix.MatchString(checkout.KeySuffix) || checkout.CardID >= f.OrderID)) {
		return nil, infraerrors.Conflict("SHOP_SNAPSHOT_INVALID", "订单续费快照异常，请联系客服")
	}
	return &checkout, nil
}

func (s *ShopService) daypassCardsAdapter(p ShopProduct) (shopDaypassGateway, error) {
	if !nativeShopProduct(p) || p.ID != s.daypassCfg.ProductID {
		return nil, infraerrors.BadRequest("SHOP_DAYPASS_PRODUCT_REQUIRED", "该商品不支持选卡")
	}
	a, err := s.actionAdapter(p.PaymentActions[0])
	adapter, ok := a.(*shopDaypassAdapter)
	if err != nil || !ok {
		return nil, shopDaypassCardsUnavailable()
	}
	return adapter.gateway, nil
}

func shopDaypassCardsUnavailable() error {
	return infraerrors.ServiceUnavailable("SHOP_DAYPASS_CARDS_UNAVAILABLE", "暂时无法读取 DeepSeek 卡片，请稍后重试")
}

func shopDaypassCardUnavailable() error {
	return infraerrors.Conflict("SHOP_DAYPASS_CARD_UNAVAILABLE", "这张卡已无法续费，请重新选卡")
}

func (s *ShopService) BuyerDaypassCards(ctx context.Context, userID, productID int64) (*ShopDaypassCards, error) {
	p, err := s.checkoutProduct(ctx, productID)
	if err != nil {
		return nil, err
	}
	gateway, err := s.daypassCardsAdapter(*p)
	if err != nil {
		return nil, err
	}
	result := &ShopDaypassCards{Cards: make([]ShopDaypassCard, 0)}
	cardIndexes := make(map[int64]int)
	legacyTerms := make(map[int64]int)
	lastOrders := make(map[int64]int64)
	var before int64
	for {
		rows, err := matchingDaypassActions(s.client, shopCardSource{userID, productID}, 0, before).
			Order(dbent.Desc(shoporderaction.FieldOrderID)).Limit(shopLegacyCardBatch).All(ctx)
		if err != nil {
			return nil, fmt.Errorf("load buyer card proofs: %w", err)
		}
		query := shopDaypassCardQuery{UserID: userID, IncludeBound: before == 0}
		for _, row := range rows {
			if days, verified := shopLegacyCardProof(row, productID); verified {
				query.LegacyOrders = append(query.LegacyOrders, row.OrderID)
				legacyTerms[row.OrderID] = days
			}
		}
		if query.IncludeBound || len(query.LegacyOrders) != 0 {
			list, err := gateway.BuyerCards(ctx, query)
			if err != nil {
				return nil, shopDaypassCardsUnavailable()
			}
			result.ServerTime = list.ServerTime
			for _, card := range list.Cards {
				if i, exists := cardIndexes[card.CardID]; exists {
					result.Cards[i] = card.ShopDaypassCard
				} else {
					cardIndexes[card.CardID] = len(result.Cards)
					result.Cards = append(result.Cards, card.ShopDaypassCard)
				}
				lastOrders[card.CardID] = card.LastOrderID
			}
		}
		if len(rows) < shopLegacyCardBatch {
			break
		}
		before = rows[len(rows)-1].OrderID
	}
	for i := range result.Cards {
		if days := legacyTerms[lastOrders[result.Cards[i].CardID]]; days != 0 {
			result.Cards[i].LastDays = days
		}
	}
	slices.SortFunc(result.Cards, compareShopDaypassCards)
	return result, nil
}

func compareShopDaypassCards(a, b ShopDaypassCard) int {
	// Alphabetically active, expired, scheduled is not presentation order.
	statusOrder := map[string]int{"active": 0, "scheduled": 1, "expired": 2}
	if order := cmp.Compare(statusOrder[a.Status], statusOrder[b.Status]); order != 0 {
		return order
	}
	switch a.Status {
	case "active":
		if order := cmp.Compare(a.ExpiresAt, b.ExpiresAt); order != 0 {
			return order
		}
	case "scheduled":
		if order := cmp.Compare(a.NextStartsAt, b.NextStartsAt); order != 0 {
			return order
		}
	default:
		if order := cmp.Compare(b.ExpiresAt, a.ExpiresAt); order != 0 {
			return order
		}
	}
	return cmp.Compare(a.CardID, b.CardID)
}

func (s *ShopService) resolveDaypassCheckout(ctx context.Context, p ShopProduct, req CreateOrderRequest) (*ShopDaypassCheckout, error) {
	if !nativeShopProduct(p) {
		if req.ShopDaypass != (shopaction.DaypassCardSelection{}) {
			return nil, infraerrors.BadRequest("SHOP_DAYPASS_PRODUCT_REQUIRED", "该商品不支持选卡")
		}
		return nil, nil
	}
	if !req.ShopDaypass.Valid() {
		return nil, infraerrors.BadRequest("SHOP_DAYPASS_SELECTION_REQUIRED", "请选择续费或另购；续费时需选一张卡")
	}
	gateway, err := s.daypassCardsAdapter(p)
	if err != nil {
		return nil, err
	}
	if err := gateway.ReadyCards(ctx, p.DurationValue); err != nil {
		return nil, shopDaypassCardsUnavailable()
	}
	checkout := &ShopDaypassCheckout{DaypassCardSelection: req.ShopDaypass}
	query := shopDaypassCardQuery{UserID: req.UserID, CardID: checkout.CardID}
	if checkout.CardID != 0 {
		proof, err := lookupShopLegacyCardProof(ctx, s.client, shopCardSource{req.UserID, p.ID}, checkout.CardID)
		if err != nil {
			return nil, err
		}
		if proof {
			query.LegacyOrders = []int64{checkout.CardID}
		}
	}
	list, err := gateway.BuyerCards(ctx, query)
	if checkout.CardID != 0 && errors.Is(err, errShopDaypassCard) {
		return nil, shopDaypassCardUnavailable()
	}
	if err != nil {
		return nil, shopDaypassCardsUnavailable()
	}
	if checkout.CardID != 0 {
		if len(list.Cards) != 1 || list.Cards[0].CardID != checkout.CardID {
			return nil, shopDaypassCardUnavailable()
		}
		checkout.KeySuffix = list.Cards[0].KeySuffix
	}
	return checkout, nil
}

type shopCardSource struct{ userID, productID int64 }

func matchingDaypassActions(client *dbent.Client, scope shopCardSource, exact, before int64) *dbent.ShopOrderActionQuery {
	q := client.ShopOrderAction.Query().Where(shoporderaction.StatusEQ(shopaction.Succeeded), func(s *sql.Selector) {
		o, f := sql.Table(paymentorder.Table), sql.Table(shopfulfillment.Table)
		source := sql.Select(o.C(paymentorder.FieldID)).From(o).Join(f).
			On(o.C(paymentorder.FieldID), f.C(shopfulfillment.FieldOrderID)).
			Where(sql.And(sql.EQ(o.C(paymentorder.FieldUserID), scope.userID),
				sql.EQ(o.C(paymentorder.FieldOrderType), payment.OrderTypeManualShop), sql.EQ(o.C(paymentorder.FieldStatus), OrderStatusCompleted),
				sql.EQ(f.C(shopfulfillment.FieldProductID), scope.productID), sql.EQ(f.C(shopfulfillment.FieldUserID), scope.userID)))
		s.Where(sql.In(s.C(shoporderaction.FieldOrderID), source))
	})
	if exact > 0 {
		q.Where(shoporderaction.OrderIDEQ(exact))
	}
	if before > 0 {
		q.Where(shoporderaction.OrderIDLT(before))
	}
	return q
}

func shopLegacyCardProof(row *dbent.ShopOrderAction, productID int64) (int, bool) {
	snapshot, err := decodeShopAction(row)
	if err != nil || snapshot.Binding.Adapter != shopaction.Native ||
		snapshot.Request.Order.ProductID != productID || snapshot.DaypassBuyer != nil {
		return 0, false
	}
	return shopNativeSKU(snapshot.Request.Params).selection().DurationDays, true
}

func lookupShopLegacyCardProof(ctx context.Context, client *dbent.Client, scope shopCardSource, cardID int64) (bool, error) {
	row, err := matchingDaypassActions(client, scope, cardID, 0).Only(ctx)
	if dbent.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read legacy card proof: %w", err)
	}
	_, verified := shopLegacyCardProof(row, scope.productID)
	return verified, nil
}

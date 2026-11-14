package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/ent/shoporderaction"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

// Called only with the payment and fulfillment rows locked, in their transaction.
// Business-invalid snapshots go to review; only database failures abort payment.
func enqueueShopOrderActionTx(ctx context.Context, client *dbent.Client, o *dbent.PaymentOrder, f *dbent.ShopFulfillment) error {
	p, snapshotErr := shopSnapshot(f)
	if snapshotErr == nil && !shopAutomaticFulfillment(p) {
		return nil
	}
	exists, err := client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).Exist(ctx)
	if err != nil || exists {
		return err
	}
	identity := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d\nprimary-delivery", shopDaypassInstance, shopaction.Paid, o.ID)))
	key := "act_" + hex.EncodeToString(identity[:20])
	paid := time.Now().UTC()
	state, kind := shopaction.Pending, ""
	if o.PaidAt != nil {
		paid = o.PaidAt.UTC()
	} else {
		state, kind = shopaction.Review, "PAID_TIME_INVALID"
	}
	snapshot := shopaction.Snapshot{Request: shopaction.Request{Version: shopaction.PaidRequestVersion, Event: shopaction.Paid, ActionID: key, Source: shopDaypassInstance,
		Order: shopaction.Order{ID: o.ID, ProductID: f.ProductID, AmountFen: int64(math.Round(o.PayAmount * 100)), Currency: "CNY", PaidAt: paid}}}
	if p.SKUID != "" && len(p.SKUs) == 1 && p.SKUs[0].ID == p.SKUID {
		sku := p.SKUs[0]
		snapshot.Request.Order.SKU = &shopaction.SKU{ID: sku.ID, Attributes: sku.Attributes, DurationValue: sku.DurationValue, DurationUnit: sku.DurationUnit, Concurrency: sku.Concurrency}
	}
	if snapshotErr != nil || len(p.PaymentActions) != 1 || p.FulfillmentMode != ShopAccountDelivery {
		state, kind = shopaction.Review, "SNAPSHOT_INVALID"
	} else {
		snapshot.Binding = p.PaymentActions[0]
		if !validateActionSnapshot(snapshot.Binding) || snapshot.Binding.Adapter == shopaction.Native && (!validShopDaypassProduct(p) || snapshot.Request.Order.AmountFen != shopProductSKU(p).priceFen()) {
			state, kind = shopaction.Review, "ACTION_INVALID"
			snapshot.Binding.Params = json.RawMessage(`{}`)
		}
		snapshot.Request.Params = snapshot.Binding.Params
	}
	if state == shopaction.Pending && snapshot.Binding.Adapter == shopaction.Native {
		if o.UserID <= 0 || o.UserID > math.MaxInt32 || f.UserID != o.UserID {
			state, kind = shopaction.Review, "ACTION_INVALID"
		} else {
			card, err := snapshotDaypassCheckout(f)
			if err != nil {
				state, kind = shopaction.Review, "DAYPASS_SELECTION_INVALID"
			} else {
				buyer, err := snapshotDaypassBuyer(ctx, client, o, f, card)
				if err != nil {
					return err
				}
				snapshot.Request.Version, snapshot.DaypassBuyer = shopaction.DaypassPaidRequestVersion, buyer
				if card != nil {
					snapshot.Request.Version = shopaction.DaypassCardRequestVersion
				}
			}
		}
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		state, kind = shopaction.Review, "ACTION_INVALID"
		snapshot.Binding = shopaction.Binding{}
		snapshot.Request.Params = nil
		raw, _ = json.Marshal(snapshot)
	}
	digest := sha256.Sum256(raw)
	if err = client.ShopOrderAction.Create().SetOrderID(o.ID).SetActionKey(key).SetSnapshot(string(raw)).SetSnapshotHash(hex.EncodeToString(digest[:])).
		SetDeadlineAt(paid.Add(24 * time.Hour)).SetStatus(state).SetErrorKind(kind).Exec(ctx); err != nil {
		return err
	}
	if state == shopaction.Review {
		return queueShopNotification(ctx, client, f, "action_review", "feishu")
	}
	return nil
}

func snapshotDaypassBuyer(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, fulfillment *dbent.ShopFulfillment, card *ShopDaypassCheckout) (*shopaction.DaypassBuyer, error) {
	buyer := &shopaction.DaypassBuyer{UserID: order.UserID}
	// The numeric ID is authoritative. The optional display name never
	// participates in the lookup and must fit the receiver's safe text format.
	buyer.Username = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, order.UserName))
	runes := []rune(buyer.Username)
	if len(runes) > 64 {
		buyer.Username = string(runes[:64])
	}
	if card != nil {
		buyer.Card = &card.DaypassCardSelection
		if card.CardID != 0 {
			proof, err := lookupShopLegacyCardProof(ctx, client, shopCardSource{order.UserID, fulfillment.ProductID}, card.CardID)
			if err != nil {
				return nil, err
			}
			if proof {
				buyer.PriorOrderID = card.CardID
			}
		}
		return buyer, nil
	}
	// A completed native action is evidence of issuance, including for orders
	// from before the receiver knew a Sub2 user ID.
	prior, err := client.ShopOrderAction.Query().Where(shoporderaction.StatusEQ(shopaction.Succeeded), func(s *sql.Selector) {
		o, f := sql.Table(paymentorder.Table), sql.Table(shopfulfillment.Table)
		matching := sql.Select(o.C(paymentorder.FieldID)).From(o).Join(f).
			On(o.C(paymentorder.FieldID), f.C(shopfulfillment.FieldOrderID)).
			Where(sql.And(sql.EQ(o.C(paymentorder.FieldUserID), order.UserID), sql.LT(o.C(paymentorder.FieldID), order.ID),
				sql.EQ(o.C(paymentorder.FieldOrderType), order.OrderType), sql.EQ(o.C(paymentorder.FieldStatus), OrderStatusCompleted),
				sql.EQ(f.C(shopfulfillment.FieldProductID), fulfillment.ProductID), sql.EQ(f.C(shopfulfillment.FieldUserID), order.UserID)))
		s.Where(sql.In(s.C(shoporderaction.FieldOrderID), matching))
	}).Order(dbent.Desc(shoporderaction.FieldOrderID)).First(ctx)
	switch {
	case dbent.IsNotFound(err):
		return buyer, nil
	case err != nil:
		return nil, fmt.Errorf("look up prior DeepSeek receipt: %w", err)
	}
	if snapshot, err := decodeShopAction(prior); err == nil && snapshot.Binding.Adapter == shopaction.Native &&
		snapshot.Request.Order.ProductID == fulfillment.ProductID && snapshot.DaypassBuyer == nil {
		buyer.PriorOrderID = prior.OrderID
	}
	return buyer, nil
}

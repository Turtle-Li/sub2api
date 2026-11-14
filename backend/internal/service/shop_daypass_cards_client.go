package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"

	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

type shopDaypassCardQuery struct {
	UserID       int64   `json:"sub2_user_id"`
	CardID       int64   `json:"card_id,omitempty"`
	LegacyOrders []int64 `json:"legacy_order_ids,omitempty"`
	IncludeBound bool    `json:"include_bound,omitempty"`
}

type ShopDaypassCard struct {
	CardID       int64  `json:"card_id"`
	KeySuffix    string `json:"key_suffix"`
	LastDays     int    `json:"last_duration_days"`
	ExpiresAt    int64  `json:"expires_at"`
	NextStartsAt int64  `json:"next_starts_at,omitempty"`
	Status       string `json:"status"`
}

type shopDaypassCardList struct {
	Instance   string                 `json:"instance"`
	ProductID  int64                  `json:"product_id"`
	UserID     int64                  `json:"sub2_user_id"`
	ServerTime int64                  `json:"server_time"`
	Cards      []shopDaypassCardEntry `json:"cards"`
}

type shopDaypassCardEntry struct {
	ShopDaypassCard
	LastOrderID int64 `json:"last_order_id"`
}

var shopKeySuffix = regexp.MustCompile(`^[A-Za-z0-9]{4}$`)

func (card ShopDaypassCard) valid(serverTime int64) bool {
	if card.CardID <= 0 || !shopKeySuffix.MatchString(card.KeySuffix) || card.ExpiresAt <= 0 ||
		(card.LastDays != 0 && card.LastDays != 1 && card.LastDays != 7) {
		return false
	}
	switch card.Status {
	case "active":
		return card.ExpiresAt > serverTime && card.NextStartsAt == 0
	case "expired":
		return card.NextStartsAt == 0
	case "scheduled":
		return card.NextStartsAt > serverTime && card.NextStartsAt < card.ExpiresAt
	default:
		return false
	}
}

func (b shopDaypassBinding) usesPaidWindows() bool {
	return b.SKUVersion == shopDaypassRenewalVersion || b.SKUVersion == shopDaypassCardsVersion
}

func (b shopDaypassBinding) validCard() bool {
	if b.SKUVersion != shopDaypassCardsVersion {
		return b.CardAction == "" && b.CardID == 0
	}
	card := shopaction.DaypassCardSelection{Action: b.CardAction, CardID: b.CardID}
	return card.Valid() && b.CardID < b.OrderID && (b.PriorOrderID == 0 || b.PriorOrderID == b.CardID)
}

func (c *shopDaypassClient) BuyerCards(ctx context.Context, query shopDaypassCardQuery) (*shopDaypassCardList, error) {
	bearer, err := c.loadBearer(ctx)
	if err != nil {
		return nil, errShopDaypassUnavailable
	}
	body, err := json.Marshal(query)
	if err != nil {
		return nil, errShopDaypassUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/api/daypass/automation/cards", bytes.NewReader(body))
	if err != nil {
		return nil, errShopDaypassUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return nil, errShopDaypassUnavailable
	}
	defer func() { _ = res.Body.Close() }()
	const limit = 256 * 1024
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	defer clear(raw)
	var result struct {
		Success bool                `json:"success"`
		Code    string              `json:"code"`
		Data    shopDaypassCardList `json:"data"`
	}
	if err != nil || len(raw) > limit || json.Unmarshal(raw, &result) != nil {
		return nil, errShopDaypassUnavailable
	}
	if res.StatusCode == http.StatusConflict && result.Code == "daypass_card_unavailable" {
		return nil, errShopDaypassCard
	}
	list := &result.Data
	if res.StatusCode != http.StatusOK || !result.Success || list.Instance != shopDaypassInstance || list.ProductID != c.productID ||
		list.UserID != query.UserID || list.ServerTime <= 0 || list.Cards == nil {
		return nil, errShopDaypassUnavailable
	}
	seen := make(map[int64]struct{}, len(list.Cards))
	for _, card := range list.Cards {
		if _, duplicate := seen[card.CardID]; duplicate || card.LastOrderID <= 0 || !card.valid(list.ServerTime) || query.CardID > 0 && card.CardID != query.CardID {
			return nil, errShopDaypassUnavailable
		}
		seen[card.CardID] = struct{}{}
	}
	return list, nil
}

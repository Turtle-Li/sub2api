package service

import (
	"context"
	"encoding/json"
	"fmt"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/shopproduct"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
	"math"
	"net/url"
	"strings"
	"unicode/utf8"
)

func shopProductDTO(p *dbent.ShopProduct) ShopProduct {
	var actions []shopaction.Binding
	if len(p.PaymentActions) > 0 && json.Unmarshal(p.PaymentActions, &actions) != nil {
		actions = []shopaction.Binding{{}}
	}
	return ShopProduct{ID: p.ID, Name: p.Name, Category: p.Category, Summary: p.Summary, Description: p.Description, Price: float64(p.PriceFen) / 100, Currency: "CNY", DurationValue: p.DurationValue, DurationUnit: p.DurationUnit, FulfillmentMode: p.FulfillmentMode, InputFormat: p.InputFormat, InputURL: p.InputURL, InputInstructions: p.InputInstructions, GuideKey: p.GuideKey, Enabled: p.Enabled, SortOrder: p.SortOrder, Revision: p.Revision, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, PaymentActions: actions, AutomaticFulfillment: len(actions) > 0}
}

func normalizeShopProduct(p *ShopProduct) (int64, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Category = strings.TrimSpace(p.Category)
	if p.DurationValue == 0 {
		p.DurationValue = 1
	}
	if p.DurationUnit == "" {
		p.DurationUnit = "month"
	}
	if p.InputFormat == "" {
		p.InputFormat = "session_json"
	}
	if p.InputURL == "" && p.InputFormat == "session_json" {
		p.InputURL = "https://chatgpt.com/api/auth/session"
	}
	p.Currency = "CNY"
	valid := utf8.ValidString(p.Name+p.Category+p.Summary+p.Description+p.InputInstructions) && !strings.ContainsRune(p.Name+p.Category+p.Summary+p.Description+p.InputInstructions+p.InputURL, 0)
	valid = valid && p.Name != "" && utf8.RuneCountInString(p.Name) <= 160 && utf8.RuneCountInString(p.Category) <= 80 && utf8.RuneCountInString(p.Summary) <= 600 && len(p.Description) <= 128*1024 && len(p.InputInstructions) <= 16*1024
	valid = valid && p.DurationValue > 0 && p.DurationValue <= 3650 && (p.DurationUnit == "month" || p.DurationUnit == "day" || p.DurationUnit == "year")
	valid = valid && (p.FulfillmentMode == ShopSessionTopup || p.FulfillmentMode == ShopAccountDelivery) && (p.InputFormat == "session_json" || p.InputFormat == "text") && (p.GuideKey == "" || p.GuideKey == "chatgpt-plus-account" || p.GuideKey == ShopDaypassGuide)
	valid = valid && !math.IsNaN(p.Price) && !math.IsInf(p.Price, 0) && p.Price >= 0 && p.Price <= 1000000 && math.Abs(p.Price*100-math.Round(p.Price*100)) < 0.000001 && (!p.Enabled || p.Price > 0)
	if p.InputURL != "" {
		u, err := url.Parse(p.InputURL)
		valid = valid && err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && len(p.InputURL) <= 2048
	}
	if !valid {
		return 0, infraerrors.BadRequest("SHOP_PRODUCT_INVALID", "请检查商品名称、价格、期限和交付配置")
	}
	return int64(math.Round(p.Price * 100)), nil
}

func (s *ShopService) Products(ctx context.Context, admin bool) ([]ShopProduct, error) {
	q := s.client.ShopProduct.Query()
	if !admin {
		q.Where(shopproduct.EnabledEQ(true))
	}
	rows, err := q.Order(dbent.Asc(shopproduct.FieldSortOrder), dbent.Asc(shopproduct.FieldID)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list shop products: %w", err)
	}
	out := make([]ShopProduct, 0, len(rows))
	for _, p := range rows {
		dto := shopProductDTO(p)
		if !admin {
			dto = s.publicCatalogProduct(dto)
		}
		out = append(out, dto)
	}
	return out, nil
}

func (s *ShopService) Product(ctx context.Context, id int64, admin bool) (*ShopProduct, error) {
	q := s.client.ShopProduct.Query().Where(shopproduct.IDEQ(id))
	if !admin {
		q.Where(shopproduct.EnabledEQ(true))
	}
	p, err := q.Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, infraerrors.NotFound("SHOP_PRODUCT_NOT_FOUND", "商品不存在或已下架")
	}
	if err != nil {
		return nil, fmt.Errorf("get shop product: %w", err)
	}
	dto := shopProductDTO(p)
	if !admin {
		dto = s.publicCatalogProduct(dto)
	}
	return &dto, nil
}

func (s *ShopService) SaveProduct(ctx context.Context, id int64, p ShopProduct) (*ShopProduct, error) {
	fen, err := normalizeShopProduct(&p)
	if err != nil {
		return nil, err
	}
	if err := s.normalizePaymentActions(&p); err != nil {
		return nil, err
	}
	actions, err := json.Marshal(p.PaymentActions)
	if err != nil {
		return nil, infraerrors.BadRequest("SHOP_ACTION_PARAMS_INVALID", "动作参数无效")
	}
	if id == 0 {
		row, err := s.client.ShopProduct.Create().SetName(p.Name).SetCategory(p.Category).SetSummary(p.Summary).SetDescription(p.Description).SetPriceFen(fen).SetDurationValue(p.DurationValue).SetDurationUnit(p.DurationUnit).SetFulfillmentMode(p.FulfillmentMode).SetInputFormat(p.InputFormat).SetInputURL(p.InputURL).SetInputInstructions(p.InputInstructions).SetGuideKey(p.GuideKey).SetPaymentActions(actions).SetEnabled(p.Enabled).SetSortOrder(p.SortOrder).Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("create shop product: %w", err)
		}
		dto := shopProductDTO(row)
		return &dto, nil
	}
	row, err := s.client.ShopProduct.UpdateOneID(id).Where(shopproduct.RevisionEQ(p.Revision)).SetName(p.Name).SetCategory(p.Category).SetSummary(p.Summary).SetDescription(p.Description).SetPriceFen(fen).SetDurationValue(p.DurationValue).SetDurationUnit(p.DurationUnit).SetFulfillmentMode(p.FulfillmentMode).SetInputFormat(p.InputFormat).SetInputURL(p.InputURL).SetInputInstructions(p.InputInstructions).SetGuideKey(p.GuideKey).SetPaymentActions(actions).SetEnabled(p.Enabled).SetSortOrder(p.SortOrder).AddRevision(1).Save(ctx)
	if dbent.IsNotFound(err) {
		return nil, shopConflict()
	}
	if err != nil {
		return nil, fmt.Errorf("update shop product: %w", err)
	}
	dto := shopProductDTO(row)
	return &dto, nil
}

func shopProductMap(p ShopProduct) (map[string]any, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = json.Unmarshal(b, &out)
	return out, err
}

func shopSnapshot(f *dbent.ShopFulfillment) (ShopProduct, error) {
	b, err := json.Marshal(f.ProductSnapshot)
	if err != nil {
		return ShopProduct{}, fmt.Errorf("encode shop snapshot: %w", err)
	}
	var p ShopProduct
	if err = json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("decode shop snapshot: %w", err)
	}
	if p.ID != f.ProductID || p.Currency != "CNY" || (p.FulfillmentMode != ShopSessionTopup && p.FulfillmentMode != ShopAccountDelivery) {
		return p, infraerrors.Conflict("SHOP_SNAPSHOT_INVALID", "订单商品快照异常，请联系客服")
	}
	p.Concurrency = shopProductConcurrency(p)
	return p, nil
}

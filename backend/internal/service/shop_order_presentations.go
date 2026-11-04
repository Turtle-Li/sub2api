package service

import (
	"context"
	"fmt"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
)

func (s *PaymentService) AdminOrderPresentations(ctx context.Context, orders []*dbent.PaymentOrder) (map[int64]PaymentOrderInvoicePresentation, error) {
	page, err := s.InvoiceOrderPresentations(ctx, orders)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, order := range orders {
		if order != nil && shopPaidActionAllowed(order) && !page[order.ID].NeedsManualReview && order.CompletedAt == nil {
			ids = append(ids, order.ID)
		}
	}
	if len(ids) == 0 {
		return page, nil
	}
	// Check for submitted input inside the database; neither ciphertext nor plaintext is returned.
	tasks, err := s.entClient.ShopFulfillment.Query().Where(
		shopfulfillment.OrderIDIn(ids...), shopfulfillment.StatusIn(ShopQueued, ShopProcessing),
		shopfulfillment.Or(shopfulfillment.InputCiphertextNEQ(""), func(sel *sql.Selector) {
			sel.Where(sqljson.ValueEQ(sel.C(shopfulfillment.FieldProductSnapshot), ShopAccountDelivery, sqljson.Path("fulfillment_mode")))
		}),
	).Select(shopfulfillment.FieldOrderID, shopfulfillment.FieldProductID, shopfulfillment.FieldProductSnapshot).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("read admin shop fulfillment availability: %w", err)
	}
	for _, task := range tasks {
		if _, err := shopSnapshot(task); err != nil {
			continue
		}
		item := page[task.OrderID]
		item.CanOpenShopFulfillment = true
		page[task.OrderID] = item
	}
	return page, nil
}

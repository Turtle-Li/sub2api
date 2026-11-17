//go:build unit || integration

package service

import (
	"context"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

func createShopFulfillmentTx(ctx context.Context, client *dbent.Client, order *dbent.PaymentOrder, product ShopProduct) error {
	return createShopFulfillmentSnapshotTx(ctx, client, order, product, nil)
}

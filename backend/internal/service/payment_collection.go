package service

import (
	"context"
	"fmt"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const maxCollectionAmountFen int64 = 100000000

// CreateCollectionOrder uses the existing durable administrative dispatch, but
// has its own operation namespace and never grants product entitlements.
func (s *PaymentService) CreateCollectionOrder(ctx context.Context, input OwnerTestOrderRequest) (*CreateOrderResponse, error) {
	input.collection = true
	return s.createAdministrativeOrder(ctx, input)
}

func collectionCanonicalAmount(fen int64) (float64, string, error) {
	if fen < 1 || fen > maxCollectionAmountFen {
		return 0, "", infraerrors.BadRequest("INVALID_COLLECTION_AMOUNT", "amount_fen must be between 1 and 100000000")
	}
	return float64(fen) / 100, fmt.Sprintf("%d.%02d", fen/100, fen%100), nil
}

func (c *ownerTestOrderContext) orderType() string {
	if c.input.collection {
		return payment.OrderTypeCollection
	}
	return payment.OrderTypeBalance
}

func (s *PaymentService) executeCollectionFulfillment(ctx context.Context, o *dbent.PaymentOrder, acquire paymentFulfillmentLeaseAcquirer) error {
	if o.Status == OrderStatusCompleted {
		return nil
	}
	ledger, err := ownerTestLedgerFromOrder(o)
	if err != nil || ledger.OwnerUserID != o.UserID || ledger.ProviderRequest.OrderType != payment.OrderTypeCollection ||
		ledger.ProviderRequest.ProductOrderNo != o.OutTradeNo || o.Amount != 0 || o.FeeRate != 0 || !paymentOrderUsesUnifiedPay(o) {
		return infraerrors.Conflict("COLLECTION_METADATA_INVALID", "collection order provenance is invalid")
	}
	_, amount, err := collectionCanonicalAmount(ledger.ProviderRequest.AmountFen)
	if err != nil || amount != ledger.ProviderRequest.Amount || float64(ledger.ProviderRequest.AmountFen)/100 != o.PayAmount {
		return infraerrors.Conflict("COLLECTION_METADATA_INVALID", "collection amount is invalid")
	}
	if o.Status != OrderStatusPaid && o.Status != OrderStatusFailed && o.Status != OrderStatusRecharging {
		return infraerrors.BadRequest("INVALID_STATUS", "collection has no confirmed payment to complete")
	}
	claimed, lease, err := acquire(ctx, o)
	if err != nil || lease == nil {
		return err
	}
	if claimed == nil || claimed.OrderType != payment.OrderTypeCollection || claimed.Amount != 0 {
		return infraerrors.Conflict("COLLECTION_METADATA_INVALID", "collection changed during completion")
	}
	// Completion has no redeem code, wallet, subscription, concurrency, or rebate
	// action. The common CAS/audit path records zero creditedAmount.
	return s.markCompleted(ctx, claimed, lease, "COLLECTION_SUCCESS")
}

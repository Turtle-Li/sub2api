package unifiedpay

import "context"

// LookupPaymentOrderByProductOrderNo returns a centrally scoped payment-order
// binding without exposing checkout material. Found is false only for a
// structurally valid, non-retryable scoped not-found response; callers must
// still apply their own local state and expiry fences before any terminal move.
func (g *Gateway) LookupPaymentOrderByProductOrderNo(ctx context.Context, productOrderNo string) (*PaymentOrderLookup, error) {
	if !g.Enabled() {
		return nil, ErrDisabled
	}
	result, found, err := g.client.lookupPaymentOrderByProductOrderNo(ctx, productOrderNo)
	if err != nil {
		return nil, err
	}
	lookup := &PaymentOrderLookup{Found: found}
	if !found {
		return lookup, nil
	}
	lookup.PaymentOrderID = result.PaymentOrderID
	lookup.ProductOrderNo = result.ProductOrderNo
	lookup.OrderType = result.OrderType
	lookup.AmountFen = result.AmountFen
	lookup.Currency = result.Currency
	lookup.PaymentMethod = result.PaymentMethod
	return lookup, nil
}

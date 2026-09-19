package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/payment/unifiedpay"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
	"github.com/google/uuid"
)

const (
	resetCardDispatchVersion     = 1
	resetCardDispatchSnapshotKey = "reset_card_dispatch"
	resetCardCheckoutSnapshotKey = "reset_card_checkout"
	resetCardCheckoutSnapshotV1  = 1
	// A provider may accept an order just before the caller observes a timeout.
	// Keep the create lease comfortably beyond the normal gateway request
	// window so another process cannot reclaim it while the first call is still
	// settling upstream. Replays remain safe because they query the deterministic
	// merchant order before another direct-provider create.
	resetCardDispatchLeaseDuration        = 2 * time.Minute
	resetCardCreateUnconfirmedAuditAction = "RESET_CARD_CREATE_UNCONFIRMED"
	// Unified payment requires at least five minutes when CreatePayment reaches
	// the provider. Reserve one more minute after a reclaimed dispatch lease for
	// deadline flooring and local request work, so a 5-7 minute configured order
	// timeout cannot create a checkout the provider must reject.
	resetCardProviderMinimumLifetime         = 5 * time.Minute
	resetCardOrderExpirySafetyMargin         = time.Minute
	resetCardMinimumExternalCheckoutLifetime = resetCardProviderMinimumLifetime + resetCardDispatchLeaseDuration + resetCardOrderExpirySafetyMargin
)

// resetCardDispatch is an on-row, fenced create lease. NeedsReconcile is set
// durably before a provider call, so a process crash or lost HTTP response can
// never make a later replay blindly create a second upstream checkout.
type resetCardDispatch struct {
	Version        int        `json:"version"`
	Generation     int64      `json:"generation"`
	ClaimToken     string     `json:"claim_token,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
	NeedsReconcile bool       `json:"needs_reconcile"`
}

type resetCardDispatchLease struct {
	version             time.Time
	generation          int64
	token               string
	previouslyUncertain bool
}

// resetCardCheckoutSnapshot retains only provider launch material that cannot
// be reconstructed by QueryOrder. URL and QR launches already have dedicated
// PaymentOrder columns; WeChat JSAPI's signed invocation payload does not.
// Provider snapshots are omitted from order DTOs, and this payload is returned
// only while its order is pending and unexpired.
type resetCardCheckoutSnapshot struct {
	Version    int                             `json:"version"`
	ResultType payment.CreatePaymentResultType `json:"result_type"`
	JSAPI      *payment.WechatJSAPIPayload     `json:"jsapi,omitempty"`
}

func withInitialResetCardDispatch(snapshot map[string]any) map[string]any {
	if snapshot == nil {
		snapshot = make(map[string]any)
	} else {
		snapshot = clonePaymentOrderSnapshot(snapshot)
	}
	snapshot[resetCardDispatchSnapshotKey] = resetCardDispatch{Version: resetCardDispatchVersion}
	return snapshot
}

func resetCardCheckoutFromOrder(order *dbent.PaymentOrder) (*resetCardCheckoutSnapshot, bool, error) {
	if order == nil || order.ProviderSnapshot == nil {
		return nil, false, nil
	}
	raw, ok := order.ProviderSnapshot[resetCardCheckoutSnapshotKey]
	if !ok || raw == nil {
		return nil, false, nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, true, fmt.Errorf("encode reset card checkout state: %w", err)
	}
	var checkout resetCardCheckoutSnapshot
	if err := json.Unmarshal(payload, &checkout); err != nil {
		return nil, true, fmt.Errorf("decode reset card checkout state: %w", err)
	}
	if checkout.Version != resetCardCheckoutSnapshotV1 ||
		checkout.ResultType != payment.CreatePaymentResultJSAPIReady ||
		!completeWechatJSAPIPayload(checkout.JSAPI) {
		return nil, true, errors.New("reset card checkout state is invalid")
	}
	return &checkout, true, nil
}

func completeWechatJSAPIPayload(payload *payment.WechatJSAPIPayload) bool {
	return payload != nil &&
		strings.TrimSpace(payload.AppID) != "" &&
		strings.TrimSpace(payload.TimeStamp) != "" &&
		strings.TrimSpace(payload.NonceStr) != "" &&
		strings.TrimSpace(payload.Package) != "" &&
		strings.TrimSpace(payload.SignType) != "" &&
		strings.TrimSpace(payload.PaySign) != ""
}

func resetCardDispatchFromOrder(order *dbent.PaymentOrder) (resetCardDispatch, bool, error) {
	if order == nil || order.ProviderSnapshot == nil {
		return resetCardDispatch{}, false, nil
	}
	raw, ok := order.ProviderSnapshot[resetCardDispatchSnapshotKey]
	if !ok || raw == nil {
		return resetCardDispatch{}, false, nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return resetCardDispatch{}, true, fmt.Errorf("encode reset card dispatch state: %w", err)
	}
	var dispatch resetCardDispatch
	if err := json.Unmarshal(payload, &dispatch); err != nil {
		return resetCardDispatch{}, true, fmt.Errorf("decode reset card dispatch state: %w", err)
	}
	if dispatch.Version != resetCardDispatchVersion || dispatch.Generation < 0 ||
		(dispatch.ClaimToken != "" && dispatch.LeaseExpiresAt == nil) {
		return resetCardDispatch{}, true, errors.New("reset card dispatch state is invalid")
	}
	return dispatch, true, nil
}

func resetCardDispatchTimestamp(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func resetCardDispatchNextVersion(previous, now time.Time) time.Time {
	previous = resetCardDispatchTimestamp(previous)
	now = resetCardDispatchTimestamp(now)
	if !now.After(previous) {
		return previous.Add(time.Microsecond)
	}
	return now
}

func resetCardDispatchActive(dispatch resetCardDispatch, now time.Time) bool {
	return dispatch.ClaimToken != "" && dispatch.LeaseExpiresAt != nil && dispatch.LeaseExpiresAt.After(now)
}

func (s *PaymentService) reopenUnconfirmedResetCardOrder(ctx context.Context, order *dbent.PaymentOrder) (*dbent.PaymentOrder, error) {
	if order == nil || order.OrderType != payment.OrderTypeResetCard || order.Status != OrderStatusFailed || order.PaidAt != nil {
		return order, nil
	}
	dispatch, present, err := resetCardDispatchFromOrder(order)
	if err != nil {
		return nil, err
	}
	if !present {
		dispatch = resetCardDispatch{Version: resetCardDispatchVersion}
	}
	now := resetCardDispatchTimestamp(time.Now())
	dispatch.ClaimToken = ""
	dispatch.LeaseExpiresAt = &now
	dispatch.NeedsReconcile = true
	snapshot := clonePaymentOrderSnapshot(order.ProviderSnapshot)
	if snapshot == nil {
		snapshot = make(map[string]any)
	}
	snapshot[resetCardDispatchSnapshotKey] = dispatch
	updated, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(order.ID),
		paymentorder.OrderTypeEQ(payment.OrderTypeResetCard),
		paymentorder.StatusEQ(OrderStatusFailed),
		paymentorder.PaidAtIsNil(),
	).SetStatus(OrderStatusPending).
		ClearFailedAt().
		ClearFailedReason().
		SetProviderSnapshot(snapshot).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("reopen unconfirmed reset card order: %w", err)
	}
	if updated == 1 {
		s.writeAuditLog(ctx, order.ID, "RESET_CARD_CREATE_STATE_REOPENED", "system", map[string]any{
			"reason": "legacy unpaid FAILED state requires provider reconciliation",
		})
	}
	reloaded, err := s.entClient.PaymentOrder.Get(ctx, order.ID)
	if err != nil {
		return nil, fmt.Errorf("reload reopened reset card order: %w", err)
	}
	return reloaded, nil
}

// claimResetCardDispatch holds a row lock only while replacing the create
// lease. No database transaction remains open during the provider request.
func (s *PaymentService) claimResetCardDispatch(ctx context.Context, initial *dbent.PaymentOrder, req CreateOrderRequest) (*dbent.PaymentOrder, *resetCardDispatchLease, bool, error) {
	if s == nil || s.entClient == nil || initial == nil {
		return nil, nil, false, infraerrors.ServiceUnavailable("RESET_CARD_UNAVAILABLE", "reset card payment order is unavailable")
	}
	for attempt := 0; attempt < 4; attempt++ {
		tx, err := s.entClient.Tx(ctx)
		if err != nil {
			return nil, nil, false, fmt.Errorf("begin reset card dispatch claim: %w", err)
		}
		var (
			terminal     *dbent.PaymentOrder
			claimToken   string
			generation   int64
			wasUncertain bool
			claimed      bool
			retry        bool
		)
		func() {
			defer func() { _ = tx.Rollback() }()
			query := tx.PaymentOrder.Query().Where(paymentorder.IDEQ(initial.ID))
			if tx.Client().Driver().Dialect() == dialect.Postgres {
				query.ForUpdate()
			}
			current, queryErr := query.Only(ctx)
			if queryErr != nil {
				err = fmt.Errorf("lock reset card payment order: %w", queryErr)
				return
			}
			if validationErr := validateResetCardOrderRecord(current, req, nil); validationErr != nil {
				err = validationErr
				return
			}
			if resetCardOrderHasReusableResponse(current) {
				terminal = current
				return
			}
			dispatch, present, dispatchErr := resetCardDispatchFromOrder(current)
			if dispatchErr != nil {
				err = infraerrors.Conflict("RESET_CARD_DISPATCH_STATE_INVALID", dispatchErr.Error())
				return
			}
			now := resetCardDispatchTimestamp(time.Now())
			if resetCardDispatchActive(dispatch, now) {
				err = infraerrors.Conflict("RESET_CARD_ORDER_IN_PROGRESS", "reset card payment checkout is still being created; retry with the same Idempotency-Key")
				return
			}
			// Rows written before this dispatch ledger existed are treated as
			// uncertain. They must query their original provider before any retry.
			wasUncertain = dispatch.NeedsReconcile || !present
			claimToken = uuid.NewString()
			generation = dispatch.Generation + 1
			leaseExpiresAt := resetCardDispatchTimestamp(now.Add(resetCardDispatchLeaseDuration))
			nextDispatch := resetCardDispatch{
				Version:        resetCardDispatchVersion,
				Generation:     generation,
				ClaimToken:     claimToken,
				LeaseExpiresAt: &leaseExpiresAt,
				// This bit is committed before the network call. A pre-network
				// validation failure explicitly clears it when releasing the lease.
				NeedsReconcile: true,
			}
			nextSnapshot := clonePaymentOrderSnapshot(current.ProviderSnapshot)
			if nextSnapshot == nil {
				nextSnapshot = make(map[string]any)
			}
			nextSnapshot[resetCardDispatchSnapshotKey] = nextDispatch
			nextVersion := resetCardDispatchNextVersion(current.UpdatedAt, now)
			updated, updateErr := tx.PaymentOrder.Update().Where(
				paymentorder.IDEQ(current.ID),
				paymentorder.StatusEQ(OrderStatusPending),
				paymentorder.PaymentTradeNoEQ(""),
			).SetProviderSnapshot(nextSnapshot).SetUpdatedAt(nextVersion).Save(ctx)
			if updateErr != nil {
				err = fmt.Errorf("claim reset card dispatch: %w", updateErr)
				return
			}
			if updated != 1 {
				retry = true
				return
			}
			if commitErr := tx.Commit(); commitErr != nil {
				err = fmt.Errorf("commit reset card dispatch claim: %w", commitErr)
				return
			}
			claimed = true
		}()
		if err != nil {
			return nil, nil, false, err
		}
		if terminal != nil {
			return terminal, nil, false, nil
		}
		if retry || !claimed {
			continue
		}
		current, err := s.entClient.PaymentOrder.Get(ctx, initial.ID)
		if err != nil {
			return nil, nil, false, fmt.Errorf("reload reset card dispatch claim: %w", err)
		}
		dispatch, _, err := resetCardDispatchFromOrder(current)
		if err != nil || dispatch.Generation != generation || dispatch.ClaimToken != claimToken {
			return nil, nil, false, infraerrors.Conflict("RESET_CARD_DISPATCH_CONFLICT", "reset card dispatch claim was lost")
		}
		return current, &resetCardDispatchLease{
			version:             current.UpdatedAt,
			generation:          generation,
			token:               claimToken,
			previouslyUncertain: wasUncertain,
		}, true, nil
	}
	return nil, nil, false, infraerrors.Conflict("RESET_CARD_DISPATCH_CONFLICT", "reset card dispatch changed concurrently")
}

func (s *PaymentService) releaseResetCardDispatch(ctx context.Context, order *dbent.PaymentOrder, lease *resetCardDispatchLease, needsReconcile bool) (bool, error) {
	if order == nil || lease == nil {
		return false, infraerrors.ServiceUnavailable("RESET_CARD_UNAVAILABLE", "reset card dispatch lease is unavailable")
	}
	dispatch, _, err := resetCardDispatchFromOrder(order)
	if err != nil {
		return false, err
	}
	if dispatch.Generation != lease.generation || dispatch.ClaimToken != lease.token {
		return false, nil
	}
	now := resetCardDispatchTimestamp(time.Now())
	dispatch.ClaimToken = ""
	dispatch.LeaseExpiresAt = &now
	dispatch.NeedsReconcile = needsReconcile
	nextSnapshot := clonePaymentOrderSnapshot(order.ProviderSnapshot)
	if nextSnapshot == nil {
		nextSnapshot = make(map[string]any)
	}
	nextSnapshot[resetCardDispatchSnapshotKey] = dispatch
	updated, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(order.ID),
		paymentorder.StatusEQ(OrderStatusPending),
		paymentorder.PaymentTradeNoEQ(""),
		paymentorder.UpdatedAtEQ(lease.version),
	).SetProviderSnapshot(nextSnapshot).SetUpdatedAt(resetCardDispatchNextVersion(lease.version, now)).Save(ctx)
	if err != nil {
		return false, fmt.Errorf("release reset card dispatch: %w", err)
	}
	return updated == 1, nil
}

// ensureResetCardProviderCreateEnabled runs after any safe reconciliation and
// immediately before a new upstream checkout. Turning payment off must still
// allow recovery of an existing checkout, but it must never open another one.
func (s *PaymentService) ensureResetCardProviderCreateEnabled(ctx context.Context) error {
	if s == nil || s.configService == nil {
		return infraerrors.ServiceUnavailable("PAYMENT_CONFIG_UNAVAILABLE", "payment configuration is unavailable")
	}
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return fmt.Errorf("get payment config before reset card provider create: %w", err)
	}
	if cfg == nil || !cfg.Enabled {
		return ErrResetCardPaymentDisabled
	}
	return nil
}

func (s *PaymentService) invokeResetCardProvider(ctx context.Context, order *dbent.PaymentOrder, req CreateOrderRequest, cfg *PaymentConfig) (*CreateOrderResponse, error) {
	claimed, lease, dispatch, err := s.claimResetCardDispatch(ctx, order, req)
	if err != nil {
		return nil, err
	}
	if !dispatch {
		return buildResetCardOrderResponse(claimed), nil
	}
	prov, sel, err := s.resetCardProviderForOrder(ctx, claimed, req)
	if err != nil {
		_, releaseErr := s.releaseResetCardDispatch(ctx, claimed, lease, lease.previouslyUncertain)
		if releaseErr != nil {
			return nil, releaseErr
		}
		return nil, err
	}
	needsReconcileOnRelease := lease.previouslyUncertain
	if lease.previouslyUncertain && sel.ProviderKey != payment.TypeUnifiedPay {
		handled, response, reconcileErr := s.reconcileResetCardBeforeCreate(ctx, claimed, lease, prov)
		if handled {
			return response, reconcileErr
		}
		// The original provider explicitly proved that the deterministic order
		// does not exist. A later local validation failure may therefore release
		// the claim without preserving an upstream-uncertain marker.
		needsReconcileOnRelease = false
	}
	// Querying the deterministic provider order is independent of the payer's
	// short-lived OAuth context. Validate that context only if reconciliation
	// proved that no upstream checkout exists and a new JSAPI checkout is about
	// to be created.
	if err := validateResetCardDispatchBinding(claimed, req, sel); err != nil {
		_, releaseErr := s.releaseResetCardDispatch(ctx, claimed, lease, needsReconcileOnRelease)
		if releaseErr != nil {
			return nil, releaseErr
		}
		return nil, err
	}
	if err := s.validateSelectedCreateOrderInstance(ctx, req, sel); err != nil {
		_, releaseErr := s.releaseResetCardDispatch(ctx, claimed, lease, needsReconcileOnRelease)
		if releaseErr != nil {
			return nil, releaseErr
		}
		return nil, err
	}

	canonicalReturnURL, err := CanonicalizeReturnURL(req.ReturnURL, req.SrcHost, req.SrcURL)
	if err != nil {
		_, releaseErr := s.releaseResetCardDispatch(ctx, claimed, lease, lease.previouslyUncertain)
		if releaseErr != nil {
			return nil, releaseErr
		}
		return nil, err
	}
	resumeToken := ""
	if resume := s.paymentResume(); resume != nil && canonicalReturnURL != "" && resume.isSigningConfigured() {
		resumeToken, err = resume.CreateToken(ResumeTokenClaims{
			OrderID:            claimed.ID,
			UserID:             claimed.UserID,
			ProviderInstanceID: sel.InstanceID,
			ProviderKey:        sel.ProviderKey,
			PaymentType:        claimed.PaymentType,
			CanonicalReturnURL: canonicalReturnURL,
		})
		if err != nil {
			_, releaseErr := s.releaseResetCardDispatch(ctx, claimed, lease, lease.previouslyUncertain)
			if releaseErr != nil {
				return nil, releaseErr
			}
			return nil, fmt.Errorf("create payment resume token: %w", err)
		}
	}
	providerReturnURL := canonicalReturnURL
	if sel.ProviderKey == payment.TypeUnifiedPay {
		if providerReturnURL == "" || s.unifiedPayment == nil || providerReturnURL != s.unifiedPayment.ReturnURL() {
			_, releaseErr := s.releaseResetCardDispatch(ctx, claimed, lease, lease.previouslyUncertain)
			if releaseErr != nil {
				return nil, releaseErr
			}
			return nil, infraerrors.BadRequest("INVALID_RETURN_URL", "return URL must match the configured Sub2 payment result page")
		}
	} else {
		providerReturnURL, err = buildPaymentReturnURL(canonicalReturnURL, claimed.ID, claimed.OutTradeNo, resumeToken)
		if err != nil {
			_, releaseErr := s.releaseResetCardDispatch(ctx, claimed, lease, lease.previouslyUncertain)
			if releaseErr != nil {
				return nil, releaseErr
			}
			return nil, err
		}
	}
	subject := applyPaymentProductNameAffix("Subscription reset card", cfg)
	providerReq := buildProviderCreatePaymentRequest(CreateOrderRequest{
		PaymentType: claimed.PaymentType,
		OrderType:   claimed.OrderType,
		OpenID:      req.OpenID,
		ClientIP:    claimed.ClientIP,
		IsMobile:    req.IsMobile,
		ReturnURL:   providerReturnURL,
	}, sel, claimed.OutTradeNo, payment.FormatAmountForCurrency(claimed.PayAmount, payment.DefaultPaymentCurrency), subject)
	providerReq.AlipayMobilePrecreate = shouldUseAlipayMobilePrecreate(req, cfg, sel)
	if err := s.ensureResetCardProviderCreateEnabled(ctx); err != nil {
		_, releaseErr := s.releaseResetCardDispatch(ctx, claimed, lease, needsReconcileOnRelease)
		if releaseErr != nil {
			return nil, releaseErr
		}
		return nil, err
	}
	providerReq.ExpiresInSeconds = paymentOrderExpiresInSeconds(claimed.ExpiresAt, s.resetCardCurrentTime())
	if providerReq.ExpiresInSeconds < int(resetCardProviderMinimumLifetime/time.Second) {
		s.writeAuditLog(ctx, claimed.ID, resetCardCreateUnconfirmedAuditAction, sel.ProviderKey, map[string]any{
			"out_trade_no":       claimed.OutTradeNo,
			"payment_type":       req.PaymentType,
			"reason":             "not enough local lifetime remains to create a safe upstream checkout",
			"expires_in_seconds": providerReq.ExpiresInSeconds,
		})
		// Keep the conservative reconciliation marker. A later request must query
		// the deterministic order again rather than infer that no provider order
		// exists from an earlier observation.
		_, releaseErr := s.releaseResetCardDispatch(ctx, claimed, lease, true)
		if releaseErr != nil {
			return nil, releaseErr
		}
		return nil, resetCardCreateUnconfirmedError(errors.New("insufficient provider checkout lifetime"))
	}
	finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
	providerResp, providerErr := prov.CreatePayment(ctx, providerReq)
	finishProviderCall()
	if providerErr != nil {
		return s.handleResetCardProviderCreateError(ctx, claimed, lease, prov, req, sel, providerErr)
	}
	sanitizeCreatePaymentResponseDetails(providerResp)
	if providerResp == nil ||
		(strings.TrimSpace(providerResp.PayURL) == "" && strings.TrimSpace(providerResp.QRCode) == "" && !completeWechatJSAPIPayload(providerResp.JSAPI)) {
		return s.handleResetCardProviderCreateError(ctx, claimed, lease, prov, req, sel, errors.New("payment provider returned an incomplete checkout response"))
	}
	return s.finishResetCardProviderSuccess(ctx, claimed, lease, req, sel, providerReq, providerResp, resumeToken)
}

func (s *PaymentService) resetCardProviderForOrder(ctx context.Context, order *dbent.PaymentOrder, req CreateOrderRequest) (payment.Provider, *payment.InstanceSelection, error) {
	snapshot := psOrderProviderSnapshot(order)
	if snapshot == nil || snapshot.ProviderKey == "" || !strings.EqualFold(snapshot.Currency, payment.DefaultPaymentCurrency) {
		return nil, nil, infraerrors.Conflict("RESET_CARD_PAYMENT_BINDING_CHANGED", "the reset card order has no valid original payment route")
	}
	var (
		prov payment.Provider
		sel  *payment.InstanceSelection
		err  error
	)
	if snapshot.ProviderKey == payment.TypeUnifiedPay {
		if s.unifiedPayment == nil || !s.unifiedPayment.Enabled() {
			return nil, nil, unifiedpay.ErrDisabled
		}
		prov = s.unifiedPayment
		sel = s.unifiedPayment.Selection(order.PaymentType)
		if sel == nil {
			return nil, nil, infraerrors.ServiceUnavailable("RESET_CARD_PAYMENT_ROUTE_UNAVAILABLE", "the original payment route is unavailable")
		}
	} else {
		inst, instanceErr := s.resolveSnapshotOrderProviderInstance(ctx, order, snapshot)
		if instanceErr != nil {
			return nil, nil, instanceErr
		}
		if inst == nil || s.loadBalancer == nil {
			return nil, nil, infraerrors.ServiceUnavailable("RESET_CARD_PAYMENT_ROUTE_UNAVAILABLE", "the original payment route is unavailable")
		}
		config, configErr := s.loadBalancer.GetInstanceConfig(ctx, inst.ID)
		if configErr != nil {
			return nil, nil, fmt.Errorf("load reset card provider config: %w", configErr)
		}
		if config == nil {
			return nil, nil, infraerrors.ServiceUnavailable("RESET_CARD_PAYMENT_ROUTE_UNAVAILABLE", "the original payment route configuration is unavailable")
		}
		// The checkout product is part of the immutable route binding. Preserve
		// the order's original mode even if an administrator later edits the
		// provider instance while an uncertain create is being recovered.
		if snapshot.PaymentMode != "" {
			config["paymentMode"] = snapshot.PaymentMode
		} else {
			delete(config, "paymentMode")
		}
		sel = &payment.InstanceSelection{
			InstanceID:     strconv.FormatInt(inst.ID, 10),
			ProviderKey:    inst.ProviderKey,
			Config:         config,
			SupportedTypes: inst.SupportedTypes,
			PaymentMode:    snapshot.PaymentMode,
		}
		prov, err = createPaymentProviderFromInstance(sel.ProviderKey, sel.InstanceID, sel.Config)
		if err != nil {
			return nil, nil, fmt.Errorf("create reset card provider from original instance: %w", err)
		}
	}
	sel.PaymentMode = snapshot.PaymentMode
	return prov, sel, nil
}

func validateResetCardDispatchBinding(order *dbent.PaymentOrder, req CreateOrderRequest, sel *payment.InstanceSelection) error {
	stored := psOrderProviderSnapshot(order)
	current := psOrderProviderSnapshot(&dbent.PaymentOrder{ProviderSnapshot: buildPaymentOrderProviderSnapshot(sel, CreateOrderRequest{
		PaymentType: order.PaymentType,
		OpenID:      req.OpenID,
		IsMobile:    req.IsMobile,
	})})
	if stored == nil || current == nil || !strings.EqualFold(stored.ProviderKey, current.ProviderKey) ||
		!strings.EqualFold(stored.ProviderInstanceID, current.ProviderInstanceID) ||
		!strings.EqualFold(stored.Currency, current.Currency) ||
		!resetCardBindingValueEqual(stored.CheckoutMode, current.CheckoutMode) ||
		!resetCardBindingValueEqual(stored.MerchantAppID, current.MerchantAppID) ||
		!resetCardBindingValueEqual(stored.MerchantID, current.MerchantID) ||
		!resetCardBindingValueEqual(stored.Environment, current.Environment) ||
		!resetCardBindingValueEqual(stored.OrganizationID, current.OrganizationID) ||
		!resetCardBindingValueEqual(stored.ProductID, current.ProductID) ||
		!resetCardBindingValueEqual(stored.AppID, current.AppID) {
		return infraerrors.Conflict("RESET_CARD_PAYMENT_BINDING_CHANGED", "the original reset card payment route changed and cannot be reused")
	}
	return nil
}

func resetCardBindingValueEqual(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

// reconcileResetCardBeforeCreate runs only after a prior attempt may have
// reached the provider. It must prove absence before another CreatePayment is
// permitted; pending, query errors, and malformed amounts stay uncertain.
func (s *PaymentService) reconcileResetCardBeforeCreate(ctx context.Context, order *dbent.PaymentOrder, lease *resetCardDispatchLease, prov payment.Provider) (bool, *CreateOrderResponse, error) {
	finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
	queryResp, queryErr := prov.QueryOrder(ctx, paymentOrderQueryReference(order, prov))
	finishProviderCall()
	if queryErr != nil || queryResp == nil {
		s.writeAuditLog(ctx, order.ID, resetCardCreateUnconfirmedAuditAction, prov.ProviderKey(), map[string]any{"reason": "provider query did not confirm the prior create attempt"})
		return true, nil, resetCardCreateUnconfirmedError(queryErr)
	}
	switch queryResp.Status {
	case payment.ProviderStatusPaid:
		tradeNo := strings.TrimSpace(queryResp.TradeNo)
		if tradeNo == "" {
			tradeNo = order.OutTradeNo
		}
		if err := s.confirmPayment(ctx, order.ID, tradeNo, queryResp.Amount, prov.ProviderKey(), queryResp.Metadata); err != nil {
			return true, nil, err
		}
		reloaded, err := s.entClient.PaymentOrder.Get(ctx, order.ID)
		if err != nil {
			return true, nil, fmt.Errorf("reload reconciled reset card order: %w", err)
		}
		return true, buildResetCardOrderResponse(reloaded), nil
	case payment.ProviderStatusFailed, payment.ProviderStatusRefunded:
		return true, nil, s.finishResetCardProviderFailure(ctx, order, lease, errors.New("the prior upstream checkout is closed"))
	case payment.ProviderStatusPending:
		// An amount equal to the order proves that an upstream checkout exists.
		// Only a provider's explicit not-found contract permits another create.
		if isValidProviderAmount(queryResp.Amount) && math.Abs(queryResp.Amount-order.PayAmount) <= paymentAmountToleranceForCurrency(payment.DefaultPaymentCurrency) {
			s.writeAuditLog(ctx, order.ID, resetCardCreateUnconfirmedAuditAction, prov.ProviderKey(), map[string]any{"reason": "the upstream checkout exists but local checkout details are unavailable"})
			return true, nil, resetCardCreateUnconfirmedError(nil)
		}
		if queryResp.Metadata[payment.QueryMetadataOrderNotFound] == "true" {
			return false, nil, nil
		}
	}
	s.writeAuditLog(ctx, order.ID, resetCardCreateUnconfirmedAuditAction, prov.ProviderKey(), map[string]any{"reason": "provider returned an unrecognized state for the prior create attempt"})
	return true, nil, resetCardCreateUnconfirmedError(nil)
}

func (s *PaymentService) handleResetCardProviderCreateError(ctx context.Context, order *dbent.PaymentOrder, lease *resetCardDispatchLease, prov payment.Provider, req CreateOrderRequest, sel *payment.InstanceSelection, providerErr error) (*CreateOrderResponse, error) {
	// UnifiedPay already creates with a deterministic central idempotency key
	// and reconciles ambiguous responses by product_order_no. It cannot query
	// by that product number through Provider.QueryOrder before the remote UUID
	// has been persisted, so preserve the local uncertain state for a safe
	// idempotent replay.
	if sel.ProviderKey == payment.TypeUnifiedPay {
		// Only a first, provably unadmitted create can relinquish the fence.
		// A rejection of a later replay says nothing about an earlier uncertain
		// request; conflicts, transport errors and retryable responses stay fenced.
		var apiErr *unifiedpay.APIError
		definitelyRejected := errors.Is(providerErr, unifiedpay.ErrInvalidRequest) ||
			(errors.As(providerErr, &apiErr) && apiErr.StatusCode == 400 && apiErr.Code == "invalid_request" && !apiErr.Retryable)
		if lease != nil && !lease.previouslyUncertain && definitelyRejected {
			released, releaseErr := s.releaseResetCardDispatch(ctx, order, lease, false)
			if releaseErr != nil {
				return nil, releaseErr
			}
			if !released {
				return nil, infraerrors.Conflict("RESET_CARD_DISPATCH_CONFLICT", "reset card payment state changed while rejection was recorded")
			}
			detail := map[string]any{"reason": "payment service rejected the create before order admission"}
			if apiErr != nil {
				detail["upstream_status"] = apiErr.StatusCode
				detail["upstream_code"] = apiErr.Code
			}
			s.writeAuditLog(ctx, order.ID, "RESET_CARD_CREATE_REJECTED", sel.ProviderKey, detail)
			return nil, infraerrors.ServiceUnavailable("RESET_CARD_PAYMENT_REJECTED", "payment service rejected this checkout; retry after the payment configuration is corrected")
		}
		s.writeAuditLog(ctx, order.ID, resetCardCreateUnconfirmedAuditAction, sel.ProviderKey, map[string]any{
			"out_trade_no": order.OutTradeNo,
			"payment_type": req.PaymentType,
			"reason":       "unified payment create result could not be persisted or reconciled",
		})
		return nil, resetCardCreateUnconfirmedError(providerErr)
	}
	finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
	queryResp, queryErr := prov.QueryOrder(ctx, paymentOrderQueryReference(order, prov))
	finishProviderCall()
	if queryErr == nil && queryResp != nil {
		switch queryResp.Status {
		case payment.ProviderStatusPaid:
			tradeNo := strings.TrimSpace(queryResp.TradeNo)
			if tradeNo == "" {
				tradeNo = order.OutTradeNo
			}
			if err := s.confirmPayment(ctx, order.ID, tradeNo, queryResp.Amount, prov.ProviderKey(), queryResp.Metadata); err != nil {
				return nil, err
			}
			reloaded, err := s.entClient.PaymentOrder.Get(ctx, order.ID)
			if err != nil {
				return nil, fmt.Errorf("reload reset card order after create reconciliation: %w", err)
			}
			return buildResetCardOrderResponse(reloaded), nil
		case payment.ProviderStatusFailed, payment.ProviderStatusRefunded:
			return nil, s.finishResetCardProviderFailure(ctx, order, lease, providerErr)
		}
	}
	s.writeAuditLog(ctx, order.ID, resetCardCreateUnconfirmedAuditAction, sel.ProviderKey, map[string]any{
		"out_trade_no": order.OutTradeNo,
		"payment_type": req.PaymentType,
		"reason":       "provider create result could not be reconciled",
	})
	return nil, resetCardCreateUnconfirmedError(providerErr)
}

func resetCardCreateUnconfirmedError(cause error) error {
	message := "reset card payment creation is still being confirmed; retry with the same Idempotency-Key"
	_ = cause // details stay in server logs/audit and are never exposed to callers
	return infraerrors.ServiceUnavailable("RESET_CARD_PAYMENT_CREATE_UNCONFIRMED", message)
}

func (s *PaymentService) finishResetCardProviderSuccess(ctx context.Context, order *dbent.PaymentOrder, lease *resetCardDispatchLease, req CreateOrderRequest, sel *payment.InstanceSelection, providerReq payment.CreatePaymentRequest, providerResp *payment.CreatePaymentResponse, resumeToken string) (*CreateOrderResponse, error) {
	dispatch, _, err := resetCardDispatchFromOrder(order)
	if err != nil || dispatch.Generation != lease.generation || dispatch.ClaimToken != lease.token {
		return s.replayResetCardOrder(ctx, req)
	}
	dispatch.ClaimToken = ""
	dispatch.NeedsReconcile = false
	now := resetCardDispatchTimestamp(time.Now())
	dispatch.LeaseExpiresAt = &now
	snapshot := clonePaymentOrderSnapshot(order.ProviderSnapshot)
	if snapshot == nil {
		snapshot = make(map[string]any)
	}
	if sel.ProviderKey == payment.TypeUnifiedPay {
		snapshot["payment_order_id"] = strings.TrimSpace(providerResp.TradeNo)
	}
	if completeWechatJSAPIPayload(providerResp.JSAPI) {
		snapshot[resetCardCheckoutSnapshotKey] = resetCardCheckoutSnapshot{
			Version:    resetCardCheckoutSnapshotV1,
			ResultType: payment.CreatePaymentResultJSAPIReady,
			JSAPI:      providerResp.JSAPI,
		}
	}
	snapshot[resetCardDispatchSnapshotKey] = dispatch
	update := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(order.ID),
		paymentorder.StatusEQ(OrderStatusPending),
		paymentorder.PaymentTradeNoEQ(""),
		paymentorder.UpdatedAtEQ(lease.version),
	).SetNillablePaymentTradeNo(psNilIfEmpty(providerResp.TradeNo)).
		SetNillablePayURL(psNilIfEmpty(providerResp.PayURL)).
		SetNillableQrCode(psNilIfEmpty(providerResp.QRCode)).
		SetProviderSnapshot(snapshot)
	effectiveExpiresAt := resetCardDispatchTimestamp(order.ExpiresAt)
	if !providerResp.ExpiresAt.IsZero() {
		providerExpiresAt := resetCardDispatchTimestamp(providerResp.ExpiresAt)
		if providerExpiresAt.Before(effectiveExpiresAt) {
			effectiveExpiresAt = providerExpiresAt
		}
	}
	// Never extend a reset-card checkout beyond the locally committed timeout,
	// which is already capped by the subscription expiry snapshot.
	if !effectiveExpiresAt.After(now) {
		return nil, resetCardCreateUnconfirmedError(errors.New("provider checkout expiry is not in the future"))
	}
	update.SetExpiresAt(effectiveExpiresAt)
	updated, err := update.Save(ctx)
	if err != nil {
		return nil, resetCardCreateUnconfirmedError(fmt.Errorf("persist provider checkout: %w", err))
	}
	if updated != 1 {
		return s.replayResetCardOrder(ctx, req)
	}
	reloaded, err := s.entClient.PaymentOrder.Get(ctx, order.ID)
	if err != nil {
		return nil, resetCardCreateUnconfirmedError(fmt.Errorf("reload persisted checkout: %w", err))
	}
	s.writeAuditLog(ctx, order.ID, "ORDER_CREATED", fmt.Sprintf("user:%d", req.UserID), map[string]any{
		"paymentAmount":  req.Amount,
		"creditedAmount": order.Amount,
		"payAmount":      order.PayAmount,
		"paymentType":    req.PaymentType,
		"orderType":      req.OrderType,
		"paymentSource":  NormalizePaymentSource(req.PaymentSource),
	})
	resultType := providerResp.ResultType
	if resultType == "" {
		resultType = payment.CreatePaymentResultOrderCreated
	}
	if providerResp.Currency == "" {
		providerResp.Currency = payment.DefaultPaymentCurrency
	}
	response := buildCreateOrderResponse(reloaded, req, reloaded.PayAmount, sel, providerResp, resultType)
	response.ResumeToken = resumeToken
	response.AlipayMobilePrecreateDeepLink = providerReq.AlipayMobilePrecreate && strings.TrimSpace(providerResp.QRCode) != ""
	return response, nil
}

func (s *PaymentService) finishResetCardProviderFailure(ctx context.Context, order *dbent.PaymentOrder, lease *resetCardDispatchLease, providerErr error) error {
	if lease == nil {
		return infraerrors.ServiceUnavailable("RESET_CARD_UNAVAILABLE", "reset card dispatch lease is unavailable")
	}
	now := time.Now()
	reason := "reset card payment provider rejected or closed the checkout"
	updated, err := s.entClient.PaymentOrder.Update().Where(
		paymentorder.IDEQ(order.ID),
		paymentorder.StatusEQ(OrderStatusPending),
		paymentorder.PaidAtIsNil(),
		paymentorder.UpdatedAtEQ(lease.version),
	).SetStatus(OrderStatusFailed).SetFailedAt(now).SetFailedReason(reason).Save(ctx)
	if err != nil {
		return fmt.Errorf("mark reset card checkout failed: %w", err)
	}
	if updated == 0 {
		current, getErr := s.entClient.PaymentOrder.Get(ctx, order.ID)
		if getErr == nil && (current.Status == OrderStatusPaid || current.Status == OrderStatusRecharging || current.Status == OrderStatusCompleted) {
			return nil
		}
		return infraerrors.Conflict("RESET_CARD_DISPATCH_CONFLICT", "reset card payment state changed while provider failure was recorded")
	}
	s.writeAuditLog(ctx, order.ID, "RESET_CARD_CHECKOUT_FAILED", "payment_provider", map[string]any{"reason": reason})
	if providerErr == nil {
		providerErr = errors.New(reason)
	}
	return classifyCreatePaymentError(CreateOrderRequest{PaymentType: order.PaymentType}, psStringValue(order.ProviderKey), providerErr)
}

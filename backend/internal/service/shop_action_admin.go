package service

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/shoporderaction"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

type ShopAutomationStatus struct {
	Status         string     `json:"status"`
	ResultKind     string     `json:"result_kind"`
	ActionID       string     `json:"action_id,omitempty"`
	Attempts       int        `json:"attempts,omitempty"`
	ClaimRevision  int        `json:"claim_revision,omitempty"`
	NextAttemptAt  *time.Time `json:"next_attempt_at,omitempty"`
	ErrorKind      string     `json:"error_kind,omitempty"`
	CanRetry       bool       `json:"can_retry,omitempty"`
	CanRevoke      bool       `json:"can_revoke,omitempty"`
	RevokeRequired bool       `json:"revoke_required,omitempty"`
}

type ShopIntegrationOption struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	Adapter string `json:"adapter"`
	Enabled bool   `json:"enabled"`
}

func (s *ShopService) ActionIntegrations() []ShopIntegrationOption {
	out := make([]ShopIntegrationOption, 0, len(s.actionsCfg.Integrations))
	for _, i := range s.actionsCfg.Integrations {
		out = append(out, ShopIntegrationOption{i.ID, i.Version, i.Adapter, s.actionsCfg.Enabled && i.Enabled})
	}
	return out
}

func (s *ShopService) attachShopAutomation(ctx context.Context, d *ShopOrderDetail, admin bool) error {
	d.DeliveryKind = "text"
	row, err := s.client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(d.OrderID)).Only(ctx)
	if dbent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	d.CanStart, d.CanComplete, d.CanRevealInput = false, false, false
	d.Product.AutomaticFulfillment = true
	d.Automation = &ShopAutomationStatus{Status: row.Status, ResultKind: row.ResultKind}
	if row.Status == shopaction.Succeeded {
		d.DeliveryKind = row.ResultKind
	}
	if admin {
		a := d.Automation
		a.ActionID, a.Attempts, a.ClaimRevision, a.NextAttemptAt, a.ErrorKind = row.ActionKey, row.Attempts, row.ClaimRevision, &row.NextAttemptAt, row.ErrorKind
		snapshot, e := decodeShopAction(row)
		native := e == nil && snapshot.Binding.Adapter == shopaction.Native
		a.CanRetry = e == nil && (row.Status == shopaction.Review || row.Status == shopaction.Pending) &&
			(row.CompensationReason != "" && native || row.CompensationReason == "" && time.Now().Before(row.DeadlineAt))
		a.CanRevoke = native && row.Status != shopaction.Revoked && row.Status != shopaction.Compensating && row.Status != shopaction.Cancelled
		a.RevokeRequired = native && row.Status != shopaction.Revoked && row.Status != shopaction.Cancelled
	}
	return nil
}

type ShopAutomationInput struct {
	Action        string `json:"action"`
	ClaimRevision int    `json:"claim_revision"`
}

func (s *ShopService) ManageAutomation(ctx context.Context, orderID, adminID int64, in ShopAutomationInput) (*ShopOrderDetail, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	o, err := lockUnifiedRefundOrder(ctx, client, orderID)
	if err != nil {
		return nil, err
	}
	if _, err = lockShopFulfillment(ctx, client, orderID); err != nil {
		return nil, err
	}
	row, err := lockShopOrderAction(ctx, client, orderID)
	if err != nil {
		return nil, err
	}
	if row.ClaimRevision != in.ClaimRevision {
		return nil, shopConflict()
	}
	snapshot, err := decodeShopAction(row)
	if err != nil {
		return nil, infraerrors.Conflict("SHOP_ACTION_REVIEW_REQUIRED", "原始交付配置异常，请核对售后，不能重新发货")
	}
	state, reason := shopaction.Pending, row.CompensationReason
	switch in.Action {
	case "retry":
		if row.Status != shopaction.Review && row.Status != shopaction.Pending {
			return nil, shopConflict()
		}
		if reason != "" {
			if snapshot.Binding.Adapter != shopaction.Native {
				return nil, infraerrors.Conflict("SHOP_ACTION_REVIEW_REQUIRED", "外部操作无法自动撤销，请联系服务方核对售后")
			}
			state = shopaction.Compensating
		} else {
			if !shopPaidActionAllowed(o) || !time.Now().Before(row.DeadlineAt) {
				return nil, infraerrors.Conflict("SHOP_ACTION_NOT_ACTIONABLE", "当前资金状态或交付期限不允许重试")
			}
			if err = ensureShopFinancialFence(ctx, client, o.ID); err != nil {
				return nil, err
			}
		}
	case "revoke":
		if snapshot.Binding.Adapter != shopaction.Native || row.Status == shopaction.Revoked || row.Status == shopaction.Cancelled {
			return nil, infraerrors.Conflict("SHOP_REVOKE_UNSUPPORTED", "当前操作不支持自动撤销，请人工核对")
		}
		state, reason = shopaction.Compensating, "ADMIN_REVOKE"
	default:
		return nil, infraerrors.BadRequest("SHOP_ACTION_INVALID", "请选择重试或撤销")
	}
	if _, err = s.actionAdapterFor(snapshot.Binding, state == shopaction.Compensating); err != nil {
		return nil, infraerrors.Conflict("SHOP_INTEGRATION_UNAVAILABLE", "原集成配置不可用，请先恢复同一版本的集成")
	}
	if err = client.ShopOrderAction.UpdateOne(row).SetStatus(state).SetCompensationReason(reason).SetErrorKind("").AddClaimRevision(1).ClearLeaseUntil().SetNextAttemptAt(time.Now()).Exec(ctx); err != nil {
		return nil, err
	}
	if err = writePaymentAuditTx(ctx, client, o.ID, fmt.Sprintf("SHOP_ACTION_%s_%d", in.Action, row.ClaimRevision+1), fmt.Sprintf("admin:%d", adminID), map[string]any{"action_id": row.ActionKey, "claim_revision": row.ClaimRevision + 1}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.Order(ctx, orderID, adminID, true)
}

package service

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/shoporderaction"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

func (s *ShopService) renewShopAction(ctx context.Context, c *shopOrderActionClaim) error {
	count, err := s.client.ShopOrderAction.Update().Where(shoporderaction.IDEQ(c.row.ID), shoporderaction.ClaimRevisionEQ(c.row.ClaimRevision),
		shoporderaction.StatusIn(shopaction.Running, shopaction.Compensating), shoporderaction.LeaseUntilGT(time.Now())).SetLeaseUntil(time.Now().Add(shopActionLease)).Save(ctx)
	if err != nil {
		return err
	}
	if count != 1 {
		return errShopActionLeaseLost
	}
	return nil
}

func (s *ShopService) actionHeartbeat(parent context.Context, c *shopOrderActionClaim) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.renewShopAction(ctx, c) != nil {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(); <-done }
}

func (s *ShopService) executeShopAction(ctx context.Context, c *shopOrderActionClaim) error {
	if !runtimegate.SharedWorkAllowed() || ctx.Err() != nil {
		return context.Canceled
	}
	if err := s.renewShopAction(ctx, c); err != nil {
		return err
	}
	ctx, stop := s.actionHeartbeat(ctx, c)
	defer stop()
	adapter, err := s.actionAdapterFor(c.snapshot.Binding, c.row.CompensationReason != "")
	if err != nil {
		return s.failShopAction(ctx, c, err)
	}
	if c.row.Status == shopaction.Compensating {
		return s.executeShopCompensation(ctx, c, adapter)
	}
	result, err := adapter.Execute(ctx, c.snapshot.Request)
	if err != nil {
		return s.failShopAction(ctx, c, err)
	}
	if !shopaction.ValidResult(c.snapshot.Request, result) {
		return s.failShopAction(ctx, c, &shopaction.Failure{Kind: "RESULT_INVALID"})
	}
	err = s.completeShopAction(ctx, c, result)
	if errors.Is(err, errShopActionCompensate) {
		return s.executeShopCompensation(ctx, c, adapter)
	}
	return err
}

func (s *ShopService) failShopAction(ctx context.Context, c *shopOrderActionClaim, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	failure := shopaction.Classify(cause)
	status := shopaction.Review
	if failure.Retryable && c.row.Attempts < 16 && (c.row.CompensationReason != "" || time.Now().Before(c.row.DeadlineAt)) {
		status = shopaction.Pending
		if c.row.CompensationReason != "" {
			status = shopaction.Compensating
		}
	}
	delay := min(20*time.Second*time.Duration(1<<min(c.row.Attempts, 7)), time.Hour)
	delay += time.Duration(rand.Int64N(int64(delay/4) + 1))
	if failure.RetryAfter > delay {
		delay = failure.RetryAfter
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	client := tx.Client()
	if _, err = lockUnifiedRefundOrder(ctx, client, c.row.OrderID); err != nil {
		return err
	}
	f, err := lockShopFulfillment(ctx, client, c.row.OrderID)
	if err != nil {
		return err
	}
	row, err := lockShopOrderAction(ctx, client, c.row.OrderID)
	if err != nil {
		return err
	}
	if !ownedShopAction(row, c) {
		return errShopActionLeaseLost
	}
	if err = client.ShopOrderAction.UpdateOne(row).SetStatus(status).SetErrorKind(failure.Kind).SetNextAttemptAt(time.Now().Add(delay)).ClearLeaseUntil().Exec(ctx); err != nil {
		return err
	}
	if status == shopaction.Review {
		f, err = client.ShopFulfillment.UpdateOne(f).SetStatusNote("自动交付需要客服复核，请勿重复购买。").AddRevision(1).Save(ctx)
		if err != nil {
			return err
		}
		if err = queueShopNotification(ctx, client, f, "action_review", "feishu"); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return cause
}

func (s *ShopService) executeShopCompensation(ctx context.Context, c *shopOrderActionClaim, adapter shopaction.Adapter) error {
	revoker, ok := adapter.(shopaction.Revoker)
	if !ok {
		return s.failShopAction(ctx, c, &shopaction.Failure{Kind: "REMOTE_STATE_UNKNOWN"})
	}
	if err := revoker.Revoke(ctx, c.snapshot.Request); err != nil {
		return s.failShopAction(ctx, c, err)
	}
	return s.finishShopRevocation(ctx, c)
}

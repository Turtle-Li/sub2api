package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/ent/shoporderaction"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

const shopActionLease = 90 * time.Second

var errShopActionLeaseLost = errors.New("shop action lease lost")
var errShopActionCompensate = errors.New("shop action compensation persisted")

type shopOrderActionClaim struct {
	row      *dbent.ShopOrderAction
	snapshot shopaction.Snapshot
}

func lockShopOrderAction(ctx context.Context, client *dbent.Client, orderID int64) (*dbent.ShopOrderAction, error) {
	q := client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(orderID))
	if client.Driver().Dialect() == dialect.Postgres {
		q.ForUpdate()
	}
	return q.Only(ctx)
}

func decodeShopAction(row *dbent.ShopOrderAction) (shopaction.Snapshot, error) {
	var snapshot shopaction.Snapshot
	digest := sha256.Sum256([]byte(row.Snapshot))
	if row.SnapshotHash != hex.EncodeToString(digest[:]) || json.Unmarshal([]byte(row.Snapshot), &snapshot) != nil ||
		snapshot.Request.ActionID != row.ActionKey || snapshot.Request.Order.ID != row.OrderID || snapshot.Request.Source != shopDaypassInstance ||
		snapshot.Request.Version != 1 || snapshot.Request.Event != shopaction.Paid || !validateActionSnapshot(snapshot.Binding) {
		return snapshot, &shopaction.Failure{Kind: "SNAPSHOT_INVALID"}
	}
	return snapshot, nil
}

func (s *ShopService) repairShopActionOutbox(ctx context.Context) error {
	rows, err := s.client.ShopFulfillment.Query().Where(shopfulfillment.StatusEQ(ShopQueued), func(selector *sql.Selector) {
		a := sql.Table(shoporderaction.Table)
		missing := sql.Select(a.C(shoporderaction.FieldID)).From(a).Where(sql.ColumnsEQ(a.C(shoporderaction.FieldOrderID), selector.C(shopfulfillment.FieldOrderID)))
		selector.Where(sql.And(sql.Not(sql.Exists(missing)), sqljson.ValueEQ(selector.C(shopfulfillment.FieldProductSnapshot), true, sqljson.Path("automatic_fulfillment"))))
	}).Order(dbent.Asc(shopfulfillment.FieldID)).Limit(16).All(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		err = func() error {
			tx, e := s.client.Tx(ctx)
			if e != nil {
				return e
			}
			defer func() { _ = tx.Rollback() }()
			o, e := lockUnifiedRefundOrder(ctx, tx.Client(), row.OrderID)
			if e != nil {
				return e
			}
			f, e := lockShopFulfillment(ctx, tx.Client(), row.OrderID)
			if e != nil {
				return e
			}
			if !shopPaidActionAllowed(o) || f.Status != ShopQueued {
				return nil
			}
			if e = enqueueShopOrderActionTx(ctx, tx.Client(), o, f); e != nil {
				return e
			}
			return tx.Commit()
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *ShopService) RecoverShopActions(ctx context.Context) error {
	if s.actionsCfg.Enabled {
		if err := s.repairShopActionOutbox(ctx); err != nil {
			return err
		}
	}
	states := []string{shopaction.Compensating}
	if s.actionsCfg.Enabled {
		states = append(states, shopaction.Pending, shopaction.Running)
	}
	now := time.Now()
	rows, err := s.client.ShopOrderAction.Query().Where(shoporderaction.StatusIn(states...), shoporderaction.NextAttemptAtLTE(now),
		shoporderaction.Or(shoporderaction.LeaseUntilIsNil(), shoporderaction.LeaseUntilLTE(now))).
		Order(dbent.Asc(shoporderaction.FieldNextAttemptAt), dbent.Asc(shoporderaction.FieldID)).Limit(3).All(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, row := range rows {
		if ctx.Err() != nil || !runtimegate.SharedWorkAllowed() {
			return context.Canceled
		}
		claim, e := s.claimShopAction(ctx, row.OrderID, time.Now())
		if e == nil && claim != nil {
			e = s.executeShopAction(ctx, claim)
		}
		if e != nil && first == nil {
			first = e
		}
	}
	return first
}

func (s *ShopService) claimShopAction(ctx context.Context, orderID int64, now time.Time) (*shopOrderActionClaim, error) {
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
	f, err := lockShopFulfillment(ctx, client, orderID)
	if err != nil {
		return nil, err
	}
	row, err := lockShopOrderAction(ctx, client, orderID)
	if dbent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.Status != shopaction.Pending && row.Status != shopaction.Running && row.Status != shopaction.Compensating {
		return nil, nil
	}
	if row.NextAttemptAt.After(now) || row.LeaseUntil != nil && row.LeaseUntil.After(now) {
		return nil, nil
	}
	snapshot, decodeErr := decodeShopAction(row)
	if decodeErr != nil {
		if err = client.ShopOrderAction.UpdateOne(row).SetStatus(shopaction.Review).SetErrorKind("SNAPSHOT_INVALID").ClearLeaseUntil().Exec(ctx); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	state, reason := shopaction.Running, row.CompensationReason
	if row.Status == shopaction.Compensating || reason != "" {
		state = shopaction.Compensating
	} else if f.Status == ShopCompleted || f.Status == ShopClosed {
		if err = client.ShopOrderAction.UpdateOne(row).SetStatus(shopaction.Cancelled).SetErrorKind("FULFILLMENT_CLOSED").ClearLeaseUntil().Exec(ctx); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	} else {
		fence := ensureShopFinancialFence(ctx, client, o.ID)
		if fence != nil && infraerrors.Reason(fence) != "SHOP_FINANCIAL_REVIEW" {
			return nil, fence
		}
		if !shopPaidActionAllowed(o) || fence != nil || o.PaidAt == nil || !o.PaidAt.Equal(snapshot.Request.Order.PaidAt) {
			state, reason = shopaction.Compensating, "FINANCIAL_FENCE"
		} else if !now.Before(row.DeadlineAt) {
			state, reason = shopaction.Compensating, "DELIVERY_EXPIRED"
		}
	}
	if state == shopaction.Compensating && snapshot.Binding.Adapter != shopaction.Native {
		end := shopaction.Review
		if row.Attempts == 0 {
			end = shopaction.Cancelled
		}
		if err = client.ShopOrderAction.UpdateOne(row).SetStatus(end).SetErrorKind(reason).SetCompensationReason(reason).ClearLeaseUntil().Exec(ctx); err != nil {
			return nil, err
		}
		if err = client.ShopFulfillment.UpdateOne(f).SetStatus(ShopClosed).SetStatusNote("自动交付已停止，请联系客服核对售后。").AddRevision(1).Exec(ctx); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	row, err = client.ShopOrderAction.UpdateOne(row).Where(shoporderaction.ClaimRevisionEQ(row.ClaimRevision)).SetStatus(state).
		SetCompensationReason(reason).AddClaimRevision(1).AddAttempts(1).SetLeaseUntil(now.Add(shopActionLease)).Save(ctx)
	if err != nil {
		return nil, err
	}
	if f.Status == ShopQueued {
		if err = client.ShopFulfillment.UpdateOne(f).SetStatus(ShopProcessing).AddRevision(1).Exec(ctx); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &shopOrderActionClaim{row: row, snapshot: snapshot}, nil
}

func ownedShopAction(row *dbent.ShopOrderAction, c *shopOrderActionClaim) bool {
	return row.ClaimRevision == c.row.ClaimRevision && (row.Status == shopaction.Running || row.Status == shopaction.Compensating) && row.LeaseUntil != nil && row.LeaseUntil.After(time.Now())
}

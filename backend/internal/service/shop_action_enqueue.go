package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/shoporderaction"
	"github.com/Wei-Shaw/sub2api/internal/shopaction"
)

// Called only with the payment and fulfillment rows locked, in their transaction.
// Business-invalid snapshots go to review; only database failures abort payment.
func enqueueShopOrderActionTx(ctx context.Context, client *dbent.Client, o *dbent.PaymentOrder, f *dbent.ShopFulfillment) error {
	p, snapshotErr := shopSnapshot(f)
	if snapshotErr == nil && !shopAutomaticFulfillment(p) {
		return nil
	}
	exists, err := client.ShopOrderAction.Query().Where(shoporderaction.OrderIDEQ(o.ID)).Exist(ctx)
	if err != nil || exists {
		return err
	}
	identity := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d\nprimary-delivery", shopDaypassInstance, shopaction.Paid, o.ID)))
	key := "act_" + hex.EncodeToString(identity[:20])
	paid := time.Now().UTC()
	state, kind := shopaction.Pending, ""
	if o.PaidAt != nil {
		paid = o.PaidAt.UTC()
	} else {
		state, kind = shopaction.Review, "PAID_TIME_INVALID"
	}
	snapshot := shopaction.Snapshot{Request: shopaction.Request{Version: 1, Event: shopaction.Paid, ActionID: key, Source: shopDaypassInstance,
		Order: shopaction.Order{ID: o.ID, ProductID: f.ProductID, AmountFen: int64(math.Round(o.PayAmount * 100)), Currency: "CNY", PaidAt: paid}}}
	if snapshotErr != nil || len(p.PaymentActions) != 1 || p.FulfillmentMode != ShopAccountDelivery {
		state, kind = shopaction.Review, "SNAPSHOT_INVALID"
	} else {
		snapshot.Binding = p.PaymentActions[0]
		if !validateActionSnapshot(snapshot.Binding) || snapshot.Binding.Adapter == shopaction.Native && (!validShopDaypassProduct(p) || snapshot.Request.Order.AmountFen != 600) {
			state, kind = shopaction.Review, "ACTION_INVALID"
			snapshot.Binding.Params = json.RawMessage(`{}`)
		}
		snapshot.Request.Params = snapshot.Binding.Params
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		state, kind = shopaction.Review, "ACTION_INVALID"
		snapshot.Binding = shopaction.Binding{}
		snapshot.Request.Params = nil
		raw, _ = json.Marshal(snapshot)
	}
	digest := sha256.Sum256(raw)
	if err = client.ShopOrderAction.Create().SetOrderID(o.ID).SetActionKey(key).SetSnapshot(string(raw)).SetSnapshotHash(hex.EncodeToString(digest[:])).
		SetDeadlineAt(paid.Add(24 * time.Hour)).SetStatus(state).SetErrorKind(kind).Exec(ctx); err != nil {
		return err
	}
	if state == shopaction.Review {
		return queueShopNotification(ctx, client, f, "action_review", "feishu")
	}
	return nil
}

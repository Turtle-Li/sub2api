package service

import (
	"context"
	"encoding/json"
	"fmt"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"strings"
	"time"
	"unicode/utf8"
)

type shopSecretEnvelope struct {
	OrderID int64  `json:"order_id"`
	Purpose string `json:"purpose"`
	Version int    `json:"version"`
	Content string `json:"content"`
}

func (s *ShopService) encrypt(orderID int64, purpose string, version int, content string) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	b, err := json.Marshal(shopSecretEnvelope{orderID, purpose, version, content})
	if err != nil {
		return "", fmt.Errorf("encode shop secret: %w", err)
	}
	value, err := s.encryptor.Encrypt(string(b))
	if err != nil {
		return "", infraerrors.ServiceUnavailable("SHOP_ENCRYPTION_FAILED", "资料加密失败，请稍后重试")
	}
	return value, nil
}

func (s *ShopService) decrypt(orderID int64, purpose string, version int, ciphertext string) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	raw, err := s.encryptor.Decrypt(ciphertext)
	if err != nil {
		return "", infraerrors.ServiceUnavailable("SHOP_DECRYPTION_FAILED", "资料解密失败，请联系客服")
	}
	var env shopSecretEnvelope
	if json.Unmarshal([]byte(raw), &env) != nil || env.OrderID != orderID || env.Purpose != purpose || env.Version != version {
		return "", infraerrors.Conflict("SHOP_SECRET_INVALID", "订单资料校验失败，请联系客服")
	}
	return env.Content, nil
}

func validateShopInput(content, format string) (string, error) {
	if len(content) > 128*1024 || strings.TrimSpace(content) == "" || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return "", infraerrors.BadRequest("SHOP_INPUT_INVALID", "请粘贴完整资料，大小不能超过128KB")
	}
	if format == "text" {
		return "", nil
	}
	check, err := inspectShopSession(content, time.Now())
	if err != nil {
		return "", err
	}
	return check.Account, nil
}

func (s *ShopService) SubmitInput(ctx context.Context, orderID, userID int64, revision int, content string) (*ShopOrderDetail, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin shop input: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	o, err := lockUnifiedRefundOrder(ctx, client, orderID)
	if err != nil {
		return nil, err
	}
	if o.UserID != userID || o.OrderType != payment.OrderTypeManualShop {
		return nil, infraerrors.NotFound("SHOP_ORDER_NOT_FOUND", "商城订单不存在")
	}
	f, err := lockShopFulfillment(ctx, client, orderID)
	if err != nil {
		return nil, err
	}
	if f.Revision != revision {
		return nil, shopConflict()
	}
	p, err := shopSnapshot(f)
	if err != nil {
		return nil, err
	}
	if !shopPaidActionAllowed(o) || p.FulfillmentMode != ShopSessionTopup || (f.Status != ShopWaitingInput && f.Status != ShopNeedsInput && f.Status != ShopQueued) {
		return nil, infraerrors.Conflict("SHOP_INPUT_NOT_ALLOWED", "当前订单不可修改资料")
	}
	if err := ensureShopFinancialFence(ctx, client, o.ID); err != nil {
		return nil, err
	}
	account, err := validateShopInput(content, p.InputFormat)
	if err != nil {
		return nil, err
	}
	ciphertext, err := s.encrypt(o.ID, "input", f.InputRevision+1, content)
	if err != nil {
		return nil, err
	}
	f, err = client.ShopFulfillment.UpdateOne(f).Where(shopfulfillment.RevisionEQ(revision)).SetInputCiphertext(ciphertext).SetInputAccount(account).SetInputSubmittedAt(time.Now()).SetStatus(ShopQueued).SetStatusNote("").AddRevision(1).AddInputRevision(1).Save(ctx)
	if dbent.IsNotFound(err) {
		return nil, shopConflict()
	}
	if err != nil {
		return nil, fmt.Errorf("save shop input: %w", err)
	}
	if err = queueShopNotification(ctx, client, f, "input_received", "feishu"); err != nil {
		return nil, fmt.Errorf("queue shop input notice: %w", err)
	}
	if err = writePaymentAuditTx(ctx, client, o.ID, fmt.Sprintf("SHOP_INPUT_SUBMITTED_%d", f.InputRevision), fmt.Sprintf("user:%d", userID), map[string]any{"input_revision": f.InputRevision}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit shop input: %w", err)
	}
	return s.Order(ctx, orderID, userID, false)
}

func (s *ShopService) RevealInput(ctx context.Context, orderID, adminID int64, revision int) (string, int, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	o, err := lockUnifiedRefundOrder(ctx, client, orderID)
	if err != nil {
		return "", 0, err
	}
	f, err := lockShopFulfillment(ctx, client, orderID)
	if err != nil {
		return "", 0, err
	}
	if f.Revision != revision {
		return "", 0, shopConflict()
	}
	if !shopPaidActionAllowed(o) || (f.Status != ShopQueued && f.Status != ShopProcessing) || f.InputCiphertext == "" {
		return "", 0, infraerrors.Conflict("SHOP_INPUT_UNAVAILABLE", "当前订单没有可读取的资料")
	}
	if err := ensureShopFinancialFence(ctx, client, o.ID); err != nil {
		return "", 0, err
	}
	content, err := s.decrypt(o.ID, "input", f.InputRevision, f.InputCiphertext)
	if err != nil {
		return "", 0, err
	}
	if err = writePaymentAuditTx(ctx, client, o.ID, fmt.Sprintf("SHOP_INPUT_REVEALED_%d", f.InputRevision), fmt.Sprintf("admin:%d", adminID), map[string]any{"input_revision": f.InputRevision}); err != nil {
		return "", 0, err
	}
	if err = tx.Commit(); err != nil {
		return "", 0, err
	}
	return content, f.InputRevision, nil
}

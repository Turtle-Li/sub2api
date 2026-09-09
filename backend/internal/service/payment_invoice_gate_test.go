//go:build unit

package service

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/runtimegate"
	"github.com/stretchr/testify/require"
)

type invoiceGateTestSender struct{ calls int }

type invoiceSMTPHandoffRepo struct {
	*notificationEmailMemorySettingRepo
	handoff func()
}

func (r *invoiceSMTPHandoffRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	values, err := r.notificationEmailMemorySettingRepo.GetMultiple(ctx, keys)
	for _, key := range keys {
		if key == SettingKeySMTPHost {
			r.handoff()
			break
		}
	}
	return values, err
}

func TestInvoiceEmailGateAfterSMTPConfigRead(t *testing.T) {
	for _, status := range []string{InvoiceStatusIssued, InvoiceStatusRejected} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			stateFile := filepath.Join(t.TempDir(), "background-state")
			require.NoError(t, os.WriteFile(stateFile, []byte("active"), 0600))
			t.Setenv(runtimegate.StateFileEnv, stateFile)
			listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
			require.NoError(t, err)
			defer listener.Close()
			// A listener without a SMTP greeting exposes an accidental transport
			// start as a timeout and a queued connection, without sending mail.
			handoffs := 0
			repo := &invoiceSMTPHandoffRepo{notificationEmailMemorySettingRepo: newNotificationEmailMemorySettingRepo(), handoff: func() {
				handoffs++
				require.NoError(t, os.WriteFile(stateFile, []byte("standby"), 0600))
			}}
			require.NoError(t, repo.SetMultiple(ctx, map[string]string{
				SettingKeySMTPHost: "127.0.0.1", SettingKeySMTPPort: strconv.Itoa(listener.Addr().(*net.TCPAddr).Port),
				SettingKeySMTPFrom: "invoice@example.test",
			}))
			svc, client := newInvoiceUnitService(t, ctx)
			svc.notificationEmailService = NewNotificationEmailService(repo, NewEmailService(repo, nil))
			owner := createInvoiceUnitUser(t, ctx, client, "email-gate@example.test")
			order := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusCompleted, true, true)
			invoice, err := client.PaymentInvoiceRequest.Create().SetOrderID(order.ID).SetUserID(owner.ID).SetTitleType(InvoiceTitleTypePersonal).SetTitle("Email gate test").SetRecipientEmail(owner.Email).SetAmount(order.PayAmount).SetCurrency("CNY").SetStatus(status).SetEmailDeliveryStatus(InvoiceEmailDeliveryPending).Save(ctx)
			require.NoError(t, err)
			if status == InvoiceStatusIssued {
				_, err = client.PaymentInvoiceDocument.Create().SetInvoiceRequestID(invoice.ID).SetFilename("invoice.pdf").SetSizeBytes(8).SetSha256("test").SetData([]byte("%PDF-1.4")).Save(ctx)
				require.NoError(t, err)
			}
			attempted, err := svc.deliverInvoiceEmail(ctx, invoice.ID)
			require.NoError(t, err)
			require.True(t, attempted)
			require.Equal(t, 1, handoffs, "must exercise the final SMTP configuration read")
			require.NoError(t, listener.SetDeadline(time.Now().Add(20*time.Millisecond)))
			conn, acceptErr := listener.Accept()
			if conn != nil {
				conn.Close()
			}
			require.Error(t, acceptErr, "standby must not initiate a SMTP connection")
			persisted, err := client.PaymentInvoiceRequest.Get(ctx, invoice.ID)
			require.NoError(t, err)
			require.Equal(t, InvoiceEmailDeliveryFailed, persisted.EmailDeliveryStatus)
			require.Equal(t, 1, persisted.EmailDeliveryAttempts)
			require.NotNil(t, persisted.EmailDeliveryNextAttemptAt)
			require.Nil(t, persisted.EmailDeliveredAt)
		})
	}
}

func (s *invoiceGateTestSender) SendText(context.Context, string) error { s.calls++; return nil }

func TestInvoiceNotificationGateStopsStandbyClaimsAndPreTransportHandoff(t *testing.T) {
	ctx := context.Background()
	stateFile := filepath.Join(t.TempDir(), "background-state")
	require.NoError(t, os.WriteFile(stateFile, []byte("standby"), 0600))
	t.Setenv(runtimegate.StateFileEnv, stateFile)
	svc, client := newInvoiceUnitService(t, ctx)
	owner := createInvoiceUnitUser(t, ctx, client, "gate@example.test")
	order := createInvoiceUnitOrder(t, ctx, client, owner, OrderStatusCompleted, true, true)
	invoice, err := client.PaymentInvoiceRequest.Create().SetOrderID(order.ID).SetUserID(owner.ID).SetTitleType(InvoiceTitleTypePersonal).SetTitle("Gate test").SetRecipientEmail("gate@example.test").SetAmount(order.PayAmount).SetCurrency("CNY").SetStatus(InvoiceStatusIssued).SetEmailDeliveryStatus(InvoiceEmailDeliveryPending).SetFeishuNotificationStatus(InvoiceFeishuNotificationPending).SetFeishuNotificationRevision(1).Save(ctx)
	require.NoError(t, err)
	email, err := svc.claimInvoiceEmailDelivery(ctx, invoice.ID)
	require.NoError(t, err)
	require.Nil(t, email)
	feishu, err := svc.claimInvoiceFeishuDelivery(ctx, invoice.ID)
	require.NoError(t, err)
	require.Nil(t, feishu)
	count, err := svc.RecoverInvoiceNotifications(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
	svc.dispatchInvoiceEmailDelivery(invoice.ID)
	svc.dispatchInvoiceFeishuDelivery(invoice.ID)
	persisted, err := client.PaymentInvoiceRequest.Get(ctx, invoice.ID)
	require.NoError(t, err)
	require.Zero(t, persisted.EmailDeliveryAttempts)
	require.Zero(t, persisted.FeishuNotificationAttempts)
	require.NoError(t, os.WriteFile(stateFile, []byte("active"), 0600))
	sender := &invoiceGateTestSender{}
	svc.invoiceFeishuSender = sender
	client.PaymentInvoiceRequest.Use(func(next dbent.Mutator) dbent.Mutator {
		return dbent.MutateFunc(func(ctx context.Context, m dbent.Mutation) (dbent.Value, error) {
			value, err := next.Mutate(ctx, m)
			if status, ok := m.Field("feishu_notification_status"); err == nil && ok && status == InvoiceFeishuNotificationSending {
				require.NoError(t, os.WriteFile(stateFile, []byte("standby"), 0600))
			}
			return value, err
		})
	})
	_, err = svc.deliverInvoiceFeishu(ctx, invoice.ID)
	require.NoError(t, err)
	require.Zero(t, sender.calls, "a generation handed to standby after claiming must not start transport")
	persisted, err = client.PaymentInvoiceRequest.Get(ctx, invoice.ID)
	require.NoError(t, err)
	require.Equal(t, InvoiceFeishuNotificationFailed, persisted.FeishuNotificationStatus)
	require.NotNil(t, persisted.FeishuNotificationNextAttemptAt, "unsent notification must remain recoverable")
}

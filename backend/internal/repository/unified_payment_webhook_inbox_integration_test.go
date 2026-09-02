//go:build integration

package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUnifiedWebhookInboxPostgresSequenceAndRetry(t *testing.T) {
	ctx := context.Background()
	store := &unifiedPaymentWebhookInbox{db: integrationDB}
	record := unifiedInboxIntegrationRecord(
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222221",
		"sub2_integration_sequence_1",
		1,
	)
	cleanupUnifiedInboxIntegrationRows(t, record.PaymentOrderID)

	claim, err := store.Claim(ctx, record, time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.UnifiedWebhookClaimNew, claim)

	claim, err = store.Claim(ctx, record, time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.UnifiedWebhookClaimBusy, claim)

	require.NoError(t, store.MarkRetryableFailure(ctx, record.EventID, "fulfillment_retry"))
	claim, err = store.Claim(ctx, record, time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.UnifiedWebhookClaimNew, claim)
	require.NoError(t, store.MarkProcessed(ctx, record.EventID))

	claim, err = store.Claim(ctx, record, time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.UnifiedWebhookClaimDuplicate, claim)

	newer := record
	newer.EventID = "22222222-2222-4222-8222-222222222223"
	newer.Sequence = 3
	newer.BodySHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	claim, err = store.Claim(ctx, newer, time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.UnifiedWebhookClaimNew, claim)
	require.NoError(t, store.MarkProcessed(ctx, newer.EventID))

	obsolete := record
	obsolete.EventID = "22222222-2222-4222-8222-222222222222"
	obsolete.Sequence = 2
	obsolete.BodySHA256 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	claim, err = store.Claim(ctx, obsolete, time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.UnifiedWebhookClaimDuplicate, claim)

	var maxSequence int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT max_processed_sequence
		FROM unified_payment_webhook_cursor
		WHERE payment_order_id = $1::uuid
	`, record.PaymentOrderID).Scan(&maxSequence))
	require.Equal(t, int64(3), maxSequence)

	var status, errorCode string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT status, COALESCE(last_error_code, '')
		FROM unified_payment_webhook_inbox
		WHERE event_id = $1::uuid
	`, obsolete.EventID).Scan(&status, &errorCode))
	require.Equal(t, "PROCESSED", status)
	require.Equal(t, "obsolete_sequence", errorCode)
}

func TestUnifiedWebhookInboxPostgresConcurrentClaimHasSingleOwner(t *testing.T) {
	ctx := context.Background()
	store := &unifiedPaymentWebhookInbox{db: integrationDB}
	record := unifiedInboxIntegrationRecord(
		"33333333-3333-4333-8333-333333333333",
		"44444444-4444-4444-8444-444444444444",
		"sub2_integration_concurrent_1",
		1,
	)
	cleanupUnifiedInboxIntegrationRows(t, record.PaymentOrderID)

	const workers = 8
	start := make(chan struct{})
	claims := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claim, err := store.Claim(ctx, record, time.Minute)
			claims <- claim
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(claims)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	counts := make(map[string]int)
	for claim := range claims {
		counts[claim]++
	}
	require.Equal(t, 1, counts[service.UnifiedWebhookClaimNew])
	require.Equal(t, workers-1, counts[service.UnifiedWebhookClaimBusy])
}

func unifiedInboxIntegrationRecord(paymentOrderID, eventID, productOrderNo string, sequence int64) service.UnifiedWebhookInboxRecord {
	return service.UnifiedWebhookInboxRecord{
		EventID:        eventID,
		PaymentOrderID: paymentOrderID,
		ProductOrderNo: productOrderNo,
		Sequence:       sequence,
		EventType:      "payment.order.paid",
		BodySHA256:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		OccurredAt:     time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
	}
}

func cleanupUnifiedInboxIntegrationRows(t *testing.T, paymentOrderID string) {
	t.Helper()
	cleanup := func() {
		ctx := context.Background()
		_, err := integrationDB.ExecContext(ctx, `
			DELETE FROM unified_payment_webhook_inbox
			WHERE payment_order_id = $1::uuid
		`, paymentOrderID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `
			DELETE FROM unified_payment_webhook_cursor
			WHERE payment_order_id = $1::uuid
		`, paymentOrderID)
		require.NoError(t, err)
	}
	cleanup()
	t.Cleanup(cleanup)
}

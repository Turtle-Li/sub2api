//go:build unit

package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const unifiedInboxTestStaleAfter = time.Minute

func TestUnifiedWebhookInboxClaimNewAndDuplicate(t *testing.T) {
	record := unifiedInboxTestRecord()

	t.Run("new", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()
		mock.ExpectBegin()
		expectUnifiedInboxCursor(mock, record, 0, "", 0, false)
		expectUnifiedInboxLookupEmpty(mock, record)
		mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO unified_payment_webhook_inbox")).
			WithArgs(record.EventID, record.PaymentOrderID, record.ProductOrderNo, record.Sequence,
				record.EventType, record.BodySHA256, "PROCESSING", "", record.OccurredAt).
			WillReturnRows(sqlmock.NewRows([]string{"event_id"}).AddRow(record.EventID))
		mock.ExpectExec(regexp.QuoteMeta("UPDATE unified_payment_webhook_cursor")).
			WithArgs(record.PaymentOrderID, record.EventID, record.Sequence).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		store := &unifiedPaymentWebhookInbox{db: db}
		claim, err := store.Claim(context.Background(), record, unifiedInboxTestStaleAfter)
		require.NoError(t, err)
		require.Equal(t, service.UnifiedWebhookClaimNew, claim)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("processed duplicate", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()
		mock.ExpectBegin()
		expectUnifiedInboxCursor(mock, record, record.Sequence, "", 0, false)
		mock.ExpectQuery("FROM unified_payment_webhook_inbox").
			WithArgs(record.EventID).
			WillReturnRows(unifiedInboxRows(record, "PROCESSED"))
		mock.ExpectCommit()
		store := &unifiedPaymentWebhookInbox{db: db}
		claim, err := store.Claim(context.Background(), record, unifiedInboxTestStaleAfter)
		require.NoError(t, err)
		require.Equal(t, service.UnifiedWebhookClaimDuplicate, claim)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestUnifiedWebhookInboxRejectsSequenceCollision(t *testing.T) {
	record := unifiedInboxTestRecord()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	expectUnifiedInboxCursor(mock, record, 0, "", 0, false)
	mock.ExpectQuery("WHERE event_id = \\$1::uuid").WithArgs(record.EventID).
		WillReturnRows(unifiedInboxEmptyRows())
	conflicting := record
	conflicting.EventID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	mock.ExpectQuery("WHERE payment_order_id = \\$1::uuid AND sequence = \\$2").
		WithArgs(record.PaymentOrderID, record.Sequence).
		WillReturnRows(unifiedInboxRows(conflicting, "PROCESSED"))
	mock.ExpectCommit()
	store := &unifiedPaymentWebhookInbox{db: db}
	claim, err := store.Claim(context.Background(), record, unifiedInboxTestStaleAfter)
	require.NoError(t, err)
	require.Equal(t, service.UnifiedWebhookClaimConflict, claim)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUnifiedWebhookInboxIgnoresObsoleteSequence(t *testing.T) {
	record := unifiedInboxTestRecord()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	expectUnifiedInboxCursor(mock, record, record.Sequence+1, "", 0, false)
	expectUnifiedInboxLookupEmpty(mock, record)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO unified_payment_webhook_inbox")).
		WithArgs(record.EventID, record.PaymentOrderID, record.ProductOrderNo, record.Sequence,
			record.EventType, record.BodySHA256, "PROCESSED", "obsolete_sequence", record.OccurredAt).
		WillReturnRows(sqlmock.NewRows([]string{"event_id"}).AddRow(record.EventID))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE unified_payment_webhook_cursor")).
		WithArgs(record.PaymentOrderID, record.EventID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	store := &unifiedPaymentWebhookInbox{db: db}
	claim, err := store.Claim(context.Background(), record, unifiedInboxTestStaleAfter)
	require.NoError(t, err)
	require.Equal(t, service.UnifiedWebhookClaimDuplicate, claim)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUnifiedWebhookInboxSerializesEventsPerPaymentOrder(t *testing.T) {
	record := unifiedInboxTestRecord()
	activeEventID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	expectUnifiedInboxCursor(mock, record, 0, activeEventID, record.Sequence-1, true)
	expectUnifiedInboxLookupEmpty(mock, record)
	mock.ExpectCommit()
	store := &unifiedPaymentWebhookInbox{db: db}
	claim, err := store.Claim(context.Background(), record, unifiedInboxTestStaleAfter)
	require.NoError(t, err)
	require.Equal(t, service.UnifiedWebhookClaimBusy, claim)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUnifiedWebhookInboxMarkProcessedAdvancesCursor(t *testing.T) {
	record := unifiedInboxTestRecord()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT payment_order_id::text").WithArgs(record.EventID).
		WillReturnRows(sqlmock.NewRows([]string{"payment_order_id"}).AddRow(record.PaymentOrderID))
	mock.ExpectQuery("FROM unified_payment_webhook_cursor").WithArgs(record.PaymentOrderID).
		WillReturnRows(sqlmock.NewRows([]string{"active_event_id"}).AddRow(record.EventID))
	mock.ExpectQuery("FROM unified_payment_webhook_inbox").WithArgs(record.EventID).
		WillReturnRows(unifiedInboxRows(record, "PROCESSING"))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE unified_payment_webhook_inbox")).
		WithArgs(record.EventID, "PROCESSED", "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE unified_payment_webhook_cursor")).
		WithArgs(record.PaymentOrderID, record.Sequence).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	store := &unifiedPaymentWebhookInbox{db: db}
	require.NoError(t, store.MarkProcessed(context.Background(), record.EventID))
	require.NoError(t, mock.ExpectationsWereMet())
}

func expectUnifiedInboxCursor(mock sqlmock.Sqlmock, record service.UnifiedWebhookInboxRecord, maxSequence int64, activeEventID string, activeSequence int64, activeFresh bool) {
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO unified_payment_webhook_cursor")).
		WithArgs(record.PaymentOrderID, record.ProductOrderNo).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT product_order_no, max_processed_sequence").
		WithArgs(record.PaymentOrderID, int64(unifiedInboxTestStaleAfter/time.Millisecond)).
		WillReturnRows(sqlmock.NewRows([]string{
			"product_order_no", "max_processed_sequence", "active_event_id", "active_sequence", "active_fresh",
		}).AddRow(record.ProductOrderNo, maxSequence, activeEventID, activeSequence, activeFresh))
}

func expectUnifiedInboxLookupEmpty(mock sqlmock.Sqlmock, record service.UnifiedWebhookInboxRecord) {
	mock.ExpectQuery("WHERE event_id = \\$1::uuid").WithArgs(record.EventID).
		WillReturnRows(unifiedInboxEmptyRows())
	mock.ExpectQuery("WHERE payment_order_id = \\$1::uuid AND sequence = \\$2").
		WithArgs(record.PaymentOrderID, record.Sequence).
		WillReturnRows(unifiedInboxEmptyRows())
}

func unifiedInboxTestRecord() service.UnifiedWebhookInboxRecord {
	return service.UnifiedWebhookInboxRecord{
		EventID:        "cccccccc-dddd-4eee-8fff-000000000001",
		PaymentOrderID: "11111111-2222-4333-8444-555555555555",
		ProductOrderNo: "sub2_20260902abcd1234", Sequence: 7,
		EventType:  "payment.order.paid",
		BodySHA256: "5986b932a3eef58573bbc4a7ef3b60502683bd14dd75567446e5a1024cc90d5f",
		OccurredAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
	}
}

func unifiedInboxRows(record service.UnifiedWebhookInboxRecord, status string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"event_id", "payment_order_id", "product_order_no", "sequence",
		"event_type", "body_sha256", "status", "updated_at",
	}).AddRow(record.EventID, record.PaymentOrderID, record.ProductOrderNo, record.Sequence,
		record.EventType, record.BodySHA256, status, time.Now())
}

func unifiedInboxEmptyRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"event_id", "payment_order_id", "product_order_no", "sequence",
		"event_type", "body_sha256", "status", "updated_at",
	})
}

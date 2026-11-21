//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOperationalAlertsReportsOnlyStableIdentifiersAndRetriesFailedDelivery(t *testing.T) {
	store := &operationalAlertTestStore{}
	sender := &operationalAlertTestSender{err: errors.New("webhook unreachable")}
	alerts := &OperationalAlerts{store: store, sender: sender}
	alert := OperationalAlert{Component: "image_studio", Stage: "final_archive", SubjectID: "imgbatch_test", Code: "IMAGE_UPSCALE_STORE_FAILED"}
	require.Error(t, alerts.Notify(t.Context(), OperationalAlert{Component: "orders", Stage: "failed", SubjectID: "https://example.test/signed?private=token", Code: "STORE_FAILED"}))
	require.Nil(t, store.pending)
	require.NoError(t, alerts.Notify(t.Context(), alert))
	require.Empty(t, sender.messages, "enqueuing never calls the remote sender inline")
	require.NoError(t, alerts.deliverOne(t.Context()))
	require.Equal(t, []bool{false}, store.deliveries)
	require.NotNil(t, store.pending)
	sender.err = nil
	require.NoError(t, alerts.deliverOne(t.Context()))
	require.Equal(t, []bool{false, true}, store.deliveries)
	require.Nil(t, store.pending)
	require.Len(t, sender.messages, 2)
	require.Equal(t, sender.messages[0], sender.messages[1])
	require.Contains(t, sender.messages[1], "imgbatch_test")
	require.Contains(t, sender.messages[1], "IMAGE_UPSCALE_STORE_FAILED")
	require.NotContains(t, sender.messages[1], "webhook unreachable")
}

type operationalAlertTestStore struct {
	pending    *OperationalAlertDelivery
	deliveries []bool
}

func (s *operationalAlertTestStore) EnqueueOperationalAlert(_ context.Context, alert OperationalAlert) error {
	s.pending = &OperationalAlertDelivery{ID: 42, Claim: "fake", Alert: alert}
	return nil
}

func (s *operationalAlertTestStore) ClaimOperationalAlert(context.Context) (*OperationalAlertDelivery, error) {
	return s.pending, nil
}

func (s *operationalAlertTestStore) FinishOperationalAlert(_ context.Context, _ OperationalAlertDelivery, sent bool) error {
	s.deliveries = append(s.deliveries, sent)
	if sent {
		s.pending = nil
	}
	return nil
}

type operationalAlertTestSender struct {
	messages []string
	err      error
}

func (s *operationalAlertTestSender) SendText(_ context.Context, message string) error {
	s.messages = append(s.messages, message)
	return s.err
}

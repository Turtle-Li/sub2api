package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIProxyStreamCircuitThresholdTTLAndSuccessReset(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	circuit := newOpenAIProxyStreamCircuit(openAIProxyStreamCircuitSettings{
		failureThreshold: 2,
		failureWindow:    time.Minute,
		quarantineTTL:    10 * time.Minute,
		maxEntries:       16,
	})

	tripped, _ := circuit.recordFailure(1, base)
	require.False(t, tripped)
	require.False(t, circuit.isBlocked(1, base))
	require.True(t, circuit.recordSuccess(1))

	tripped, _ = circuit.recordFailure(1, base.Add(10*time.Second))
	require.False(t, tripped, "success must clear the previous failure observation")
	tripped, until := circuit.recordFailure(1, base.Add(20*time.Second))
	require.True(t, tripped)
	require.Equal(t, base.Add(20*time.Second+10*time.Minute), until)
	require.True(t, circuit.isBlocked(1, until.Add(-time.Nanosecond)))
	require.False(t, circuit.isBlocked(1, until), "TTL expiry must re-admit the proxy")

	tripped, _ = circuit.recordFailure(2, base)
	require.False(t, tripped)
	tripped, _ = circuit.recordFailure(2, base.Add(2*time.Minute))
	require.False(t, tripped, "failures outside the window must not accumulate")
}

func TestOpenAIProxyStreamCircuitCollapsesBurstFailures(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	circuit := newOpenAIProxyStreamCircuit(openAIProxyStreamCircuitSettings{
		failureThreshold: 2,
		failureWindow:    time.Minute,
		quarantineTTL:    10 * time.Minute,
		collapseInterval: 3 * time.Second,
		maxEntries:       16,
	})

	// One HTTP/2 connection loss kills several multiplexed streams at once:
	// the near-simultaneous reports must count as a single failure event.
	tripped, _ := circuit.recordFailure(1, base)
	require.False(t, tripped)
	tripped, _ = circuit.recordFailure(1, base.Add(time.Second))
	require.False(t, tripped, "burst failures inside the collapse interval must merge")
	tripped, _ = circuit.recordFailure(1, base.Add(2*time.Second))
	require.False(t, tripped, "burst failures inside the collapse interval must merge")
	require.False(t, circuit.isBlocked(1, base.Add(2*time.Second)))

	// A second, distinct incident past the collapse interval still trips.
	tripped, _ = circuit.recordFailure(1, base.Add(5*time.Second))
	require.True(t, tripped)
	require.True(t, circuit.isBlocked(1, base.Add(5*time.Second)))
}

func TestOpenAIProxyStreamCircuitDisabled(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	circuit := newOpenAIProxyStreamCircuit(openAIProxyStreamCircuitSettings{
		disabled:         true,
		failureThreshold: 1,
		failureWindow:    time.Minute,
		quarantineTTL:    10 * time.Minute,
		maxEntries:       16,
	})

	tripped, _ := circuit.recordFailure(1, base)
	require.False(t, tripped)
	require.False(t, circuit.isBlocked(1, base))
	require.Equal(t, 0, circuit.activeBlockCount(base))
}

func TestOpenAIProxyStreamCircuitActiveBlockCount(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	circuit := newOpenAIProxyStreamCircuit(openAIProxyStreamCircuitSettings{
		failureThreshold: 1,
		failureWindow:    time.Minute,
		quarantineTTL:    10 * time.Minute,
		maxEntries:       16,
	})

	require.Equal(t, 0, circuit.activeBlockCount(base))
	tripped, until := circuit.recordFailure(1, base)
	require.True(t, tripped)
	circuit.recordFailure(2, base) // second proxy also tripped (threshold 1)
	require.Equal(t, 2, circuit.activeBlockCount(base.Add(time.Second)))
	require.Equal(t, 0, circuit.activeBlockCount(until), "expired quarantines must not count")
}

func TestOpenAIProxyStreamQuarantineBypassContext(t *testing.T) {
	proxyID := int64(7)
	account := &Account{ID: 1, Platform: PlatformOpenAI, ProxyID: &proxyID}
	svc := &OpenAIGatewayService{}
	svc.openaiProxyStreamCircuit = newOpenAIProxyStreamCircuit(openAIProxyStreamCircuitSettings{
		failureThreshold: 1,
		failureWindow:    time.Minute,
		quarantineTTL:    10 * time.Minute,
		maxEntries:       16,
	})
	svc.openaiProxyStreamCircuit.recordFailure(proxyID, time.Now())

	ctx := context.Background()
	require.True(t, svc.isOpenAIProxyStreamQuarantined(ctx, account))
	require.False(t, svc.isOpenAIProxyStreamQuarantined(withOpenAIProxyStreamQuarantineBypass(ctx), account))
}

func TestOpenAIProxyCapacityShedQuarantinesRouteAfterRepeatedFailures(t *testing.T) {
	proxyID := int64(12)
	account := &Account{ID: 1201, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &proxyID}
	svc := &OpenAIGatewayService{openaiCapacityShed: newOpenAICapacityShedState(8)}
	svc.openaiProxyStreamCircuit = newOpenAIProxyStreamCircuit(openAIProxyStreamCircuitSettings{
		failureThreshold: 2,
		failureWindow:    time.Minute,
		quarantineTTL:    10 * time.Minute,
		maxEntries:       16,
	})
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}}`)

	svc.RecordOpenAICapacityShedTerminalFailure(context.Background(), account, payload, "Our servers are currently overloaded. Please try again later.")
	require.False(t, svc.isOpenAIProxyStreamQuarantined(context.Background(), account), "one capacity failure must not quarantine a route")
	svc.RecordOpenAICapacityShedTerminalFailure(context.Background(), account, payload, "Our servers are currently overloaded. Please try again later.")
	require.True(t, svc.isOpenAIProxyStreamQuarantined(context.Background(), account))

	otherAccount := &Account{ID: 1202, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &proxyID}
	scheduler := &defaultOpenAIAccountScheduler{service: svc}
	compatible, reason := scheduler.isAccountRequestCompatibleReason(context.Background(), otherAccount, OpenAIAccountScheduleRequest{})
	require.False(t, compatible, "accounts sharing the quarantined route must be deferred")
	require.Equal(t, "proxy_stream_quarantined", reason)
}

func TestOpenAIProxyCapacityShedSuccessResetsObservation(t *testing.T) {
	proxyID := int64(13)
	account := &Account{ID: 1301, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &proxyID}
	svc := &OpenAIGatewayService{openaiCapacityShed: newOpenAICapacityShedState(8)}
	svc.openaiProxyStreamCircuit = newOpenAIProxyStreamCircuit(openAIProxyStreamCircuitSettings{
		failureThreshold: 2,
		failureWindow:    time.Minute,
		quarantineTTL:    10 * time.Minute,
		maxEntries:       16,
	})
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}}`)

	svc.RecordOpenAICapacityShedTerminalFailure(context.Background(), account, payload, "overloaded")
	svc.clearOpenAIProxyStreamDisconnect(account)
	svc.RecordOpenAICapacityShedTerminalFailure(context.Background(), account, payload, "overloaded")
	require.False(t, svc.isOpenAIProxyStreamQuarantined(context.Background(), account), "a successful stream must reset the route observation")
}

func TestOpenAIProxyCapacityShedIgnoresDirectAccount(t *testing.T) {
	account := &Account{ID: 1401, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	svc := &OpenAIGatewayService{openaiCapacityShed: newOpenAICapacityShedState(8)}
	svc.openaiProxyStreamCircuit = newOpenAIProxyStreamCircuit(openAIProxyStreamCircuitSettings{
		failureThreshold: 1,
		failureWindow:    time.Minute,
		quarantineTTL:    10 * time.Minute,
		maxEntries:       16,
	})
	payload := []byte(`{"type":"response.failed","response":{"error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}}`)

	svc.RecordOpenAICapacityShedTerminalFailure(context.Background(), account, payload, "overloaded")
	require.Equal(t, 0, svc.openaiProxyStreamCircuit.activeBlockCount(time.Now()))
}

func TestOpenAIProxyStreamCircuitBoundsEntries(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	circuit := newOpenAIProxyStreamCircuit(openAIProxyStreamCircuitSettings{
		failureThreshold: 1,
		failureWindow:    time.Minute,
		quarantineTTL:    10 * time.Minute,
		maxEntries:       2,
	})

	circuit.recordFailure(1, base)
	circuit.recordFailure(2, base.Add(time.Second))
	circuit.recordFailure(3, base.Add(2*time.Second))

	circuit.mu.Lock()
	defer circuit.mu.Unlock()
	require.Len(t, circuit.entries, 2)
	_, oldestRetained := circuit.entries[1]
	require.False(t, oldestRetained, "the oldest entry must be evicted at the bound")
}

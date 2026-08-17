package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newDesktopAuthServiceForTest(t *testing.T) (*DesktopAuthService, *miniredis.Miniredis) {
	t.Helper()
	miniRedis := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: miniRedis.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewDesktopAuthService(client), miniRedis
}

func testDesktopAuthVerifier() string {
	return strings.Repeat("a", 43)
}

func startDesktopAuthForTest(t *testing.T, service *DesktopAuthService) DesktopAuthStart {
	t.Helper()
	grant, err := service.Start(context.Background(), pkceChallenge(testDesktopAuthVerifier()))
	require.NoError(t, err)
	return grant
}

func TestDesktopAuthServiceRequiresApprovalAndConsumesOnce(t *testing.T) {
	service, _ := newDesktopAuthServiceForTest(t)
	grant := startDesktopAuthForTest(t, service)

	pending, err := service.Consume(context.Background(), grant.DeviceCode, testDesktopAuthVerifier())
	require.NoError(t, err)
	require.Equal(t, "pending", pending.Status)

	approved, err := service.Approve(context.Background(), grant.UserCode, 42)
	require.NoError(t, err)
	require.Equal(t, "approved", approved.Status)

	authenticated, err := service.Consume(context.Background(), grant.DeviceCode, testDesktopAuthVerifier())
	require.NoError(t, err)
	require.Equal(t, "authenticated", authenticated.Status)
	require.EqualValues(t, 42, authenticated.UserID)

	replayed, err := service.Consume(context.Background(), grant.DeviceCode, testDesktopAuthVerifier())
	require.NoError(t, err)
	require.Equal(t, "denied", replayed.Status)
}

func TestDesktopAuthServiceBindsApprovalAndTokenExchangeToOneUserAndPKCEVerifier(t *testing.T) {
	service, _ := newDesktopAuthServiceForTest(t)
	grant := startDesktopAuthForTest(t, service)

	wrongVerifier, err := service.Consume(context.Background(), grant.DeviceCode, strings.Repeat("b", 43))
	require.NoError(t, err)
	require.Equal(t, "denied", wrongVerifier.Status)

	approved, err := service.Approve(context.Background(), strings.ToLower(grant.UserCode), 7)
	require.NoError(t, err)
	require.Equal(t, "approved", approved.Status)

	idempotent, err := service.Approve(context.Background(), grant.UserCode, 7)
	require.NoError(t, err)
	require.Equal(t, "approved", idempotent.Status)

	_, err = service.Approve(context.Background(), grant.UserCode, 8)
	require.ErrorIs(t, err, ErrDesktopAuthInvalidRequest)

	authenticated, err := service.Consume(context.Background(), grant.DeviceCode, testDesktopAuthVerifier())
	require.NoError(t, err)
	require.Equal(t, "authenticated", authenticated.Status)
	require.EqualValues(t, 7, authenticated.UserID)
}

func TestDesktopAuthServiceExpiresBothBrowserApprovalAndDesktopExchange(t *testing.T) {
	service, miniRedis := newDesktopAuthServiceForTest(t)
	grant := startDesktopAuthForTest(t, service)
	miniRedis.FastForward(desktopAuthTTL + time.Second)

	approval, err := service.Approve(context.Background(), grant.UserCode, 7)
	require.NoError(t, err)
	require.Equal(t, "expired", approval.Status)

	exchange, err := service.Consume(context.Background(), grant.DeviceCode, testDesktopAuthVerifier())
	require.NoError(t, err)
	require.Equal(t, "expired", exchange.Status)
}

func TestDesktopAuthServiceConcurrentExchangeHasExactlyOneWinner(t *testing.T) {
	service, _ := newDesktopAuthServiceForTest(t)
	grant := startDesktopAuthForTest(t, service)
	_, err := service.Approve(context.Background(), grant.UserCode, 99)
	require.NoError(t, err)

	const callers = 8
	results := make(chan DesktopAuthResult, callers)
	errors := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, consumeErr := service.Consume(context.Background(), grant.DeviceCode, testDesktopAuthVerifier())
			results <- result
			errors <- consumeErr
		}()
	}
	group.Wait()
	close(results)
	close(errors)

	for consumeErr := range errors {
		require.NoError(t, consumeErr)
	}
	var authenticated int
	var denied int
	for result := range results {
		switch result.Status {
		case "authenticated":
			authenticated++
			require.EqualValues(t, 99, result.UserID)
		case "denied":
			denied++
		default:
			t.Fatalf("unexpected concurrent device authorization status %q", result.Status)
		}
	}
	require.Equal(t, 1, authenticated)
	require.Equal(t, callers-1, denied)
}

func TestDesktopAuthServiceStoresOnlyHashedCodes(t *testing.T) {
	service, _ := newDesktopAuthServiceForTest(t)
	grant := startDesktopAuthForTest(t, service)

	client := service.redis
	keys, err := client.Keys(context.Background(), "desktop-auth:v1:*").Result()
	require.NoError(t, err)
	require.NotEmpty(t, keys)
	for _, key := range keys {
		require.NotContains(t, key, grant.DeviceCode)
		require.NotContains(t, key, strings.ReplaceAll(grant.UserCode, "-", ""))
		kind, err := client.Type(context.Background(), key).Result()
		require.NoError(t, err)
		switch kind {
		case "hash":
			values, hashErr := client.HGetAll(context.Background(), key).Result()
			require.NoError(t, hashErr)
			for _, value := range values {
				require.NotEqual(t, grant.DeviceCode, value)
				require.NotEqual(t, grant.UserCode, value)
			}
		case "string":
			value, getErr := client.Get(context.Background(), key).Result()
			require.NoError(t, getErr)
			require.NotEqual(t, grant.DeviceCode, value)
			require.NotEqual(t, grant.UserCode, value)
		default:
			t.Fatalf("unexpected Redis value type %q", kind)
		}
	}
}

func TestDesktopAuthServiceRejectsMalformedInput(t *testing.T) {
	service, _ := newDesktopAuthServiceForTest(t)

	_, err := service.Start(context.Background(), "not-a-valid-pkce-challenge")
	require.ErrorIs(t, err, ErrDesktopAuthInvalidRequest)

	_, err = service.Approve(context.Background(), "invalid-code", 1)
	require.ErrorIs(t, err, ErrDesktopAuthInvalidRequest)
}

//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3CacheUsesRealSharedAccountTraffic(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	makeGroup := func(platform string, private bool) *service.Group {
		return mustCreateGroup(t, client, &service.Group{Name: "cache-" + uuid.NewString(), Platform: platform, IsExclusive: private})
	}
	target := makeGroup(service.PlatformAnthropic, false)
	peer := makeGroup(service.PlatformAnthropic, false)
	private := makeGroup(service.PlatformAnthropic, true)
	otherPlatform := makeGroup(service.PlatformOpenAI, false)
	inactive := makeGroup(service.PlatformAnthropic, false)
	_, err := integrationDB.ExecContext(ctx, `UPDATE groups SET status='disabled' WHERE id=$1`, inactive.ID)
	require.NoError(t, err)
	account := mustCreateAccount(t, client, &service.Account{Name: "cache-" + uuid.NewString(), Platform: service.PlatformAnthropic})
	unrelated := mustCreateAccount(t, client, &service.Account{Name: "cache-other-" + uuid.NewString(), Platform: service.PlatformAnthropic})
	mustBindAccountToGroup(t, client, account.ID, target.ID, 1)
	mustBindAccountToGroup(t, client, account.ID, peer.ID, 1)
	mustBindAccountToGroup(t, client, account.ID, private.ID, 1)
	mustBindAccountToGroup(t, client, account.ID, otherPlatform.ID, 1)
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, GroupID: &peer.ID})
	usage := NewUsageLogRepository(client, integrationDB)
	to := time.Now().UTC().Truncate(time.Minute)
	from := to.Add(-3 * time.Hour)
	add := func(gid, aid int64, model string, kind service.RequestType, at time.Time, input, write, read int) {
		t.Helper()
		images, videos := 0, 0
		if model == "media" {
			images = 1
		} else if model == "video" {
			videos = 1
		}
		created, err := usage.Create(ctx, &service.UsageLog{
			UserID: user.ID, APIKeyID: key.ID, GroupID: &gid, AccountID: aid,
			RequestID: uuid.NewString(), Model: model, RequestedModel: model,
			RequestType: kind, InputTokens: input, CacheCreationTokens: write,
			CacheReadTokens: read, CreatedAt: at,
			ImageCount: images, VideoCount: videos,
			// Zero-price real usage is part of the dashboard too.
			ActualCost: 0,
		})
		require.NoError(t, err)
		require.True(t, created)
	}
	for i := 0; i < 20; i++ {
		model := "claude-opus"
		if i%2 == 0 {
			model = "claude-sonnet"
		}
		add(peer.ID, account.ID, model, service.RequestTypeStream, from, 2, 2, 96)
	}
	// Subscription traffic shares the pool, but never publishes a private card.
	for i := 0; i < 10; i++ {
		add(private.ID, account.ID, "claude-subscription", service.RequestTypeStream, from, 1, 1, 98)
	}
	// Every excluded class would change the result if its boundary regressed.
	for _, gid := range []int64{otherPlatform.ID, inactive.ID} {
		add(gid, account.ID, "not-public-pool", service.RequestTypeStream, from, 10000, 0, 0)
	}
	add(peer.ID, unrelated.ID, "unrelated-account", service.RequestTypeStream, from, 10000, 0, 0)
	add(peer.ID, account.ID, "too-old", service.RequestTypeStream, from.Add(-time.Second), 10000, 0, 0)
	add(peer.ID, account.ID, "outside-end", service.RequestTypeStream, to, 10000, 0, 0)
	add(peer.ID, account.ID, "policy-blocked", service.RequestTypeCyberBlocked, from, 10000, 0, 0)
	add(peer.ID, account.ID, "media", service.RequestTypeStream, from, 10000, 0, 0)
	add(peer.ID, account.ID, "video", service.RequestTypeStream, from, 10000, 0, 0)
	add(peer.ID, account.ID, "empty-usage", service.RequestTypeStream, from, 0, 0, 0)
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO channel_monitor_v3_group_probes
		(group_id,model,slot_start,finished_at,outcome,input_tokens,output_tokens,attempt_count)
		VALUES ($1,'cheap-haiku',$2,$2,'success',100000,1,1)`, target.ID, from)
	require.NoError(t, err)
	repo := NewChannelMonitorV3Repository(integrationDB)
	facts, err := repo.GroupRequestCache(ctx, []int64{target.ID, private.ID, inactive.ID}, from, to)
	require.NoError(t, err)
	require.Equal(t, service.ChannelMonitorV3RequestCache{Requests: 30, InputTokens: 50, CacheCreationTokens: 50, CacheReadTokens: 2900}, facts[target.ID])
	require.NotContains(t, facts, private.ID)
	require.NotContains(t, facts, inactive.ID)
	// Removing the live binding removes the scope immediately, without cached data.
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM account_groups WHERE group_id=$1`, target.ID)
	require.NoError(t, err)
	facts, err = repo.GroupRequestCache(ctx, []int64{target.ID}, from, to)
	require.NoError(t, err)
	require.Empty(t, facts)
}

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImageStudioKeyDirectorySeparatesGenerationAndOwnedHistory(t *testing.T) {
	// Given live, historical and unrelated owned keys, plus another user's key.
	ctx := context.Background()
	repo, client := newAPIKeyRepoSQLite(t)
	owner := mustCreateAPIKeyRepoUser(t, ctx, client, "studio-owner@example.com")
	other := mustCreateAPIKeyRepoUser(t, ctx, client, "studio-other@example.com")
	group, err := client.Group.Create().SetName("studio-images").SetPlatform(service.PlatformOpenAI).
		SetStatus(service.StatusActive).SetAllowBatchImageGeneration(true).Save(ctx)
	require.NoError(t, err)
	live, err := client.APIKey.Create().SetUserID(owner.ID).SetKey("secret-live").SetName("live").SetStatus(service.StatusActive).SetGroupID(group.ID).Save(ctx)
	require.NoError(t, err)
	history, err := client.APIKey.Create().SetUserID(owner.ID).SetKey("secret-history").SetName("history").SetStatus(service.StatusActive).Save(ctx)
	require.NoError(t, err)
	unrelated, err := client.APIKey.Create().SetUserID(owner.ID).SetKey("secret-unrelated").SetName("unrelated").SetStatus(service.StatusActive).Save(ctx)
	require.NoError(t, err)
	_, err = client.APIKey.Create().SetUserID(other.ID).SetKey("secret-other").SetName("other").SetStatus(service.StatusActive).SetGroupID(group.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.BatchImageJob.Create().SetBatchID("studio-owned").SetUserID(owner.ID).SetAPIKeyID(history.ID).
		SetProvider("muse_images").SetModel("muse-image").SetItemCount(1).Save(ctx)
	require.NoError(t, err)
	_, err = client.BatchImageJob.Create().SetBatchID("studio-other").SetUserID(other.ID).SetAPIKeyID(unrelated.ID).
		SetProvider("muse_images").SetModel("muse-image").SetItemCount(1).Save(ctx)
	require.NoError(t, err)
	// When the owner reads the credential-free image directory.
	keys, page, err := repo.ListImageStudioKeys(ctx, owner.ID, pagination.PaginationParams{Page: 1, PageSize: 100})
	// Then generation and history are independent, and both sides of history ownership match.
	require.NoError(t, err)
	require.EqualValues(t, 3, page.Total)
	require.Len(t, keys, 3)
	require.Equal(t, live.ID, keys[0].ID)
	require.True(t, keys[0].CanGenerate)
	require.False(t, keys[0].HasHistory)
	require.Equal(t, history.ID, keys[1].ID)
	require.False(t, keys[1].CanGenerate)
	require.True(t, keys[1].HasHistory)
	require.False(t, keys[2].CanGenerate)
	require.False(t, keys[2].HasHistory)
}

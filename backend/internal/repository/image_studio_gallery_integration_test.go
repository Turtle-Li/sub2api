//go:build integration

package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestImageStudioGallerySeparatesRetiredOutputsBeforePaging(t *testing.T) {
	ctx := t.Context()
	tx := testTx(t)
	repo := newBatchImageRepositoryWithSQL(tx)
	studio := &service.BatchImagePublicService{Repo: repo}
	owner := service.BatchImageOwner{UserID: 101, APIKeyID: service.ImageStudioWalletOwnerSlot}
	groupID := int64(45)
	legacyKey := int64(222)
	var deadline time.Time
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT NOW()`).Scan(&deadline))
	future := deadline.Add(time.Hour)
	prompt := "retention fixture"
	root := batchImageTestID(t, "gallery-root")
	job := func(name, status string, expires *time.Time) service.CreateBatchImageJobParams {
		return service.CreateBatchImageJobParams{
			BatchID: batchImageTestID(t, name), UserID: owner.UserID, APIKeyID: &owner.APIKeyID, StudioGroupID: &groupID, ParentBatchID: &root,
			Provider: service.BatchImageProviderOpenAI, Model: "image", ImageSize: "1K", Status: status, OutputExpiresAt: expires,
		}
	}
	add := func(params service.CreateBatchImageJobParams, items []service.CreateBatchImageItemParams) {
		t.Helper()
		params.ItemCount = len(items)
		_, err := repo.CreateBatchImageJob(ctx, params)
		require.NoError(t, err)
		for i := range items {
			items[i].JobID = params.BatchID
			items[i].PromptPreview = &prompt
			if items[i].CustomID == "" {
				items[i].CustomID = fmt.Sprintf("item-%d", i)
			}
		}
		require.NoError(t, repo.BulkCreateBatchImageItems(ctx, items))
	}
	successes := func(count int) []service.CreateBatchImageItemParams {
		items := make([]service.CreateBatchImageItemParams, count)
		for i := range items {
			items[i].Status, items[i].ImageCount = service.BatchImageItemStatusSuccess, 1
		}
		return items
	}
	live := job("live", service.BatchImageJobStatusCompleted, &future)
	live.BatchID, live.ParentBatchID = root, nil
	add(live, successes(49))
	archive := []service.CreateBatchImageItemParams{{Status: service.BatchImageItemStatusResultAvailable}}
	liveArchive := job("live-archive", service.BatchImageJobStatusCompleted, nil)
	add(liveArchive, archive)
	failed := job("failed-delivery", service.BatchImageJobStatusFailed, &deadline)
	add(failed, archive)
	expired := job("elapsed", service.BatchImageJobStatusCompleted, &deadline)
	add(expired, successes(51))
	deleted := job("deleted-status", service.BatchImageJobStatusOutputDeleted, nil)
	add(deleted, append(successes(1), service.CreateBatchImageItemParams{CustomID: "failure", Status: service.BatchImageItemStatusFailed}))
	cleanedArchive := job("cleaned-archive", service.BatchImageJobStatusCompleted, &future)
	add(cleanedArchive, archive)
	_, err := tx.ExecContext(ctx, `UPDATE batch_image_jobs SET output_deleted_at = NOW() WHERE batch_id = $1`, cleanedArchive.BatchID)
	require.NoError(t, err)

	foreign := job("foreign-wallet", service.BatchImageJobStatusCompleted, &deadline)
	foreign.UserID++
	add(foreign, successes(1))
	legacy := job("legacy-key", service.BatchImageJobStatusCompleted, &deadline)
	legacy.APIKeyID, legacy.StudioGroupID = &legacyKey, nil
	add(legacy, successes(1))
	hidden := job("hidden", service.BatchImageJobStatusCompleted, &deadline)
	add(hidden, successes(1))
	require.NoError(t, repo.MarkBatchImageJobUserDeleted(ctx, owner.UserID, owner.APIKeyID, hidden.BatchID, deadline))
	unrelated := job("different-root", service.BatchImageJobStatusCompleted, &deadline)
	unrelated.ParentBatchID = nil
	add(unrelated, successes(1))

	read := func(status string, sizes ...int) []service.BatchImageGalleryEntry {
		t.Helper()
		query := service.BatchImageGalleryRequest{Status: status, Limit: 48, Search: "retention fixture", RootBatchID: root}
		var rows []service.BatchImageGalleryEntry
		seen := make(map[[2]string]bool)
		for index, size := range sizes {
			page, err := studio.Gallery(ctx, owner, query)
			require.NoError(t, err)
			require.Len(t, page.Data, size, "%s page %d", status, index+1)
			for _, entry := range page.Data {
				key := [2]string{entry.Job.ID, entry.Item.CustomID}
				require.False(t, seen[key], "duplicate across pages: %v", key)
				seen[key] = true
			}
			rows = append(rows, page.Data...)
			query.Cursor = page.NextCursor
			if index == len(sizes)-1 {
				require.Empty(t, query.Cursor)
			} else {
				require.NotEmpty(t, query.Cursor)
			}
		}
		return rows
	}
	current := read("current", 48, 4)
	require.Equal(t, "failure", current[0].Item.CustomID, "failed item in a cleaned batch remains visible")
	require.Equal(t, failed.BatchID, current[1].Job.ID)
	require.Equal(t, liveArchive.BatchID, current[2].Job.ID)
	for _, entry := range current[3:] {
		require.Equal(t, root, entry.Job.ID)
	}
	retired := read("expired", 48, 5)
	require.Equal(t, cleanedArchive.BatchID, retired[0].Job.ID, "a deletion timestamp is enough even before the deadline")
	require.Equal(t, deleted.BatchID, retired[1].Job.ID, "the deleted status is enough with no timestamps")
	for _, entry := range retired[2:] {
		require.Equal(t, expired.BatchID, entry.Job.ID, "the deadline itself excludes the image before cleanup")
	}
	read("all", 48, 48, 9)
	read("failed", 2)
	read("completed", 48, 2)

	mismatch, err := studio.Gallery(ctx, owner, service.BatchImageGalleryRequest{Status: "expired", RootBatchID: root, Search: "not a stored prompt"})
	require.NoError(t, err)
	require.Empty(t, mismatch.Data)
	legacyPage, err := studio.Gallery(ctx, service.BatchImageOwner{UserID: owner.UserID, APIKeyID: legacyKey}, service.BatchImageGalleryRequest{Status: "expired", RootBatchID: root})
	require.NoError(t, err)
	require.Len(t, legacyPage.Data, 1)
	require.Equal(t, legacy.BatchID, legacyPage.Data[0].Job.ID)
}

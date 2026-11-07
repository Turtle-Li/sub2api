package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Gallery reads existing job/item metadata only; it never opens provider results.
type BatchImageGalleryRecord struct {
	Job  *BatchImageJob
	Item *BatchImageItem
}

type BatchImageGalleryQuery struct {
	RootBatchID string
	Limit       int
	BeforeJob   int64
	BeforeItem  int64
	Status      string
	Search      string
}

type BatchImageGalleryRepository interface {
	ListBatchImageGalleryForOwner(context.Context, int64, int64, BatchImageGalleryQuery) ([]BatchImageGalleryRecord, error)
}

type BatchImageGalleryEntry struct {
	Job  *BatchImagePublicBatch `json:"job"`
	Item BatchImagePublicItem   `json:"item"`
}

type BatchImageGalleryPage struct {
	Data       []BatchImageGalleryEntry `json:"data"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

type BatchImageGalleryRequest struct {
	Limit       int
	Cursor      string
	Status      string
	Search      string
	RootBatchID string
}

func (s *BatchImagePublicService) Gallery(ctx context.Context, owner BatchImageOwner, request BatchImageGalleryRequest) (*BatchImageGalleryPage, error) {
	repo, ok := s.Repo.(BatchImageGalleryRepository)
	if !ok {
		return nil, ErrBatchImageDisabled
	}
	limit := request.Limit
	if limit <= 0 || limit > 50 {
		limit = 24
	}
	query := BatchImageGalleryQuery{Limit: limit + 1, Status: request.Status, Search: strings.TrimSpace(request.Search), RootBatchID: request.RootBatchID}
	switch query.Status {
	case "", "all", "pending", "completed", "failed":
	default:
		return nil, ErrBatchImageInvalidItems
	}
	if len(query.Search) > 240 || len(query.RootBatchID) > 200 {
		return nil, ErrBatchImageInvalidItems
	}
	if request.Cursor != "" {
		parts := strings.Split(request.Cursor, ":")
		if len(parts) != 2 {
			return nil, ErrBatchImageInvalidItems
		}
		var err error
		query.BeforeJob, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil || query.BeforeJob <= 0 {
			return nil, ErrBatchImageInvalidItems
		}
		query.BeforeItem, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || query.BeforeItem <= 0 {
			return nil, ErrBatchImageInvalidItems
		}
	}
	rows, err := repo.ListBatchImageGalleryForOwner(ctx, owner.UserID, owner.APIKeyID, query)
	if err != nil {
		return nil, err
	}
	page := &BatchImageGalleryPage{Data: make([]BatchImageGalleryEntry, 0, min(len(rows), limit))}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		page.NextCursor = fmt.Sprintf("%d:%d", last.Job.ID, last.Item.ID)
	}
	for _, row := range rows {
		page.Data = append(page.Data, BatchImageGalleryEntry{Job: BatchImageJobToPublic(row.Job), Item: BatchImageItemToPublic(row.Item)})
	}
	return page, nil
}

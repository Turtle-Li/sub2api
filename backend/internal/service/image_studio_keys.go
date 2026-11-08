package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// ImageStudioKey is a credential-free directory projection, not authorization.
// Every actual operation still authenticates the selected key and its owner.
type ImageStudioKey struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	GroupID        *int64     `json:"group_id"`
	GroupName      string     `json:"group_name"`
	ExpiresAt      *time.Time `json:"expires_at"`
	Expired        bool       `json:"expired"`
	QuotaExhausted bool       `json:"quota_exhausted"`
	CanGenerate    bool       `json:"can_generate"`
	HasHistory     bool       `json:"has_history"`
}

type imageStudioKeyRepository interface {
	ListImageStudioKeys(context.Context, int64, pagination.PaginationParams) ([]ImageStudioKey, *pagination.PaginationResult, error)
}

func (s *APIKeyService) ListImageStudioKeys(ctx context.Context, userID int64, params pagination.PaginationParams) ([]ImageStudioKey, *pagination.PaginationResult, error) {
	repo, ok := s.apiKeyRepo.(imageStudioKeyRepository)
	if !ok {
		return nil, nil, fmt.Errorf("image studio key directory is unavailable")
	}
	return repo.ListImageStudioKeys(ctx, userID, params)
}

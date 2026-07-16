package service

import (
	"context"
	"time"
)

const MaxResetCardsPerGrant = 1000

// SubscriptionResetCardBatch is an unexpired grant batch with remaining uses.
// Separate batches allow repeated grants to stack while retaining their own expiry.
type SubscriptionResetCardBatch struct {
	Remaining int
	ExpiresAt time.Time
}

type SubscriptionResetCardSummary struct {
	AvailableCount int
	Batches        []SubscriptionResetCardBatch
}

type GrantSubscriptionResetCardsInput struct {
	GroupIDs  []int64
	Count     int
	ExpiresAt time.Time
	IssuedBy  int64
}

type GrantSubscriptionResetCardsResult struct {
	GroupIDs []int64
	// RecipientCount counts eligible subscriptions. A user subscribed to two
	// selected groups receives cards on both subscriptions and counts twice.
	RecipientCount    int64
	CardCount         int64
	RecipientsByGroup map[int64]int64
	ExpiresAt         time.Time
}

type GrantSubscriptionResetCardsToSubscriptionInput struct {
	SubscriptionID int64
	Count          int
	ExpiresAt      time.Time
	IssuedBy       int64
}

type GrantSubscriptionResetCardsToSubscriptionResult struct {
	SubscriptionID int64
	UserID         int64
	GroupID        int64
	CardCount      int64
	ExpiresAt      time.Time
}

// SubscriptionResetCardRepository owns the atomic grant/consume persistence
// operations. ConsumeAndReset must deduct one card and reset all usage windows
// in the same database transaction.
type SubscriptionResetCardRepository interface {
	GrantToGroups(ctx context.Context, groupIDs []int64, quantity int, expiresAt time.Time, issuedBy int64, now time.Time) (map[int64]int64, error)
	GrantToSubscriptions(ctx context.Context, subscriptionIDs []int64, quantity int, expiresAt time.Time, issuedBy int64, now time.Time) (map[int64]int64, error)
	ListAvailable(ctx context.Context, subscriptionIDs []int64, now time.Time) (map[int64]SubscriptionResetCardSummary, error)
	ConsumeAndReset(ctx context.Context, userID, subscriptionID int64, now, windowStart time.Time) (groupID int64, err error)
}

package service

import (
	"context"
	"log"
	"sort"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrResetCardRepositoryUnavailable = infraerrors.InternalServer("RESET_CARD_REPOSITORY_UNAVAILABLE", "reset card service is unavailable")
	ErrResetCardInvalidCount          = infraerrors.BadRequest("RESET_CARD_INVALID_COUNT", "reset card count must be between 1 and 1000")
	ErrResetCardInvalidExpiry         = infraerrors.BadRequest("RESET_CARD_INVALID_EXPIRY", "reset card expiry must be in the future and no later than 2099-12-31")
	ErrResetCardInvalidGroups         = infraerrors.BadRequest("RESET_CARD_INVALID_GROUPS", "at least one subscription group is required")
	ErrResetCardGroupInactive         = infraerrors.BadRequest("RESET_CARD_GROUP_INACTIVE", "reset cards can only be granted to active subscription groups")
	ErrResetCardRecipientUnavailable  = infraerrors.Conflict("RESET_CARD_RECIPIENT_UNAVAILABLE", "the subscription is no longer active or eligible for reset cards")
	ErrResetCardUnavailable           = infraerrors.Conflict("RESET_CARD_UNAVAILABLE", "no unexpired reset card is available for this subscription")
)

func (s *SubscriptionService) SetResetCardRepository(repo SubscriptionResetCardRepository) {
	s.resetCardRepo = repo
}

func (s *SubscriptionService) GrantResetCards(ctx context.Context, input *GrantSubscriptionResetCardsInput) (*GrantSubscriptionResetCardsResult, error) {
	if s.resetCardRepo == nil {
		return nil, ErrResetCardRepositoryUnavailable
	}
	if input == nil || len(input.GroupIDs) == 0 {
		return nil, ErrResetCardInvalidGroups
	}
	for _, groupID := range input.GroupIDs {
		if groupID <= 0 {
			return nil, ErrResetCardInvalidGroups
		}
	}

	now, err := validateResetCardGrant(input.Count, input.ExpiresAt)
	if err != nil {
		return nil, err
	}

	groupIDs := uniquePositiveInt64s(input.GroupIDs)
	if len(groupIDs) == 0 {
		return nil, ErrResetCardInvalidGroups
	}
	for _, groupID := range groupIDs {
		group, err := s.groupRepo.GetByID(ctx, groupID)
		if err != nil {
			return nil, err
		}
		if !group.IsSubscriptionType() {
			return nil, ErrGroupNotSubscriptionType
		}
		if !group.IsActive() {
			return nil, ErrResetCardGroupInactive
		}
	}

	byGroup, err := s.resetCardRepo.GrantToGroups(ctx, groupIDs, input.Count, input.ExpiresAt, input.IssuedBy, now)
	if err != nil {
		return nil, err
	}
	var recipients int64
	for _, count := range byGroup {
		recipients += count
	}

	return &GrantSubscriptionResetCardsResult{
		GroupIDs:          groupIDs,
		RecipientCount:    recipients,
		CardCount:         recipients * int64(input.Count),
		RecipientsByGroup: byGroup,
		ExpiresAt:         input.ExpiresAt,
	}, nil
}

// GrantResetCardsToSubscription grants cards to exactly one active user
// subscription. It is used by the per-row admin action and never fans out to
// other subscriptions owned by the same user or in the same group.
func (s *SubscriptionService) GrantResetCardsToSubscription(
	ctx context.Context,
	input *GrantSubscriptionResetCardsToSubscriptionInput,
) (*GrantSubscriptionResetCardsToSubscriptionResult, error) {
	if s.resetCardRepo == nil {
		return nil, ErrResetCardRepositoryUnavailable
	}
	if input == nil || input.SubscriptionID <= 0 {
		return nil, ErrSubscriptionNotFound
	}

	now, err := validateResetCardGrant(input.Count, input.ExpiresAt)
	if err != nil {
		return nil, err
	}
	subscription, err := s.userSubRepo.GetByID(ctx, input.SubscriptionID)
	if err != nil {
		return nil, err
	}
	if subscription.Status == SubscriptionStatusExpired || !subscription.ExpiresAt.After(now) {
		return nil, ErrSubscriptionExpired
	}
	if subscription.Status != SubscriptionStatusActive {
		return nil, ErrSubscriptionSuspended
	}

	group := subscription.Group
	if group == nil {
		group, err = s.groupRepo.GetByID(ctx, subscription.GroupID)
		if err != nil {
			return nil, err
		}
	}
	if !group.IsSubscriptionType() {
		return nil, ErrGroupNotSubscriptionType
	}
	if !group.IsActive() {
		return nil, ErrResetCardGroupInactive
	}

	bySubscription, err := s.resetCardRepo.GrantToSubscriptions(
		ctx,
		[]int64{input.SubscriptionID},
		input.Count,
		input.ExpiresAt,
		input.IssuedBy,
		now,
	)
	if err != nil {
		return nil, err
	}
	if bySubscription[input.SubscriptionID] != 1 {
		return nil, ErrResetCardRecipientUnavailable
	}

	return &GrantSubscriptionResetCardsToSubscriptionResult{
		SubscriptionID: input.SubscriptionID,
		UserID:         subscription.UserID,
		GroupID:        subscription.GroupID,
		CardCount:      int64(input.Count),
		ExpiresAt:      input.ExpiresAt,
	}, nil
}

func validateResetCardGrant(count int, expiresAt time.Time) (time.Time, error) {
	if count < 1 || count > MaxResetCardsPerGrant {
		return time.Time{}, ErrResetCardInvalidCount
	}
	now := time.Now()
	if !expiresAt.After(now) || expiresAt.After(MaxExpiresAt) {
		return time.Time{}, ErrResetCardInvalidExpiry
	}
	return now, nil
}

// UseResetCard validates ownership and availability in the repository, where
// one card is consumed and all three subscription usage windows are reset in a
// single transaction.
func (s *SubscriptionService) UseResetCard(ctx context.Context, userID, subscriptionID int64) (*UserSubscription, error) {
	if s.resetCardRepo == nil {
		return nil, ErrResetCardRepositoryUnavailable
	}
	if userID <= 0 || subscriptionID <= 0 {
		return nil, ErrSubscriptionNotFound
	}

	now := time.Now()
	groupID, err := s.resetCardRepo.ConsumeAndReset(ctx, userID, subscriptionID, now, startOfDay(now))
	if err != nil {
		return nil, err
	}

	// The reset is already committed at this point. Invalidate both local and
	// shared caches (and notify peer instances), but never turn a successful
	// card consumption into an API failure because cache invalidation failed.
	if cacheErr := s.invalidateSubscriptionCaches(userID, groupID); cacheErr != nil {
		log.Printf("Warning: failed to invalidate subscription caches after reset card use: user=%d group=%d err=%v", userID, groupID, cacheErr)
	}

	sub, err := s.userSubRepo.GetByID(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	items := []UserSubscription{*sub}
	if err := s.attachResetCardSummaries(ctx, items); err != nil {
		return nil, err
	}
	return &items[0], nil
}

func (s *SubscriptionService) attachResetCardSummaries(ctx context.Context, subscriptions []UserSubscription) error {
	if s.resetCardRepo == nil || len(subscriptions) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(subscriptions))
	for i := range subscriptions {
		subscriptions[i].ResetCardCount = 0
		subscriptions[i].ResetCardBatches = nil
		ids = append(ids, subscriptions[i].ID)
	}
	summaries, err := s.resetCardRepo.ListAvailable(ctx, ids, time.Now())
	if err != nil {
		return err
	}
	for i := range subscriptions {
		if summary, ok := summaries[subscriptions[i].ID]; ok {
			subscriptions[i].ResetCardCount = summary.AvailableCount
			subscriptions[i].ResetCardBatches = append([]SubscriptionResetCardBatch(nil), summary.Batches...)
		}
	}
	return nil
}

func uniquePositiveInt64s(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

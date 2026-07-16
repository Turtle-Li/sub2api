//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type resetCardRepositoryStub struct {
	grantGroups             []int64
	grantSubscriptions      []int64
	grantQuantity           int
	grantExpiresAt          time.Time
	grantIssuedBy           int64
	grantResult             map[int64]int64
	grantSubscriptionResult map[int64]int64
	grantErr                error
	consumeUserID           int64
	consumeSubID            int64
	consumeGroupID          int64
	consumeErr              error
	availableResult         map[int64]SubscriptionResetCardSummary
}

func (r *resetCardRepositoryStub) GrantToGroups(_ context.Context, groupIDs []int64, quantity int, expiresAt time.Time, issuedBy int64, _ time.Time) (map[int64]int64, error) {
	r.grantGroups = append([]int64(nil), groupIDs...)
	r.grantQuantity = quantity
	r.grantExpiresAt = expiresAt
	r.grantIssuedBy = issuedBy
	return r.grantResult, r.grantErr
}

func (r *resetCardRepositoryStub) GrantToSubscriptions(_ context.Context, subscriptionIDs []int64, quantity int, expiresAt time.Time, issuedBy int64, _ time.Time) (map[int64]int64, error) {
	r.grantSubscriptions = append([]int64(nil), subscriptionIDs...)
	r.grantQuantity = quantity
	r.grantExpiresAt = expiresAt
	r.grantIssuedBy = issuedBy
	return r.grantSubscriptionResult, r.grantErr
}

func (r *resetCardRepositoryStub) ListAvailable(_ context.Context, _ []int64, _ time.Time) (map[int64]SubscriptionResetCardSummary, error) {
	return r.availableResult, nil
}

func (r *resetCardRepositoryStub) ConsumeAndReset(_ context.Context, userID, subscriptionID int64, _, _ time.Time) (int64, error) {
	r.consumeUserID = userID
	r.consumeSubID = subscriptionID
	return r.consumeGroupID, r.consumeErr
}

type resetCardGroupRepoStub struct {
	groupRepoNoop
	groups map[int64]*Group
}

func (r resetCardGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	group, ok := r.groups[id]
	if !ok {
		return nil, ErrGroupNotFound
	}
	copy := *group
	return &copy, nil
}

type resetCardUserSubRepoStub struct {
	userSubRepoNoop
	sub *UserSubscription
}

func (r resetCardUserSubRepoStub) GetByID(_ context.Context, id int64) (*UserSubscription, error) {
	if r.sub == nil || r.sub.ID != id {
		return nil, ErrSubscriptionNotFound
	}
	copy := *r.sub
	return &copy, nil
}

func TestGrantResetCards_DeduplicatesGroupsAndStacksQuantity(t *testing.T) {
	expiresAt := time.Now().Add(48 * time.Hour)
	cardRepo := &resetCardRepositoryStub{grantResult: map[int64]int64{1: 2, 2: 1}}
	groupRepo := resetCardGroupRepoStub{groups: map[int64]*Group{
		1: {ID: 1, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription},
		2: {ID: 2, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription},
	}}
	svc := NewSubscriptionService(groupRepo, userSubRepoNoop{}, nil, nil, nil)
	svc.SetResetCardRepository(cardRepo)

	result, err := svc.GrantResetCards(context.Background(), &GrantSubscriptionResetCardsInput{
		GroupIDs:  []int64{2, 1, 2},
		Count:     3,
		ExpiresAt: expiresAt,
		IssuedBy:  99,
	})

	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, cardRepo.grantGroups)
	require.Equal(t, 3, cardRepo.grantQuantity)
	require.Equal(t, int64(3), result.RecipientCount)
	require.Equal(t, int64(9), result.CardCount)
	require.Equal(t, int64(99), cardRepo.grantIssuedBy)
}

func TestGrantResetCards_RejectsInvalidExpiryBeforeWriting(t *testing.T) {
	cardRepo := &resetCardRepositoryStub{}
	svc := NewSubscriptionService(resetCardGroupRepoStub{}, userSubRepoNoop{}, nil, nil, nil)
	svc.SetResetCardRepository(cardRepo)

	_, err := svc.GrantResetCards(context.Background(), &GrantSubscriptionResetCardsInput{
		GroupIDs:  []int64{1},
		Count:     1,
		ExpiresAt: time.Now().Add(-time.Minute),
	})

	require.ErrorIs(t, err, ErrResetCardInvalidExpiry)
	require.Empty(t, cardRepo.grantGroups)
}

func TestGrantResetCards_RejectsNonPositiveGroupBeforeWriting(t *testing.T) {
	cardRepo := &resetCardRepositoryStub{}
	svc := NewSubscriptionService(resetCardGroupRepoStub{}, userSubRepoNoop{}, nil, nil, nil)
	svc.SetResetCardRepository(cardRepo)

	_, err := svc.GrantResetCards(context.Background(), &GrantSubscriptionResetCardsInput{
		GroupIDs:  []int64{1, 0},
		Count:     1,
		ExpiresAt: time.Now().Add(time.Hour),
	})

	require.ErrorIs(t, err, ErrResetCardInvalidGroups)
	require.Empty(t, cardRepo.grantGroups)
}

func TestGrantResetCards_RejectsInactiveGroupBeforeWriting(t *testing.T) {
	cardRepo := &resetCardRepositoryStub{}
	svc := NewSubscriptionService(resetCardGroupRepoStub{groups: map[int64]*Group{
		1: {ID: 1, Status: StatusDisabled, SubscriptionType: SubscriptionTypeSubscription},
	}}, userSubRepoNoop{}, nil, nil, nil)
	svc.SetResetCardRepository(cardRepo)

	_, err := svc.GrantResetCards(context.Background(), &GrantSubscriptionResetCardsInput{
		GroupIDs:  []int64{1},
		Count:     1,
		ExpiresAt: time.Now().Add(time.Hour),
	})

	require.ErrorIs(t, err, ErrResetCardGroupInactive)
	require.Empty(t, cardRepo.grantGroups)
}

func TestGrantResetCardsToSubscription_TargetsOnlySelectedSubscription(t *testing.T) {
	expiresAt := time.Now().Add(48 * time.Hour)
	cardRepo := &resetCardRepositoryStub{grantSubscriptionResult: map[int64]int64{7: 1}}
	groupRepo := resetCardGroupRepoStub{groups: map[int64]*Group{
		20: {ID: 20, Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription},
	}}
	userSubRepo := resetCardUserSubRepoStub{sub: &UserSubscription{
		ID:        7,
		UserID:    10,
		GroupID:   20,
		Status:    SubscriptionStatusActive,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}}
	svc := NewSubscriptionService(groupRepo, userSubRepo, nil, nil, nil)
	svc.SetResetCardRepository(cardRepo)

	result, err := svc.GrantResetCardsToSubscription(context.Background(), &GrantSubscriptionResetCardsToSubscriptionInput{
		SubscriptionID: 7,
		Count:          4,
		ExpiresAt:      expiresAt,
		IssuedBy:       99,
	})

	require.NoError(t, err)
	require.Equal(t, []int64{7}, cardRepo.grantSubscriptions)
	require.Equal(t, 4, cardRepo.grantQuantity)
	require.Equal(t, int64(99), cardRepo.grantIssuedBy)
	require.Equal(t, int64(10), result.UserID)
	require.Equal(t, int64(20), result.GroupID)
	require.Equal(t, int64(4), result.CardCount)
}

func TestGrantResetCardsToSubscription_RejectsInactiveSubscriptionBeforeWriting(t *testing.T) {
	cardRepo := &resetCardRepositoryStub{}
	userSubRepo := resetCardUserSubRepoStub{sub: &UserSubscription{
		ID:        7,
		UserID:    10,
		GroupID:   20,
		Status:    SubscriptionStatusSuspended,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}}
	svc := NewSubscriptionService(resetCardGroupRepoStub{}, userSubRepo, nil, nil, nil)
	svc.SetResetCardRepository(cardRepo)

	_, err := svc.GrantResetCardsToSubscription(context.Background(), &GrantSubscriptionResetCardsToSubscriptionInput{
		SubscriptionID: 7,
		Count:          1,
		ExpiresAt:      time.Now().Add(time.Hour),
	})

	require.ErrorIs(t, err, ErrSubscriptionSuspended)
	require.Empty(t, cardRepo.grantSubscriptions)
}

func TestUseResetCard_ReturnsRefreshedCount(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	cardRepo := &resetCardRepositoryStub{
		consumeGroupID: 20,
		availableResult: map[int64]SubscriptionResetCardSummary{
			7: {
				AvailableCount: 1,
				Batches:        []SubscriptionResetCardBatch{{Remaining: 1, ExpiresAt: expiresAt}},
			},
		},
	}
	userSubRepo := resetCardUserSubRepoStub{sub: &UserSubscription{ID: 7, UserID: 10, GroupID: 20}}
	svc := NewSubscriptionService(groupRepoNoop{}, userSubRepo, nil, nil, nil)
	svc.SetResetCardRepository(cardRepo)

	result, err := svc.UseResetCard(context.Background(), 10, 7)

	require.NoError(t, err)
	require.Equal(t, int64(10), cardRepo.consumeUserID)
	require.Equal(t, int64(7), cardRepo.consumeSubID)
	require.Equal(t, 1, result.ResetCardCount)
	require.Len(t, result.ResetCardBatches, 1)
}

func TestUseResetCard_PropagatesOwnershipAndAvailabilityErrors(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{name: "wrong owner", err: ErrSubscriptionNotFound},
		{name: "no cards", err: ErrResetCardUnavailable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cardRepo := &resetCardRepositoryStub{consumeErr: testCase.err}
			svc := NewSubscriptionService(groupRepoNoop{}, resetCardUserSubRepoStub{}, nil, nil, nil)
			svc.SetResetCardRepository(cardRepo)

			_, err := svc.UseResetCard(context.Background(), 10, 7)

			require.ErrorIs(t, err, testCase.err)
		})
	}
}

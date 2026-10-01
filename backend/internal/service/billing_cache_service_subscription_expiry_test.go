//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

type subscriptionExpiryCacheStub struct {
	fakeZeroQuotaCache
	data *SubscriptionCacheData
}

func (f *subscriptionExpiryCacheStub) GetSubscriptionCache(_ context.Context, _ int64, _ int64) (*SubscriptionCacheData, error) {
	clone := *f.data
	return &clone, nil
}

// 订阅到期必须返回 ErrSubscriptionExpired（网关映射为"订阅已到期"中文提示），
// 其它非 active 状态仍返回 ErrSubscriptionInvalid。
func TestCheckSubscriptionEligibility_DistinguishesExpiry(t *testing.T) {
	tests := []struct {
		name string
		data SubscriptionCacheData
		want error
	}{
		{"expired status", SubscriptionCacheData{Status: SubscriptionStatusExpired, ExpiresAt: time.Now().Add(-time.Hour)}, ErrSubscriptionExpired},
		{"active but past expiry", SubscriptionCacheData{Status: SubscriptionStatusActive, ExpiresAt: time.Now().Add(-time.Minute)}, ErrSubscriptionExpired},
		{"suspended", SubscriptionCacheData{Status: SubscriptionStatusSuspended, ExpiresAt: time.Now().Add(time.Hour)}, ErrSubscriptionInvalid},
		{"active and valid", SubscriptionCacheData{Status: SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.data
			s := &BillingCacheService{cache: &subscriptionExpiryCacheStub{data: &data}}
			err := s.checkSubscriptionEligibility(context.Background(), 1, &Group{ID: 2}, &UserSubscription{})
			if tt.want == nil {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
	}
}

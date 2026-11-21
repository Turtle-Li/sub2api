package service

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// Zero is a per-user/per-group ledger namespace, not a bearer credential or a database API key.
const VideoStudioWalletSlot int64 = 0

func (s *APIKeyService) VideoStudioWalletGrants(ctx context.Context, userID int64, groups []int64) ([]*APIKey, error) {
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.ID != userID || !user.IsActive() {
		return nil, nil
	}
	grants := make([]*APIKey, 0, len(groups))
	for _, id := range groups {
		g, err := s.groupRepo.GetByIDLite(ctx, id)
		if errors.Is(err, ErrGroupNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if g == nil || g.ID != id || !g.IsActive() || g.SubscriptionType != SubscriptionTypeStandard || !GroupAllowsImageGeneration(g) || !(g.IsMuseMediaOnly() || IsGoodPutVideoGroup(g)) || !studioWalletAccess(s.cfg, user, g) {
			continue
		}
		grants = append(grants, &APIKey{ID: VideoStudioWalletSlot, UserID: user.ID, User: user, GroupID: &g.ID, Group: g, Status: StatusActive})
	}
	return grants, nil
}

func (s *OpenAIGatewayService) StudioModelGroup(g *Group, model string) bool {
	if g == nil || !slices.Contains(g.ModelAllowlist.Models, model) {
		return false
	}
	if model == MuseVideoModel {
		if !g.IsMuseMediaOnly() {
			return false
		}
		if s.cfg != nil && s.cfg.Gateway.VideoStudioMuseGroupID > 0 {
			return s.cfg.Gateway.VideoStudioMuseGroupID == g.ID
		}
		return g.IsExclusive
	}
	if !IsGoodPutVideoModel(model) || !IsGoodPutVideoGroup(g) || !s.goodPutPublicGroup(g) {
		return false
	}
	pinned := s.cfg.Gateway.VideoStudioH3GroupID
	return pinned == 0 || pinned == g.ID
}

func studioWalletAccess(cfg *config.Config, user *User, g *Group) bool {
	return user.CanBindGroup(g.ID, g.IsExclusive) || (!user.RestrictPublicGroups && studioSharedGroup(cfg, g))
}

// Routing affinity only, never authorization. The caller first resolves the durable receipt to an account.
func StudioBoundVideoHash(receipt string, k *APIKey) string {
	if k.ID != VideoStudioWalletSlot {
		return GrokMediaVideoRequestSessionHash(receipt, k.UserID, k.ID)
	}
	if k.UserID <= 0 || k.GroupID == nil || *k.GroupID <= 0 || receipt == "" {
		return ""
	}
	return "video-studio-wallet:" + DeriveSessionHashFromSeed(fmt.Sprintf("%d:%d:%s", k.UserID, *k.GroupID, receipt))
}

// Naming a routing group is not a permission change. Only explicit sharing of an
// exact, correctly typed channel enables the session-only Image Studio-style grant.
func studioSharedGroup(cfg *config.Config, g *Group) bool {
	if cfg == nil || g == nil || g.ID <= 0 || !cfg.Gateway.VideoStudioShareConfiguredGroups {
		return false
	}
	return (cfg.Gateway.VideoStudioMuseGroupID == g.ID && g.IsMuseMediaOnly()) || (cfg.Gateway.VideoStudioH3GroupID == g.ID && cfg.Gateway.GoodPutVideoPublicGroupID == g.ID && IsGoodPutVideoGroup(g))
}

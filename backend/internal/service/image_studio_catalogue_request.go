package service

import "context"

// These snapshots live for one catalogue request only. Pricing and permission
// changes are visible on the next request; submission always reads fresh data.
type imageCatalogueGroup struct {
	repo   BatchImageGroupPricingRepository
	id     int64
	loaded bool
	group  *Group
	err    error
}

func (r *imageCatalogueGroup) GetByIDLite(ctx context.Context, id int64) (*Group, error) {
	if !r.loaded || r.id != id {
		r.group, r.err = r.repo.GetByIDLite(ctx, id)
		r.id, r.loaded = id, true
	}
	return r.group, r.err
}

type imageCatalogueUserRate struct {
	repo            BatchImageUserGroupRateRepository
	userID, groupID int64
	loaded          bool
	rate            *float64
	err             error
}

func (r *imageCatalogueUserRate) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	if !r.loaded || r.userID != userID || r.groupID != groupID {
		r.rate, r.err = r.repo.GetByUserAndGroup(ctx, userID, groupID)
		r.userID, r.groupID, r.loaded = userID, groupID, true
	}
	return r.rate, r.err
}

func (s *BatchImagePublicService) catalogueRequest() *BatchImagePublicService {
	request := *s
	if s.GroupRepo != nil {
		request.GroupRepo = &imageCatalogueGroup{repo: s.GroupRepo}
	}
	if s.UserGroupRateRepo != nil {
		request.UserGroupRateRepo = &imageCatalogueUserRate{repo: s.UserGroupRateRepo}
	}
	return &request
}

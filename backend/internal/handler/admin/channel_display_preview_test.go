//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type displayPreviewChannelRepo struct{ service.ChannelRepository }

func (displayPreviewChannelRepo) GetByID(_ context.Context, id int64) (*service.Channel, error) {
	if id != 7 {
		return nil, service.ErrChannelNotFound
	}
	return &service.Channel{ID: id, FeaturesConfig: map[string]any{
		"claude_display_models_by_group": map[int64][]string{11: {"claude-sonnet-4-6"}},
	}}, nil
}

type displayPreviewGroupRepo struct{ service.GroupRepository }

func (displayPreviewGroupRepo) ListActive(context.Context) ([]service.Group, error) {
	return []service.Group{{ID: 11, Platform: service.PlatformAnthropic}}, nil
}

type displayPreviewAccountRepo struct{ service.AccountRepository }

func (displayPreviewAccountRepo) ListModelAvailabilityCandidates(context.Context, *int64, []string, bool) ([]service.Account, error) {
	return nil, nil
}

func TestPreviewClaudeDisplayModelsResponsePreservesEmptyGroup(t *testing.T) {
	catalog := service.NewChannelModelCatalog(displayPreviewAccountRepo{}, nil)
	channels := service.NewChannelService(displayPreviewChannelRepo{}, displayPreviewGroupRepo{}, nil, nil, nil, catalog)
	h := NewChannelHandler(channels, nil, nil)
	router := gin.New()
	const path = "/channels/claude-display-models/preview"
	router.POST(path, h.PreviewClaudeDisplayModels)

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"channel_id":7,"group_ids":[11]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"models_by_group":{"11":[]}`)
	require.Contains(t, w.Body.String(), `"previous_models_by_group":{"11":["claude-sonnet-4-6"]}`)
}

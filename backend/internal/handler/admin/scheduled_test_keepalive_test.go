//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type keepAliveHandlerPlans struct {
	service.ScheduledTestPlanRepository
	plan    *service.ScheduledTestPlan
	updated *service.ScheduledTestPlan
}

func (r *keepAliveHandlerPlans) GetByID(context.Context, int64) (*service.ScheduledTestPlan, error) {
	copy := *r.plan
	return &copy, nil
}

func (r *keepAliveHandlerPlans) ListByAccountID(context.Context, int64) ([]*service.ScheduledTestPlan, error) {
	return []*service.ScheduledTestPlan{r.plan}, nil
}

func (r *keepAliveHandlerPlans) Update(_ context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	r.updated = plan
	return plan, nil
}

func TestScheduledKeepAliveHandler_PartialUpdates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name    string
		body    string
		status  int
		hours   int
		enabled bool
	}{
		{"disable preserves threshold", `{"enabled":false}`, http.StatusOK, 48, false},
		{"threshold editable", `{"idle_hours":24}`, http.StatusOK, 24, true},
		{"zero restores ordinary schedule", `{"idle_hours":0}`, http.StatusOK, 0, true},
		{"negative threshold rejected", `{"idle_hours":-1}`, http.StatusBadRequest, 0, false},
		{"fractional threshold rejected", `{"idle_hours":1.5}`, http.StatusBadRequest, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &keepAliveHandlerPlans{plan: &service.ScheduledTestPlan{
				ID: 1, AccountID: 2, ModelID: "small-model", CronExpression: "0 * * * *", IdleHours: 48, Enabled: true,
			}}
			handler := NewScheduledTestHandler(service.NewScheduledTestService(repo, nil))
			router := gin.New()
			router.PUT("/plans/:id", handler.Update)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/plans/1", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.status, recorder.Code)
			if tc.status == http.StatusOK {
				require.Equal(t, tc.hours, repo.updated.IdleHours)
				require.Equal(t, tc.enabled, repo.updated.Enabled)
			} else {
				require.Nil(t, repo.updated)
			}
		})
	}
}

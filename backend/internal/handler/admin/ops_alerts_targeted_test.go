package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type targetedOpsAlertRulesRepo struct {
	service.OpsRepository
	writes []*service.OpsAlertRule
}

func (r *targetedOpsAlertRulesRepo) CreateAlertRule(_ context.Context, rule *service.OpsAlertRule) (*service.OpsAlertRule, error) {
	r.writes = append(r.writes, rule)
	return rule, nil
}

func (r *targetedOpsAlertRulesRepo) UpdateAlertRule(_ context.Context, rule *service.OpsAlertRule) (*service.OpsAlertRule, error) {
	r.writes = append(r.writes, rule)
	return rule, nil
}

func TestOpsTargetedAlertHandlerValidationAndClearing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &targetedOpsAlertRulesRepo{}
	h := &OpsHandler{opsService: service.NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)}
	router := gin.New()
	router.POST("/rules", h.CreateAlertRule)
	router.PUT("/rules/:id", h.UpdateAlertRule)
	const upstreamBase = `"name":"Rule","metric_type":"account_upstream_error","operator":">=","threshold":1`
	const accountBase = `"name":"Rule","metric_type":"account_error_ratio","operator":">=","threshold":1`
	send := func(method, path, fields string) *httptest.ResponseRecorder {
		res := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader("{"+fields+"}"))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(res, req)
		return res
	}
	for _, tc := range []struct{ name, fields, problem string }{
		{"no filters", upstreamBase, "requires account_ids"},
		{"keywords alone", upstreamBase + `,"filters":{"error_keywords":["quota"]}`, "requires account_ids"},
		{"invalid account", upstreamBase + `,"filters":{"account_ids":[0]}`, "account_ids"},
		{"fractional account", upstreamBase + `,"filters":{"account_ids":[1.5]}`, "account_ids"},
		{"status out of range", upstreamBase + `,"filters":{"upstream_status_codes":[399]}`, "upstream_status_codes"},
		{"invalid provider code", upstreamBase + `,"filters":{"provider_codes":["quota=bad"]}`, "provider_codes"},
		{"invalid keyword", upstreamBase + `,"filters":{"group_id":1,"error_keywords":[" "]}`, "error_keywords"},
		{"bad notification address", upstreamBase + `,"filters":{"group_id":1},"notify_emails":["admin <a@example.invalid>"]`, "notify_emails"},
		{"email header injection", upstreamBase + `,"filters":{"group_id":1},"notify_emails":["a@example.invalid\r\nBcc: b@example.invalid"]`, "notify_emails"},
		{"invalid feishu toggle", upstreamBase + `,"filters":{"group_id":1},"notify_feishu":null`, "notify_feishu"},
		{"unavailable window", upstreamBase + `,"filters":{"group_id":1},"window_minutes":10`, "window_minutes"},
		{"legacy window unchanged", accountBase + `,"window_minutes":15`, "window_minutes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := send(http.MethodPost, "/rules", tc.fields)
			require.Equal(t, http.StatusBadRequest, res.Code, res.Body.String())
			require.Contains(t, res.Body.String(), tc.problem)
		})
	}
	require.Empty(t, repo.writes)
	addresses := make([]string, 21)
	for i := range addresses {
		addresses[i] = fmt.Sprintf("ops%d@example.invalid", i)
	}
	encoded, err := json.Marshal(addresses)
	require.NoError(t, err)
	res := send(http.MethodPost, "/rules", upstreamBase+`,"filters":{"group_id":1},"notify_emails":`+string(encoded))
	require.Equal(t, http.StatusBadRequest, res.Code)
	res = send(http.MethodPost, "/rules", upstreamBase+`,"filters":{"group_id":1},"custom_message":"`+strings.Repeat("中", 501)+`"`)
	require.Equal(t, http.StatusBadRequest, res.Code)

	for _, window := range []int{1, 5, 15, 60} {
		res = send(http.MethodPost, "/rules", upstreamBase+fmt.Sprintf(`,"window_minutes":%d`, window)+`,
			"filters":{"platform":"openai","group_id":2,"region":"east","account_ids":[258,248,248],
			"upstream_status_codes":[503,402,402],"provider_codes":["vendor:pay","vendor:pay"],"error_keywords":["quota_%"]},
			"notify_feishu":true,"notify_emails":["ops@example.invalid","OPS@example.invalid"],
			"custom_message":"Check plan.\nAPI Key: sk-example-secret"`)
		require.Equal(t, http.StatusOK, res.Code, res.Body.String())
		rule := repo.writes[len(repo.writes)-1]
		require.Equal(t, window, rule.WindowMinutes)
		require.Equal(t, []int64{248, 258}, rule.Filters["account_ids"])
		require.Equal(t, []int{402, 503}, rule.Filters["upstream_status_codes"])
		require.Equal(t, []string{"vendor:pay"}, rule.Filters["provider_codes"])
		require.Equal(t, []string{"quota_%"}, rule.Filters["error_keywords"])
		require.Equal(t, "east", rule.Filters["region"])
		require.True(t, rule.NotifyFeishu)
		require.Equal(t, []string{"ops@example.invalid"}, rule.NotifyEmails)
		require.NotContains(t, rule.CustomMessage, "sk-example-secret")
	}
	res = send(http.MethodPut, "/rules/7", accountBase+`,"filters":{"account_ids":[248],"upstream_status_codes":[402]},
		"notify_feishu":false,"notify_emails":[],"custom_message":""`)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	updated := repo.writes[len(repo.writes)-1]
	require.Equal(t, int64(7), updated.ID)
	require.NotContains(t, updated.Filters, "upstream_status_codes")
	require.False(t, updated.NotifyFeishu)
	require.Empty(t, updated.NotifyEmails)
	require.Empty(t, updated.CustomMessage)
	require.Equal(t, &service.OpsAlertNotificationUpdate{Feishu: true, Emails: true, CustomMessage: true}, updated.NotificationUpdate)
	res = send(http.MethodPut, "/rules/7", accountBase)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Equal(t, &service.OpsAlertNotificationUpdate{}, repo.writes[len(repo.writes)-1].NotificationUpdate)
}

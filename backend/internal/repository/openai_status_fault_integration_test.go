//go:build integration

package repository

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestOpenAIStatusFaultResponsibility(t *testing.T) {
	// The fixture is the final Ops outcome, not an upstream retry or a request body.
	for _, c := range []struct {
		name, input string
		want        bool
	}{
		{"tool reference", `{"error_type":"invalid_request_error","status_code":400,"error_phase":"request","error_owner":"client"}`, false},
		{"bad previous ID echoed upstream", `{"status_code":400,"upstream_status_code":400,"error_phase":"upstream","error_owner":"provider","account_id":2,"error_type":"invalid_request_error","error_message":"Invalid 'previous_response_id'."}`, false},
		{"body upload timeout", `{"status_code":408,"error_phase":"request","error_owner":"client","error_message":"Request body read timed out"}`, false},
		{"upstream material download", `{"status_code":502,"upstream_status_code":504,"account_id":2,"error_phase":"upstream","error_owner":"provider","upstream_error_message":"Unable to download file from the provided URL within the timeout"}`, false},
		{"result download timeout", `{"status_code":504,"error_phase":"internal","error_owner":"platform","provider_error_code":"upstream_download_timeout"}`, false},
		{"client disconnect", `{"status_code":499,"upstream_status_code":502,"error_phase":"network","error_owner":"provider"}`, false},
		{"customer key", `{"status_code":401,"error_phase":"auth","error_owner":"client","is_business_limited":true}`, false},
		{"customer rate", `{"status_code":429,"error_phase":"request","error_owner":"client","is_business_limited":true}`, false},
		{"customer quota", `{"status_code":402,"error_phase":"request","error_owner":"client","is_business_limited":true}`, false},
		{"policy", `{"status_code":403,"upstream_status_code":403,"error_phase":"upstream","error_owner":"provider","account_id":2,"provider_error_code":"content_policy_violation"}`, false},
		{"unknown 400", `{"status_code":400,"error_phase":"upstream","error_owner":"provider","account_id":2}`, false},
		{"unknown 5xx", `{"status_code":502,"error_phase":"","error_owner":""}`, false},
		{"recovered upstream attempt", `{"status_code":200,"upstream_status_code":503,"error_phase":"upstream","error_owner":"provider","account_id":2}`, false},
		{"token count", `{"status_code":502,"upstream_status_code":503,"error_phase":"upstream","error_owner":"provider","is_count_tokens":true}`, false},
		{"our account auth", `{"status_code":502,"error_phase":"account_auth","error_owner":"provider","account_id":2}`, true},
		{"credential fetch timeout is not a caller file", `{"status_code":502,"error_phase":"account_auth","error_owner":"provider","upstream_error_message":"Failed to fetch https://issuer.invalid/token: i/o timeout"}`, true},
		{"provider key rejected", `{"status_code":502,"upstream_status_code":401,"error_phase":"upstream","error_owner":"provider","account_id":2}`, true},
		{"provider quota", `{"status_code":400,"upstream_status_code":400,"error_phase":"upstream","error_owner":"provider","account_id":2,"provider_error_code":"insufficient_quota"}`, true},
		{"internal rate without upstream status", `{"status_code":429,"account_id":2,"error_owner":"provider","error_phase":"upstream","error_message":"Upstream rate limit exceeded, please retry later"}`, true},
		{"streamed upstream rate limit despite generic business flag", `{"status_code":429,"upstream_status_code":200,"account_id":2,"error_owner":"provider","error_phase":"upstream","is_business_limited":true,"error_message":"Upstream rate limit exceeded, please retry later"}`, true},
		{"upstream usage-limit summary with no HTTP 429 headers", `{"status_code":429,"upstream_status_code":200,"account_id":2,"error_owner":"provider","error_phase":"upstream","upstream_error_message":"The usage limit has been reached"}`, true},
		{"local queue 429 without provider rate evidence", `{"status_code":429,"account_id":2,"error_owner":"provider","error_phase":"upstream","error_message":"Too many open WebSocket connections, please retry later"}`, false},
		{"explicit invalid asset URL despite generic 502 wrapper", `{"status_code":502,"upstream_status_code":502,"error_owner":"provider","error_phase":"upstream","provider_error_code":"invalid_image_url"}`, false},
		{"scheduler all accounts cooling", `{"status_code":429,"error_phase":"routing","error_owner":"platform","is_business_limited":true,"error_message":"All available accounts are currently rate-limited. Please retry later."}`, true},
		{"scheduler empty pool", `{"status_code":503,"error_phase":"routing","error_owner":"platform","is_business_limited":true,"error_message":"Service temporarily unavailable"}`, true},
		{"local model policy not a pool failure", `{"status_code":404,"error_phase":"routing","error_owner":"platform","is_business_limited":true,"error_type":"model_not_found"}`, false},
		{"model generation timeout", `{"status_code":504,"error_phase":"upstream","error_owner":"provider","upstream_status_code":504,"account_id":2,"error_message":"model response deadline exceeded"}`, true},
		{"platform internal", `{"status_code":500,"error_phase":"internal","error_owner":"platform"}`, true},
		{"completed downloads cannot hide an inference timeout", `{"status_code":504,"upstream_status_code":504,"error_owner":"provider","error_phase":"upstream","error_message":"File download completed","upstream_error_message":"generation timeout"}`, true},
		{"wrapped caller error without upstream 5xx", `{"status_code":502,"account_id":2,"error_phase":"upstream","error_owner":"provider","error_type":"invalid_request_error"}`, false},
		{"account limit despite generic ops business flag", `{"status_code":429,"upstream_status_code":429,"account_id":2,"error_phase":"upstream","error_owner":"provider","is_business_limited":true}`, true},
		{"unknown resource forbidden not assumed to be account", `{"status_code":502,"upstream_status_code":403,"account_id":2,"error_phase":"upstream","error_owner":"provider"}`, false},
		{"explicit upstream credential permission", `{"status_code":502,"upstream_status_code":403,"account_id":2,"error_phase":"upstream","error_owner":"provider","upstream_error_message":"Upstream account permission denied"}`, true},
		{"caller cancellation wrapped by gateway", `{"status_code":502,"upstream_status_code":502,"error_phase":"network","error_owner":"provider","provider_error_code":"client_disconnected"}`, false},
		{"provider 408 during inference", `{"status_code":504,"upstream_status_code":408,"account_id":2,"error_phase":"upstream","error_owner":"provider"}`, true},
		{"model fetch is not a file download", `{"status_code":504,"upstream_status_code":504,"error_phase":"upstream","error_owner":"provider","upstream_error_message":"Failed to fetch upstream model response: i/o timeout"}`, true},
		{"upstream fetch of caller image", `{"status_code":504,"upstream_status_code":504,"error_phase":"upstream","error_owner":"provider","upstream_error_message":"Failed to fetch the image at the provided URL: i/o timeout"}`, false},
		{"wrapped ingress body timeout", `{"status_code":502,"upstream_status_code":408,"error_phase":"upstream","error_owner":"provider","error_message":"Timed out reading request body"}`, false},
		{"disconnect without a structured provider code", `{"status_code":502,"upstream_status_code":502,"error_phase":"network","error_owner":"provider","error_message":"Client disconnected during attachment processing"}`, false},
		{"provider capacity encoded as a 400 without a provider code", `{"status_code":502,"upstream_status_code":400,"account_id":2,"error_phase":"upstream","error_owner":"provider","upstream_error_message":"The selected model is at capacity. Please try again later."}`, true},
		{"echoed quota phrase is not an account diagnosis", `{"status_code":400,"upstream_status_code":400,"account_id":2,"error_phase":"upstream","error_owner":"provider","upstream_error_message":"Invalid value: 'you exceeded your current quota'."}`, false},
		{"provider quota without a provider code", `{"status_code":400,"upstream_status_code":400,"account_id":2,"error_phase":"upstream","error_owner":"provider","upstream_error_message":"You exceeded your current quota, please check your plan and billing details."}`, true},
		{"missing upstream credential scopes", `{"status_code":502,"upstream_status_code":403,"account_id":2,"error_phase":"upstream","error_owner":"provider","upstream_error_message":"You have insufficient permissions for this operation. Missing scopes: model.request"}`, true},
		{"provider refusal to access a user file", `{"status_code":502,"upstream_status_code":403,"account_id":2,"error_phase":"upstream","error_owner":"provider","provider_error_code":"permission_denied","upstream_error_message":"You do not have access to this file"}`, false},
		{"upstream stream credential failure with HTTP 200 headers", `{"status_code":401,"upstream_status_code":200,"account_id":2,"error_phase":"upstream","error_owner":"provider","error_type":"authentication_error"}`, true},
		{"final caller parameter error wins over a previous provider failure", `{"status_code":400,"upstream_status_code":503,"account_id":2,"error_phase":"upstream","error_owner":"provider","error_type":"invalid_request_error","error_message":"Invalid 'previous_response_id'."}`, false},
		{"untrusted body cannot hide provider outage", `{"status_code":502,"upstream_status_code":503,"error_phase":"upstream","error_owner":"provider","account_id":2,"error_body":"client cancelled; upstream_download_timeout"}`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got bool
			require.NoError(t, integrationDB.QueryRow("SELECT channel_monitor_openai_fault_v1(jsonb_populate_record(NULL::ops_error_logs,$1::jsonb))", c.input).Scan(&got))
			require.Equal(t, c.want, got)
		})
	}
}

func TestOpenAIStatusFaultHistoricalReplay(t *testing.T) {
	tx := testTx(t)
	exec := func(q string, args ...any) { t.Helper(); _, err := tx.Exec(q, args...); require.NoError(t, err) }
	group := func(platform string) int64 {
		t.Helper()
		var id int64
		require.NoError(t, tx.QueryRow("INSERT INTO groups(name,platform) VALUES ($1,$2) RETURNING id", uuid.NewString(), platform).Scan(&id))
		return id
	}
	g, other := group("openai"), group("anthropic")
	var first, cutoff time.Time
	require.NoError(t, tx.QueryRow(`SELECT date_bin(interval '10 minutes',now(),timestamptz '1970-01-01 00:00Z') - interval '710 minutes', date_trunc('minute',now())`).Scan(&first, &cutoff))
	anchor := cutoff.Add(-time.Hour)
	metric := func(gid int64, platform, model string, at time.Time, n int) {
		exec("INSERT INTO channel_monitor_v2_metrics_1m(bucket_start,group_id,platform,model,error_requests) VALUES ($1,$2,$3,$4,$5)", at, gid, platform, model, n)
	}
	record := func(gid int64, platform, model, id string, at time.Time, sc int, phase, owner string) {
		exec("INSERT INTO ops_error_logs(group_id,platform,requested_model,request_id,created_at,status_code,upstream_status_code,error_phase,error_owner,error_type) VALUES ($1,$2,$3,$4,$5,$6,$6,$7,$8,'api_error')", gid, platform, model, id, at, sc, phase, owner)
	}
	a, b := uuid.NewString(), uuid.NewString()
	metric(g, "openai", "known", anchor, 3)
	record(g, "openai", "known", a, anchor, 502, "upstream", "provider")
	record(g, "openai", "known", a, anchor.Add(time.Second), 400, "request", "client")
	record(g, "openai", "known", "", anchor, 503, "upstream", "provider")
	record(g, "openai", "known", "", anchor, 400, "request", "client")
	record(g, "openai", "known", b, anchor, 200, "upstream", "provider")
	metric(g, "openai", "no-source", anchor, 2)
	record(g, "openai", "no-source", uuid.NewString(), anchor, 401, "auth", "client")
	metric(g, "openai", "all-external", anchor, 1)
	record(g, "openai", "all-external", uuid.NewString(), anchor, 408, "request", "client")
	metric(other, "anthropic", "other", anchor, 1)
	record(other, "anthropic", "other", uuid.NewString(), anchor, 503, "upstream", "provider")
	past := first.Add(-time.Minute)
	metric(g, "openai", "old", past, 1)
	record(g, "openai", "old", uuid.NewString(), past, 503, "upstream", "provider")
	metric(g, "openai", "first-visible", first, 1)
	record(g, "openai", "first-visible", uuid.NewString(), first, 503, "upstream", "provider")
	metric(g, "openai", "unfinished", cutoff, 1)
	record(g, "openai", "unfinished", uuid.NewString(), cutoff, 503, "upstream", "provider")
	sharedID := uuid.NewString()
	metric(g, "openai", "collision", anchor, 1)
	record(g, "openai", "collision", sharedID, anchor, 503, "upstream", "provider")
	record(other, "anthropic", "other", sharedID, anchor.Add(time.Minute), 400, "request", "client")
	migration, err := migrations.FS.ReadFile("285_backfill_openai_public_status_window.sql")
	require.NoError(t, err)
	exec(string(migration))
	exec(string(migration))
	for _, v := range []struct {
		gid   int64
		model string
		valid bool
		count int64
	}{{g, "known", true, 1}, {g, "all-external", true, 0}, {g, "no-source", false, 0}, {other, "other", false, 0}, {g, "old", false, 0}, {g, "first-visible", true, 1}, {g, "unfinished", false, 0}, {g, "collision", false, 0}} {
		var n sql.NullInt64
		require.NoError(t, tx.QueryRow("SELECT openai_fault_requests FROM channel_monitor_v2_metrics_1m WHERE group_id=$1 AND model=$2", v.gid, v.model).Scan(&n))
		require.Equal(t, v.valid, n.Valid, v.model)
		require.Equal(t, v.count, n.Int64, v.model)
	}
}

//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/shopfulfillment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func shopSessionFixture(t *testing.T, edit func(map[string]any, map[string]any)) string {
	t.Helper()
	session := map[string]any{"user": map[string]any{"email": "target@example.com", "id": "user-synthetic"}, "account": map[string]any{"id": "workspace-synthetic", "planType": "free"}, "expires": "2099-01-01T00:00:00Z"}
	claims := map[string]any{"exp": int64(4070908800), "https://api.openai.com/profile": map[string]any{"email": "target@example.com"}, "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "workspace-synthetic", "chatgpt_user_id": "user-synthetic", "chatgpt_plan_type": "free"}}
	if edit != nil {
		edit(session, claims)
	}
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	session["accessToken"] = "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".c3ludGhldGljLXNpZ25hdHVyZQ"
	raw, err := json.Marshal(session)
	require.NoError(t, err)
	return string(raw)
}

func TestShopSessionPrecheckRejectsInvalidAndInconsistentContent(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any, map[string]any)
	}{
		{"missing email", func(s, _ map[string]any) { delete(s, "user") }},
		{"email display name", func(s, _ map[string]any) { s["user"] = map[string]any{"email": "Name <target@example.com>"} }},
		{"wrong account", func(s, _ map[string]any) { s["account"].(map[string]any)["id"] = "other" }},
		{"wrong user", func(s, _ map[string]any) { s["user"].(map[string]any)["id"] = "other" }},
		{"wrong email", func(s, _ map[string]any) { s["user"].(map[string]any)["email"] = "other@example.com" }},
		{"expired access token", func(_, c map[string]any) { c["exp"] = 1 }},
		{"missing token expiry", func(_, c map[string]any) { delete(c, "exp") }},
		{"expired session", func(s, _ map[string]any) { s["expires"] = "2000-01-01T00:00:00Z" }},
		{"invalid expiry", func(s, _ map[string]any) { s["expires"] = "invalid" }},
		{"future token", func(_, c map[string]any) { c["nbf"] = int64(4070908700) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := shopSessionFixture(t, tc.edit)
			_, err := validateShopInput(content, "session_json")
			require.Error(t, err)
			require.NotContains(t, err.Error(), "target@example.com")
			require.NotContains(t, err.Error(), "eyJ")
		})
	}
	for _, token := range []string{"opaque", "a.b.c", "eyJhbGciOiJub25lIn0.e30.c2ln", "eyJhbGciOiJSUzI1NiJ9.bnVsbA.c2ln"} {
		_, err := validateShopInput(`{"user":{"email":"target@example.com"},"accessToken":"`+token+`"}`, "session_json")
		require.Error(t, err)
	}
}

func TestShopSessionPrecheckReturnsOnlyOfflineHints(t *testing.T) {
	content := shopSessionFixture(t, func(s, c map[string]any) {
		c["exp"] = time.Now().Add(time.Hour).Unix()
		s["WARNING_BANNER"] = "Untrusted instructions must remain inert"
	})
	check, err := inspectShopSession(content, time.Now())
	require.NoError(t, err)
	require.Equal(t, "target@example.com", check.Account)
	require.Equal(t, "free", check.PlanType)
	require.WithinDuration(t, time.Now().Add(time.Hour), *check.ExpiresAt, 2*time.Second)
	raw, err := json.Marshal(check)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "eyJ")
	require.NotContains(t, string(raw), "workspace-synthetic")
}

func TestShopSessionUnknownOrMismatchedPlanDoesNotInventEligibility(t *testing.T) {
	content := shopSessionFixture(t, func(s, _ map[string]any) { s["account"].(map[string]any)["planType"] = "future-plan" })
	check, err := inspectShopSession(content, time.Now())
	require.NoError(t, err)
	require.Equal(t, "unknown", check.PlanType)
	require.Len(t, check.Warnings, 2)
}

func TestShopSessionCheckIsPrivateAndDoesNotQueueFulfillment(t *testing.T) {
	s, _, o, u := newShopUnit(t, ShopSessionTopup, true)
	ctx := context.Background()
	d, err := s.Order(ctx, o.ID, u.ID, false)
	require.NoError(t, err)
	_, err = s.CheckInput(ctx, o.ID, u.ID+1, d.Revision, shopSessionFixture(t, nil))
	require.Error(t, err)
	_, err = s.CheckInput(ctx, o.ID, u.ID, d.Revision-1, shopSessionFixture(t, nil))
	require.Error(t, err)
	check, err := s.CheckInput(ctx, o.ID, u.ID, d.Revision, shopSessionFixture(t, nil))
	require.NoError(t, err)
	require.Equal(t, "free", check.PlanType)
	after, err := s.Order(ctx, o.ID, u.ID, false)
	require.NoError(t, err)
	require.Equal(t, d.Revision, after.Revision)
	require.Equal(t, ShopWaitingInput, after.FulfillmentStatus)
	d, err = s.SubmitInput(ctx, o.ID, u.ID, d.Revision, shopSessionFixture(t, nil))
	require.NoError(t, err)
	serialized, _ := json.Marshal(d)
	require.Contains(t, string(serialized), `"plan_type":"free"`)
	require.False(t, strings.Contains(string(serialized), "eyJ"))
}

func TestShopCompletionCanRecordWorkAfterSubmittedTokenExpires(t *testing.T) {
	s, _, o, u := newShopUnit(t, ShopSessionTopup, true)
	ctx := context.Background()
	d, err := s.Order(ctx, o.ID, u.ID, false)
	require.NoError(t, err)
	d, err = s.SubmitInput(ctx, o.ID, u.ID, d.Revision, shopSessionFixture(t, nil))
	require.NoError(t, err)
	// Model time passing after the original valid submission, without a sleep.
	content := shopSessionFixture(t, func(_, c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() })
	ciphertext, err := s.encrypt(o.ID, "input", d.InputRevision, content)
	require.NoError(t, err)
	s.client.ShopFulfillment.Update().Where(shopfulfillment.OrderIDEQ(o.ID)).SetInputCiphertext(ciphertext).SetInputSubmittedAt(time.Now().Add(-2 * time.Hour)).ExecX(ctx)
	d, err = s.Action(ctx, o.ID, 9001, ShopActionInput{Action: "complete", Revision: d.Revision, InputRevision: d.InputRevision})
	require.NoError(t, err)
	require.Equal(t, ShopCompleted, d.FulfillmentStatus)
}

func TestShopSessionInvalidTimestampNeverPersistsInput(t *testing.T) {
	for _, field := range []string{"exp", "nbf"} {
		for _, withSessionExpiry := range []bool{true, false} {
			t.Run(field+"/"+map[bool]string{true: "with expiry", false: "without expiry"}[withSessionExpiry], func(t *testing.T) {
				s, _, o, u := newShopUnit(t, ShopSessionTopup, true)
				ctx := context.Background()
				d, err := s.Order(ctx, o.ID, u.ID, false)
				require.NoError(t, err)
				content := shopSessionFixture(t, func(s, c map[string]any) {
					c[field] = int64(4070908800000)
					if !withSessionExpiry {
						delete(s, "expires")
					}
				})
				for _, operation := range []func() error{
					func() error { _, err := s.CheckInput(ctx, o.ID, u.ID, d.Revision, content); return err },
					func() error { _, err := s.SubmitInput(ctx, o.ID, u.ID, d.Revision, content); return err },
				} {
					err := operation()
					var appErr *infraerrors.ApplicationError
					require.ErrorAs(t, err, &appErr)
					require.EqualValues(t, http.StatusBadRequest, appErr.Code)
				}
				f := s.client.ShopFulfillment.Query().Where(shopfulfillment.OrderIDEQ(o.ID)).OnlyX(ctx)
				require.Empty(t, f.InputCiphertext)
				require.Equal(t, ShopWaitingInput, f.Status)
				require.Equal(t, d.Revision, f.Revision)
			})
		}
	}
}

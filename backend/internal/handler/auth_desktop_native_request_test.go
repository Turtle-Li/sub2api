//go:build unit

package handler

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDesktopNativeRequestsDoNotCarryBrowserCaptchaProofs(t *testing.T) {
	login := DesktopLoginRequest{Email: "user@example.com", Password: "secret-password"}
	loginPayload, err := json.Marshal(login)
	require.NoError(t, err)
	require.NotContains(t, string(loginPayload), "turnstile")
	require.NotContains(t, string(loginPayload), "captcha")

	register := DesktopRegisterRequest{
		Email:          "new@example.com",
		Password:       "secret-password",
		VerifyCode:     "123456",
		PromoCode:      "PROMO",
		InvitationCode: "INVITE",
	}
	registerPayload, err := json.Marshal(register)
	require.NoError(t, err)
	require.NotContains(t, string(registerPayload), "turnstile")
	require.NotContains(t, string(registerPayload), "captcha")
}

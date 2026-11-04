package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/mail"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ShopInputCheck contains only offline hints, never authenticated provider state.
type ShopInputCheck struct {
	Account   string     `json:"account"`
	PlanType  string     `json:"plan_type"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Warnings  []string   `json:"warnings"`
}

func inspectShopSession(content string, now time.Time) (*ShopInputCheck, error) {
	invalid := func() (*ShopInputCheck, error) {
		return nil, infraerrors.BadRequest("SHOP_SESSION_INVALID", "Session 内容不完整或令牌格式错误，请复制资料页面的全部 JSON 内容")
	}
	var session struct {
		AccessToken string                     `json:"accessToken"`
		Expires     string                     `json:"expires"`
		User        struct{ ID, Email string } `json:"user"`
		Account     struct {
			ID       string `json:"id"`
			PlanType string `json:"planType"`
		} `json:"account"`
	}
	if json.Unmarshal([]byte(content), &session) != nil || session.AccessToken == "" || len(session.User.Email) > 320 {
		return invalid()
	}
	address, err := mail.ParseAddress(session.User.Email)
	if err != nil || address.Address != session.User.Email {
		return nil, infraerrors.BadRequest("SHOP_SESSION_ACCOUNT_MISSING", "Session 缺少有效的账号邮箱，请先登录要充值的账号后重新复制")
	}
	parts := strings.Split(session.AccessToken, ".")
	if len(parts) != 3 {
		return invalid()
	}
	var header struct{ Alg string }
	var claims struct {
		Exp  int64 `json:"exp"`
		Nbf  int64 `json:"nbf"`
		Auth struct {
			AccountID string `json:"chatgpt_account_id"`
			UserID    string `json:"chatgpt_user_id"`
			PlanType  string `json:"chatgpt_plan_type"`
		} `json:"https://api.openai.com/auth"`
		Profile struct{ Email string } `json:"https://api.openai.com/profile"`
	}
	decode := func(part string, target any) bool {
		data, err := base64.RawURLEncoding.DecodeString(part)
		return err == nil && json.Unmarshal(data, target) == nil
	}
	if !decode(parts[0], &header) || header.Alg == "" || strings.EqualFold(header.Alg, "none") || !decode(parts[1], &claims) || claims.Exp <= 0 {
		return invalid()
	}
	// JWT NumericDate uses seconds. Reject millisecond/overflow values before
	// converting them to time.Time or returning an unserializable hint.
	const maxRFC3339Unix = int64(253402300799)
	if claims.Exp > maxRFC3339Unix || claims.Nbf < 0 || claims.Nbf > maxRFC3339Unix {
		return invalid()
	}
	if signature, err := base64.RawURLEncoding.DecodeString(parts[2]); err != nil || len(signature) == 0 {
		return invalid()
	}
	if (claims.Profile.Email != "" && !strings.EqualFold(claims.Profile.Email, session.User.Email)) ||
		(claims.Auth.AccountID != "" && session.Account.ID != "" && claims.Auth.AccountID != session.Account.ID) ||
		(claims.Auth.UserID != "" && session.User.ID != "" && claims.Auth.UserID != session.User.ID) {
		return nil, infraerrors.BadRequest("SHOP_SESSION_ACCOUNT_MISMATCH", "Session 中的账号信息不一致，请退出其他账号后重新登录并复制")
	}
	expires := time.Unix(claims.Exp, 0).UTC()
	if session.Expires != "" {
		sessionExpiry, err := time.Parse(time.RFC3339Nano, session.Expires)
		if err != nil {
			return invalid()
		}
		if sessionExpiry.Before(expires) {
			expires = sessionExpiry
		}
	}
	if !expires.After(now) || (claims.Nbf > 0 && time.Unix(claims.Nbf, 0).After(now.Add(time.Minute))) {
		return nil, infraerrors.BadRequest("SHOP_SESSION_EXPIRED", "Session 已过期或尚未生效，请重新登录后复制")
	}
	check := &ShopInputCheck{Account: session.User.Email, PlanType: "unknown", ExpiresAt: &expires, Warnings: []string{}}
	plan := strings.ToLower(session.Account.PlanType)
	if plan == "" {
		plan = strings.ToLower(claims.Auth.PlanType)
	}
	switch plan {
	case "free", "plus", "pro", "team", "business", "enterprise", "edu", "go":
		check.PlanType = plan
	default:
		check.Warnings = append(check.Warnings, "未识别到订阅类型，需管理员人工核对。")
	}
	if session.Account.PlanType != "" && claims.Auth.PlanType != "" && !strings.EqualFold(session.Account.PlanType, claims.Auth.PlanType) {
		check.Warnings = append(check.Warnings, "Session 与令牌中的订阅信息不同，需管理员人工核对。")
	}
	return check, nil
}

func (s *ShopService) CheckInput(ctx context.Context, orderID, userID int64, revision int, content string) (*ShopInputCheck, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	detail, err := s.Order(ctx, orderID, userID, false)
	if err != nil {
		return nil, err
	}
	if !detail.CanSubmit || detail.Revision != revision {
		return nil, shopConflict()
	}
	if _, err := validateShopInput(content, detail.Product.InputFormat); err != nil {
		return nil, err
	}
	if detail.Product.InputFormat == "text" {
		return &ShopInputCheck{Warnings: []string{}}, nil
	}
	return inspectShopSession(content, time.Now())
}

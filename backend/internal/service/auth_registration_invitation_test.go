//go:build unit

package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

type registrationAffiliateRepoStub struct {
	AffiliateRepository
	validCode       string
	inviterID       int64
	boundUser       int64
	suppressBinding bool
}

type transactionalRegistrationAffiliateRepo struct {
	AffiliateRepository
	client    *dbent.Client
	code      string
	inviterID int64
}

func (r *transactionalRegistrationAffiliateRepo) clientFor(ctx context.Context) *dbent.Client {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return r.client
}

func (r *transactionalRegistrationAffiliateRepo) GetAffiliateByCode(_ context.Context, code string) (*AffiliateSummary, error) {
	if code != r.code {
		return nil, ErrAffiliateProfileNotFound
	}
	return &AffiliateSummary{UserID: r.inviterID, AffCode: r.code}, nil
}

func (r *transactionalRegistrationAffiliateRepo) EnsureUserAffiliate(ctx context.Context, userID int64) (*AffiliateSummary, error) {
	client := r.clientFor(ctx)
	if _, err := client.ExecContext(ctx, `
INSERT OR IGNORE INTO affiliate_registration_state (user_id, aff_code, aff_count)
VALUES (?, ?, 0)`, userID, fmt.Sprintf("SELF%d", userID)); err != nil {
		return nil, err
	}
	rows, err := client.QueryContext(ctx, `
SELECT aff_code, inviter_id, aff_count
FROM affiliate_registration_state
WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return nil, ErrAffiliateProfileNotFound
	}
	var summary AffiliateSummary
	var inviterID sql.NullInt64
	if err := rows.Scan(&summary.AffCode, &inviterID, &summary.AffCount); err != nil {
		return nil, err
	}
	summary.UserID = userID
	if inviterID.Valid {
		summary.InviterID = &inviterID.Int64
	}
	return &summary, rows.Err()
}

func (r *transactionalRegistrationAffiliateRepo) BindInviter(ctx context.Context, userID, inviterID int64) (bool, error) {
	client := r.clientFor(ctx)
	if _, err := client.ExecContext(ctx,
		"UPDATE affiliate_registration_state SET inviter_id = ? WHERE user_id = ?",
		inviterID, userID,
	); err != nil {
		return false, err
	}
	if _, err := client.ExecContext(ctx,
		"UPDATE affiliate_registration_state SET aff_count = aff_count + 1 WHERE user_id = ?",
		inviterID,
	); err != nil {
		return false, err
	}
	return false, errors.New("forced affiliate bind failure")
}

func (s *registrationAffiliateRepoStub) GetAffiliateByCode(_ context.Context, code string) (*AffiliateSummary, error) {
	if code != s.validCode {
		return nil, ErrAffiliateProfileNotFound
	}
	return &AffiliateSummary{UserID: s.inviterID, AffCode: code}, nil
}

func (s *registrationAffiliateRepoStub) EnsureUserAffiliate(_ context.Context, userID int64) (*AffiliateSummary, error) {
	summary := &AffiliateSummary{UserID: userID, AffCode: "SELF1234"}
	if userID == s.boundUser {
		summary.InviterID = &s.inviterID
	}
	return summary, nil
}

func (s *registrationAffiliateRepoStub) BindInviter(_ context.Context, userID, inviterID int64) (bool, error) {
	if inviterID != s.inviterID {
		return false, ErrAffiliateCodeInvalid
	}
	if s.suppressBinding {
		return true, nil
	}
	s.boundUser = userID
	return true, nil
}

func newAffiliateRegistrationTestService(t *testing.T) (*AuthService, *raceSafeUserRepo, *registrationAffiliateRepoStub) {
	t.Helper()
	userRepo := newRaceSafeUserRepo()
	redeemRepo := &raceSafeRedeemRepo{codes: map[string]*RedeemCode{}}
	settings := map[string]string{
		SettingKeyRegistrationEnabled:              "true",
		SettingKeyInvitationCodeEnabled:            "true",
		SettingKeyAffiliateEnabled:                 "true",
		SettingKeyAffiliateCodeRegistrationEnabled: "true",
	}
	authService := newOAuthEmailFlowAuthService(
		userRepo,
		redeemRepo,
		&refreshTokenCacheStub{},
		settings,
		nil,
		&userPlatformQuotaRepoStub{},
	)
	affiliateRepo := &registrationAffiliateRepoStub{validCode: "AFF12345", inviterID: 99}
	authService.affiliateService = NewAffiliateService(affiliateRepo, authService.settingService, nil, nil)
	return authService, userRepo, affiliateRepo
}

func TestRegisterWithManualAffiliateCodeBindsInviter(t *testing.T) {
	authService, _, affiliateRepo := newAffiliateRegistrationTestService(t)

	_, user, err := authService.RegisterWithVerification(
		context.Background(), "manual-aff@example.com", "Password123!", "", "", "AFF12345", "IGNORED1",
	)

	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, user.ID, affiliateRepo.boundUser)
}

func TestRegisterWithAffiliateFallbackBindsInviter(t *testing.T) {
	authService, _, affiliateRepo := newAffiliateRegistrationTestService(t)

	_, user, err := authService.RegisterWithVerification(
		context.Background(), "url-aff@example.com", "Password123!", "", "", "", "AFF12345",
	)

	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, user.ID, affiliateRepo.boundUser)
}

func TestRegisterWithRedeemCodeAlsoBindsAffiliateFallback(t *testing.T) {
	authService, _, affiliateRepo := newAffiliateRegistrationTestService(t)
	authService.redeemRepo = &raceSafeRedeemRepo{codes: map[string]*RedeemCode{
		"INVITE123": {ID: 7, Code: "INVITE123", Type: RedeemTypeInvitation, Status: StatusUnused},
	}}

	_, user, err := authService.RegisterWithVerification(
		context.Background(), "redeem-and-aff@example.com", "Password123!", "", "", "INVITE123", "AFF12345",
	)

	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, user.ID, affiliateRepo.boundUser)
}

func TestRegisterInvalidManualCodeDoesNotFallbackToAffiliateCode(t *testing.T) {
	authService, userRepo, affiliateRepo := newAffiliateRegistrationTestService(t)

	_, _, err := authService.RegisterWithVerification(
		context.Background(), "no-fallback@example.com", "Password123!", "", "", "INVALID1", "AFF12345",
	)

	require.ErrorIs(t, err, ErrInvitationCodeInvalid)
	require.Zero(t, affiliateRepo.boundUser)
	exists, existsErr := userRepo.ExistsByEmail(context.Background(), "no-fallback@example.com")
	require.NoError(t, existsErr)
	require.False(t, exists)
}

func TestApplyAffiliateRegistrationInvitationRequiresBoundInviter(t *testing.T) {
	authService, _, affiliateRepo := newAffiliateRegistrationTestService(t)
	affiliateRepo.suppressBinding = true

	err := authService.applyRegistrationInvitation(context.Background(), 123, &registrationInvitation{
		kind:          registrationInvitationAffiliate,
		affiliateCode: "AFF12345",
	})

	require.ErrorIs(t, err, ErrInvitationCodeInvalid)
}

func TestValidateUnknownManualInvitationPreservesNotFound(t *testing.T) {
	authService, _, _ := newAffiliateRegistrationTestService(t)

	_, err := authService.ValidateRegistrationInvitation(context.Background(), "UNKNOWN1", "manual")

	require.ErrorIs(t, err, ErrRedeemCodeNotFound)
}

func TestAffiliateRegistrationFailureRollsBackUserAndAffiliateState(t *testing.T) {
	dbName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := sql.Open("sqlite", "file:"+dbName+"?mode=memory&cache=shared&_fk=1")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	driver := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(driver)))
	t.Cleanup(func() { _ = client.Close() })
	_, err = db.Exec(`
CREATE TABLE affiliate_registration_state (
	user_id INTEGER PRIMARY KEY,
	aff_code TEXT NOT NULL UNIQUE,
	inviter_id INTEGER NULL,
	aff_count INTEGER NOT NULL DEFAULT 0
)`)
	require.NoError(t, err)

	ctx := context.Background()
	inviter, err := client.User.Create().
		SetEmail("inviter@example.com").
		SetPasswordHash("hash").
		SetRole(RoleUser).
		SetStatus(StatusActive).
		Save(ctx)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO affiliate_registration_state (user_id, aff_code, aff_count) VALUES (?, ?, 0)",
		inviter.ID, "AFF12345",
	)
	require.NoError(t, err)

	settings := NewSettingService(&settingRepoStub{values: map[string]string{
		SettingKeyAffiliateEnabled: "true",
	}}, &config.Config{})
	affiliateRepo := &transactionalRegistrationAffiliateRepo{
		client:    client,
		code:      "AFF12345",
		inviterID: inviter.ID,
	}
	authService := &AuthService{
		entClient:        client,
		affiliateService: NewAffiliateService(affiliateRepo, settings, nil, nil),
	}
	user := &User{
		Email:        "rollback@example.com",
		PasswordHash: "hash",
		Role:         RoleUser,
		Status:       StatusActive,
	}
	invitation := &registrationInvitation{
		kind:          registrationInvitationAffiliate,
		affiliateCode: "AFF12345",
	}

	err = authService.createUserAndApplyRegistrationInvitation(ctx, user, invitation, func(execCtx context.Context, user *User) error {
		execClient := client
		if tx := dbent.TxFromContext(execCtx); tx != nil {
			execClient = tx.Client()
		}
		created, createErr := execClient.User.Create().
			SetEmail(user.Email).
			SetPasswordHash(user.PasswordHash).
			SetRole(user.Role).
			SetStatus(user.Status).
			Save(execCtx)
		if createErr == nil {
			user.ID = created.ID
		}
		return createErr
	})
	require.ErrorIs(t, err, ErrInvitationCodeInvalid)

	exists, err := client.User.Query().Where(func(selector *entsql.Selector) {
		selector.Where(entsql.EQ(selector.C("email"), user.Email))
	}).Exist(ctx)
	require.NoError(t, err)
	require.False(t, exists)

	var inviteeProfiles int
	err = db.QueryRow("SELECT COUNT(*) FROM affiliate_registration_state WHERE user_id <> ?", inviter.ID).Scan(&inviteeProfiles)
	require.NoError(t, err)
	require.Zero(t, inviteeProfiles)

	var inviterCount int
	err = db.QueryRow("SELECT aff_count FROM affiliate_registration_state WHERE user_id = ?", inviter.ID).Scan(&inviterCount)
	require.NoError(t, err)
	require.Zero(t, inviterCount)
}

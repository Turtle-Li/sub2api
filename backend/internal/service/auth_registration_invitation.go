package service

import (
	"context"
	"errors"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type registrationInvitationKind uint8

const (
	registrationInvitationNone registrationInvitationKind = iota
	registrationInvitationRedeem
	registrationInvitationAffiliate
)

type registrationInvitation struct {
	kind          registrationInvitationKind
	redeemCode    *RedeemCode
	affiliateCode string
}

func (i *registrationInvitation) bindsAffiliate() bool {
	return i != nil && i.kind == registrationInvitationAffiliate
}

func (s *AuthService) ValidateRegistrationInvitation(ctx context.Context, code, source string) (string, error) {
	if s == nil || s.settingService == nil || s.redeemRepo == nil {
		return "", ErrServiceUnavailable
	}
	if strings.EqualFold(strings.TrimSpace(source), "affiliate") {
		invitation, err := s.resolveRegistrationInvitation(ctx, "", code)
		if err != nil {
			return "", err
		}
		if invitation == nil || invitation.kind != registrationInvitationAffiliate {
			return "", ErrInvitationCodeInvalid
		}
		return "affiliate", nil
	}

	code = strings.TrimSpace(code)
	redeemCode, err := s.redeemRepo.GetByCode(ctx, code)
	if err == nil {
		if redeemCode.Type != RedeemTypeInvitation {
			return "", ErrInvitationCodeInvalid
		}
		if redeemCode.Status != StatusUnused {
			return "", ErrRedeemCodeUsed
		}
		if !redeemCode.CanUse() {
			return "", ErrInvitationCodeInvalid
		}
		return "invitation", nil
	}
	if !errors.Is(err, ErrRedeemCodeNotFound) {
		return "", err
	}
	if !s.settingService.IsAffiliateCodeRegistrationEnabled(ctx) || s.affiliateService == nil {
		return "", ErrRedeemCodeNotFound
	}
	if _, err := s.affiliateService.ValidateAffiliateCode(ctx, code); err != nil {
		if errors.Is(err, ErrAffiliateCodeInvalid) {
			return "", ErrRedeemCodeNotFound
		}
		return "", err
	}
	return "affiliate", nil
}

func (s *AuthService) resolveRegistrationInvitation(ctx context.Context, manualCode, affiliateCode string) (*registrationInvitation, error) {
	if s == nil || s.settingService == nil || !s.settingService.IsInvitationCodeEnabled(ctx) {
		return nil, nil
	}

	manualCode = strings.TrimSpace(manualCode)
	affiliateCode = strings.TrimSpace(affiliateCode)
	allowAffiliate := s.settingService.IsAffiliateCodeRegistrationEnabled(ctx)

	if manualCode != "" {
		if s.redeemRepo == nil {
			return nil, ErrServiceUnavailable
		}
		redeemCode, err := s.redeemRepo.GetByCode(ctx, manualCode)
		switch {
		case err == nil:
			if redeemCode.Type != RedeemTypeInvitation || !redeemCode.CanUse() {
				return nil, ErrInvitationCodeInvalid
			}
			return &registrationInvitation{kind: registrationInvitationRedeem, redeemCode: redeemCode}, nil
		case !errors.Is(err, ErrRedeemCodeNotFound):
			return nil, ErrServiceUnavailable
		}

		if !allowAffiliate || s.affiliateService == nil {
			return nil, ErrInvitationCodeInvalid
		}
		if _, err := s.affiliateService.ValidateAffiliateCode(ctx, manualCode); err != nil {
			if errors.Is(err, ErrAffiliateCodeInvalid) {
				return nil, ErrInvitationCodeInvalid
			}
			return nil, ErrServiceUnavailable
		}
		return &registrationInvitation{kind: registrationInvitationAffiliate, affiliateCode: manualCode}, nil
	}

	if affiliateCode == "" {
		return nil, ErrInvitationCodeRequired
	}
	if !allowAffiliate || s.affiliateService == nil {
		return nil, ErrInvitationCodeRequired
	}
	if _, err := s.affiliateService.ValidateAffiliateCode(ctx, affiliateCode); err != nil {
		if errors.Is(err, ErrAffiliateCodeInvalid) {
			return nil, ErrInvitationCodeInvalid
		}
		return nil, ErrServiceUnavailable
	}
	return &registrationInvitation{kind: registrationInvitationAffiliate, affiliateCode: affiliateCode}, nil
}

func (s *AuthService) applyRegistrationInvitation(ctx context.Context, userID int64, invitation *registrationInvitation) error {
	if invitation == nil {
		return nil
	}
	switch invitation.kind {
	case registrationInvitationRedeem:
		if invitation.redeemCode == nil || s.redeemRepo == nil {
			return ErrServiceUnavailable
		}
		if err := s.redeemRepo.Use(ctx, invitation.redeemCode.ID, userID); err != nil {
			return ErrInvitationCodeInvalid
		}
	case registrationInvitationAffiliate:
		if s.affiliateService == nil {
			return ErrServiceUnavailable
		}
		inviter, err := s.affiliateService.ValidateAffiliateCode(ctx, invitation.affiliateCode)
		if err != nil {
			return ErrInvitationCodeInvalid
		}
		if err := s.affiliateService.BindInviterByCode(ctx, userID, invitation.affiliateCode); err != nil {
			return ErrInvitationCodeInvalid
		}
		invitee, err := s.affiliateService.EnsureUserAffiliate(ctx, userID)
		if err != nil || invitee == nil || invitee.InviterID == nil || *invitee.InviterID != inviter.UserID {
			return ErrInvitationCodeInvalid
		}
	}
	return nil
}

func (s *AuthService) createUserAndApplyRegistrationInvitation(ctx context.Context, user *User, invitation *registrationInvitation, create func(context.Context, *User) error) error {
	commit := func(execCtx context.Context) error {
		if err := create(execCtx, user); err != nil {
			return err
		}
		return s.applyRegistrationInvitation(execCtx, user.ID, invitation)
	}
	if invitation == nil || s.entClient == nil {
		return commit(ctx)
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return ErrServiceUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	if err := commit(txCtx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "failed to commit registration").WithCause(err)
	}
	return nil
}

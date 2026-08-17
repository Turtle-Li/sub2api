package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/redis/go-redis/v9"
)

// DesktopAuthService implements the server-side half of the OAuth device
// authorization flow used by TT Switch. The browser only receives a short
// user code; the high-entropy device code and its PKCE verifier stay in the
// desktop process. Redis stores only hashes of both codes.
type DesktopAuthService struct {
	redis *redis.Client
}

const (
	desktopAuthTTL          = 5 * time.Minute
	desktopAuthPollInterval = 5
	desktopAuthCodeLength   = 8
	desktopAuthMaxAttempts  = 8

	desktopAuthGrantKeyPrefix = "desktop-auth:v1:{grant}:"
	desktopAuthUserKeyPrefix  = "desktop-auth:v1:{grant}:user:"
)

var (
	ErrDesktopAuthUnavailable = infraerrors.ServiceUnavailable(
		"DESKTOP_AUTH_UNAVAILABLE",
		"desktop authorization is temporarily unavailable",
	)
	ErrDesktopAuthInvalidRequest = infraerrors.BadRequest(
		"INVALID_DESKTOP_AUTH_REQUEST",
		"invalid desktop authorization request",
	)
)

var desktopAuthStartScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 or redis.call('EXISTS', KEYS[2]) == 1 then
  return 0
end
redis.call('HSET', KEYS[1],
  'code_challenge', ARGV[1],
  'user_code_hash', ARGV[2],
  'status', 'pending',
  'user_id', '0')
redis.call('EXPIRE', KEYS[1], ARGV[3])
redis.call('SET', KEYS[2], ARGV[4], 'EX', ARGV[3])
return 1
`)

var desktopAuthApproveScript = redis.NewScript(`
local indexed_device_hash = redis.call('GET', KEYS[1])
if not indexed_device_hash or indexed_device_hash ~= ARGV[1] then
  return 'expired'
end
local status = redis.call('HGET', KEYS[2], 'status')
if not status then
  redis.call('DEL', KEYS[1])
  return 'expired'
end
if status == 'pending' then
  redis.call('HSET', KEYS[2], 'status', 'approved', 'user_id', ARGV[2])
  return 'approved'
end
if status == 'approved' then
  local approved_user_id = redis.call('HGET', KEYS[2], 'user_id')
  if approved_user_id == ARGV[2] then
    return 'approved'
  end
  return 'denied'
end
return 'denied'
`)

var desktopAuthConsumeScript = redis.NewScript(`
local status = redis.call('HGET', KEYS[1], 'status')
if not status then
  return { 'expired', '0' }
end
if status == 'pending' then
  return { 'pending', '0' }
end
if status == 'approved' then
  local user_id = redis.call('HGET', KEYS[1], 'user_id')
  if not user_id or user_id == '0' then
    return { 'denied', '0' }
  end
  redis.call('HSET', KEYS[1], 'status', 'consumed')
  return { 'authenticated', user_id }
end
return { 'denied', '0' }
`)

// DesktopAuthStart is the confidential result returned to an initiating
// desktop. DeviceCode must never be sent to a browser or written to logs.
type DesktopAuthStart struct {
	DeviceCode string
	UserCode   string
	ExpiresIn  int
	Interval   int
}

// DesktopAuthResult represents a poll or approval state. UserID is only set
// for the one successful device-code consumption.
type DesktopAuthResult struct {
	Status string
	UserID int64
}

func NewDesktopAuthService(redisClient *redis.Client) *DesktopAuthService {
	return &DesktopAuthService{redis: redisClient}
}

func (s *DesktopAuthService) Start(ctx context.Context, codeChallenge string) (DesktopAuthStart, error) {
	if s == nil || s.redis == nil {
		return DesktopAuthStart{}, ErrDesktopAuthUnavailable
	}
	codeChallenge = strings.TrimSpace(codeChallenge)
	if !isPKCEValue(codeChallenge) {
		return DesktopAuthStart{}, ErrDesktopAuthInvalidRequest
	}

	for attempt := 0; attempt < desktopAuthMaxAttempts; attempt++ {
		deviceCode, err := randomURLSafeCode(32)
		if err != nil {
			return DesktopAuthStart{}, ErrDesktopAuthUnavailable
		}
		userCode, err := randomUserCode()
		if err != nil {
			return DesktopAuthStart{}, ErrDesktopAuthUnavailable
		}
		deviceHash := desktopAuthHash(deviceCode)
		userHash := desktopAuthHash(normalizeDesktopAuthUserCode(userCode))
		created, err := desktopAuthStartScript.Run(
			ctx,
			s.redis,
			[]string{desktopAuthGrantKey(deviceHash), desktopAuthUserKey(userHash)},
			codeChallenge,
			userHash,
			int(desktopAuthTTL.Seconds()),
			deviceHash,
		).Int()
		if err != nil {
			return DesktopAuthStart{}, ErrDesktopAuthUnavailable
		}
		if created == 1 {
			return DesktopAuthStart{
				DeviceCode: deviceCode,
				UserCode:   userCode,
				ExpiresIn:  int(desktopAuthTTL.Seconds()),
				Interval:   desktopAuthPollInterval,
			}, nil
		}
	}

	return DesktopAuthStart{}, ErrDesktopAuthUnavailable
}

// Approve binds the authenticated browser user to the pending grant. Repeating
// the approval is idempotent for the same user but never transfers a grant to
// a different account.
func (s *DesktopAuthService) Approve(ctx context.Context, userCode string, userID int64) (DesktopAuthResult, error) {
	if s == nil || s.redis == nil {
		return DesktopAuthResult{}, ErrDesktopAuthUnavailable
	}
	if userID <= 0 {
		return DesktopAuthResult{}, ErrDesktopAuthInvalidRequest
	}
	userCode = normalizeDesktopAuthUserCode(userCode)
	if !isDesktopAuthUserCode(userCode) {
		return DesktopAuthResult{}, ErrDesktopAuthInvalidRequest
	}

	userHash := desktopAuthHash(userCode)
	deviceHash, err := s.redis.Get(ctx, desktopAuthUserKey(userHash)).Result()
	if errors.Is(err, redis.Nil) {
		return DesktopAuthResult{Status: "expired"}, nil
	}
	if err != nil || !isDesktopAuthHash(deviceHash) {
		return DesktopAuthResult{}, ErrDesktopAuthUnavailable
	}

	status, err := desktopAuthApproveScript.Run(
		ctx,
		s.redis,
		[]string{desktopAuthUserKey(userHash), desktopAuthGrantKey(deviceHash)},
		deviceHash,
		userID,
	).Text()
	if err != nil {
		return DesktopAuthResult{}, ErrDesktopAuthUnavailable
	}
	status = normalizeDesktopAuthStatus(status)
	if status == "denied" {
		return DesktopAuthResult{}, ErrDesktopAuthInvalidRequest
	}
	return DesktopAuthResult{Status: status}, nil
}

// Consume checks PKCE and atomically consumes an approved grant. A successful
// result can be used exactly once to issue a normal browser-independent token
// pair for the desktop's current IP/User-Agent session binding.
func (s *DesktopAuthService) Consume(ctx context.Context, deviceCode, codeVerifier string) (DesktopAuthResult, error) {
	if s == nil || s.redis == nil {
		return DesktopAuthResult{}, ErrDesktopAuthUnavailable
	}
	deviceCode = strings.TrimSpace(deviceCode)
	codeVerifier = strings.TrimSpace(codeVerifier)
	if !isDeviceCode(deviceCode) || !isPKCEValue(codeVerifier) {
		return DesktopAuthResult{Status: "denied"}, nil
	}

	deviceHash := desktopAuthHash(deviceCode)
	grantKey := desktopAuthGrantKey(deviceHash)
	challenge, err := s.redis.HGet(ctx, grantKey, "code_challenge").Result()
	if errors.Is(err, redis.Nil) {
		return DesktopAuthResult{Status: "expired"}, nil
	}
	if err != nil {
		return DesktopAuthResult{}, ErrDesktopAuthUnavailable
	}
	if subtle.ConstantTimeCompare([]byte(challenge), []byte(pkceChallenge(codeVerifier))) != 1 {
		return DesktopAuthResult{Status: "denied"}, nil
	}

	values, err := desktopAuthConsumeScript.Run(
		ctx,
		s.redis,
		[]string{grantKey},
	).StringSlice()
	if err != nil || len(values) != 2 {
		return DesktopAuthResult{}, ErrDesktopAuthUnavailable
	}
	status := normalizeDesktopAuthStatus(values[0])
	if status != "authenticated" {
		return DesktopAuthResult{Status: status}, nil
	}
	var userID int64
	for _, digit := range values[1] {
		if digit < '0' || digit > '9' {
			return DesktopAuthResult{Status: "denied"}, nil
		}
		userID = userID*10 + int64(digit-'0')
	}
	if userID <= 0 {
		return DesktopAuthResult{Status: "denied"}, nil
	}
	return DesktopAuthResult{Status: status, UserID: userID}, nil
}

func desktopAuthGrantKey(deviceHash string) string {
	return desktopAuthGrantKeyPrefix + deviceHash
}

func desktopAuthUserKey(userHash string) string {
	return desktopAuthUserKeyPrefix + userHash
}

func desktopAuthHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomURLSafeCode(byteLength int) (string, error) {
	bytes := make([]byte, byteLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func randomUserCode() (string, error) {
	// This alphabet has 32 characters, so masking each random byte produces an
	// unbiased character and avoids visually ambiguous 0/O/1/I combinations.
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	bytes := make([]byte, desktopAuthCodeLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	characters := make([]byte, desktopAuthCodeLength)
	for index, value := range bytes {
		characters[index] = alphabet[int(value)&31]
	}
	return string(characters[:4]) + "-" + string(characters[4:]), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func isPKCEValue(value string) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func isDeviceCode(value string) bool {
	return len(value) == 43 && isPKCEValue(value)
}

func normalizeDesktopAuthUserCode(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) == desktopAuthCodeLength+1 && value[4] == '-' {
		return value[:4] + value[5:]
	}
	return value
}

func isDesktopAuthUserCode(value string) bool {
	if len(value) != desktopAuthCodeLength {
		return false
	}
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for _, character := range value {
		if !strings.ContainsRune(alphabet, character) {
			return false
		}
	}
	return true
}

func isDesktopAuthHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') {
			continue
		}
		return false
	}
	return true
}

func normalizeDesktopAuthStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "pending", "approved", "authenticated", "expired", "denied":
		return strings.TrimSpace(status)
	default:
		return "denied"
	}
}

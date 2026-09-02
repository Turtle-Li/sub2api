package unifiedpay

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maximumWebhookBodyBytes = 256 * 1024

type webhookVerifier struct {
	environment    Environment
	organizationID string
	productID      string
	appID          string
	publicKeys     map[string]ed25519.PublicKey
	now            func() time.Time
}

func newWebhookVerifier(config Config) (*webhookVerifier, error) {
	if !validUUID(config.OrganizationID) || !validUUID(config.ProductID) || !validIdentifier(config.AppID, 8, 80) ||
		(config.Environment != EnvironmentSandbox && config.Environment != EnvironmentLive) ||
		len(config.WebhookPublicKeys) == 0 || len(config.WebhookPublicKeys) > 5 {
		return nil, ErrInvalidConfiguration
	}
	keys := make(map[string]ed25519.PublicKey, len(config.WebhookPublicKeys))
	for keyID, key := range config.WebhookPublicKeys {
		if !validIdentifier(keyID, 8, 80) || len(key) != ed25519.PublicKeySize {
			return nil, ErrInvalidConfiguration
		}
		keys[keyID] = bytes.Clone(key)
	}
	now := config.Clock
	if now == nil {
		now = time.Now
	}
	return &webhookVerifier{
		environment: config.Environment, organizationID: config.OrganizationID,
		productID: config.ProductID, appID: config.AppID, publicKeys: keys, now: now,
	}, nil
}

func (v *webhookVerifier) verify(header http.Header, rawBody []byte) (VerifiedWebhook, error) {
	if v == nil || len(rawBody) == 0 || len(rawBody) > maximumWebhookBodyBytes {
		return VerifiedWebhook{}, ErrInvalidWebhookEvent
	}
	if err := requireJSONContentType(header); err != nil {
		return VerifiedWebhook{}, err
	}
	keyID, err := requiredWebhookHeader(header, HeaderWebhookKeyID)
	if err != nil || !validIdentifier(keyID, 8, 80) {
		if err != nil {
			return VerifiedWebhook{}, err
		}
		return VerifiedWebhook{}, ErrInvalidWebhookHeader
	}
	timestampText, err := requiredWebhookHeader(header, HeaderWebhookTimestamp)
	if err != nil {
		return VerifiedWebhook{}, err
	}
	timestampSeconds, err := parsePositiveInt64(timestampText)
	if err != nil {
		return VerifiedWebhook{}, ErrInvalidWebhookHeader
	}
	timestamp := time.Unix(timestampSeconds, 0).UTC()
	delta := timestamp.Unix() - v.now().UTC().Unix()
	if delta < -int64(MaximumClockSkew/time.Second) || delta > int64(MaximumClockSkew/time.Second) {
		return VerifiedWebhook{}, ErrWebhookTimestampWindow
	}
	eventID, err := requiredWebhookHeader(header, HeaderWebhookEventID)
	if err != nil || !validUUID(eventID) {
		if err != nil {
			return VerifiedWebhook{}, err
		}
		return VerifiedWebhook{}, ErrInvalidWebhookHeader
	}
	signatureText, err := requiredWebhookHeader(header, HeaderWebhookSignature)
	if err != nil {
		return VerifiedWebhook{}, err
	}
	publicKey, ok := v.publicKeys[keyID]
	if !ok {
		return VerifiedWebhook{}, ErrInvalidWebhookSignature
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(signatureText)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(publicKey, []byte(WebhookSignaturePayload(keyID, timestampSeconds, eventID, rawBody)), signature) {
		return VerifiedWebhook{}, ErrInvalidWebhookSignature
	}
	var event WebhookEvent
	if err := strictUnmarshalObject(rawBody, &event, true); err != nil {
		return VerifiedWebhook{}, err
	}
	if !validWebhookEvent(event) {
		return VerifiedWebhook{}, ErrInvalidWebhookEvent
	}
	if event.EventID != eventID {
		return VerifiedWebhook{}, ErrWebhookEventIDMismatch
	}
	if event.Environment != v.environment || event.OrganizationID != v.organizationID ||
		event.ProductID != v.productID || event.AppID != v.appID {
		return VerifiedWebhook{}, ErrWebhookScopeMismatch
	}
	bodyHash := sha256.Sum256(rawBody)
	return VerifiedWebhook{
		KeyID: keyID, Timestamp: timestamp, EventID: eventID, Sequence: event.Sequence,
		BodyHash: hex.EncodeToString(bodyHash[:]), Event: event,
	}, nil
}

// WebhookSignaturePayload is the exact four-line payment-webhook.v1 input.
func WebhookSignaturePayload(keyID string, timestamp int64, eventID string, rawBody []byte) string {
	bodyHash := sha256.Sum256(rawBody)
	return keyID + "\n" + strconv.FormatInt(timestamp, 10) + "\n" + eventID + "\n" + hex.EncodeToString(bodyHash[:])
}

func requiredWebhookHeader(header http.Header, name string) (string, error) {
	value, present, err := singleHeader(header, name)
	if err != nil {
		return "", err
	}
	if !present || value == "" {
		return "", ErrMissingWebhookHeader
	}
	return value, nil
}

func singleHeader(header http.Header, name string) (string, bool, error) {
	var value string
	count := 0
	matched := false
	for headerName, values := range header {
		if !strings.EqualFold(headerName, name) {
			continue
		}
		matched = true
		for _, candidate := range values {
			count++
			value = candidate
		}
	}
	if !matched {
		return "", false, nil
	}
	if count != 1 {
		return "", true, ErrDuplicateWebhookHeader
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e || value[i] == ',' {
			return "", true, ErrInvalidWebhookHeader
		}
	}
	return value, true, nil
}

func requireJSONContentType(header http.Header) error {
	value, present, err := singleHeader(header, "Content-Type")
	if err != nil || !present || value == "" {
		return ErrInvalidWebhookHeader
	}
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return ErrInvalidWebhookHeader
	}
	for name, value := range parameters {
		if strings.EqualFold(name, "charset") && strings.EqualFold(value, "utf-8") {
			continue
		}
		return ErrInvalidWebhookHeader
	}
	return nil
}

func parsePositiveInt64(value string) (int64, error) {
	if value == "" {
		return 0, ErrInvalidWebhookHeader
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, ErrInvalidWebhookHeader
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, ErrInvalidWebhookHeader
	}
	return parsed, nil
}

func validWebhookEvent(event WebhookEvent) bool {
	if event.SchemaVersion != "payment-webhook.v1" || !validUUID(event.EventID) || !validWebhookEventType(event.EventType) ||
		event.OccurredAt.IsZero() || (event.Environment != EnvironmentSandbox && event.Environment != EnvironmentLive) ||
		!validUUID(event.OrganizationID) || !validUUID(event.ProductID) || !validLowerIdentifier(event.ProductCode, 3, 48) ||
		!validIdentifier(event.AppID, 8, 80) || event.Sequence < 1 || !validIdentifier(event.OriginRequestID, 16, 80) ||
		!validPaymentOrderResource(event.Resource) {
		return false
	}
	if event.EventType == EventRefundSucceeded || event.EventType == EventRefundFailed {
		return event.Refund != nil && validRefundResource(*event.Refund)
	}
	return event.Refund == nil || validRefundResource(*event.Refund)
}

func validWebhookEventType(value string) bool {
	switch value {
	case EventPaymentPaid, EventPaymentPaidAfterClose, EventPaymentConfirmationPending,
		EventPaymentClosed, EventPaymentExpired, EventRefundSucceeded, EventRefundFailed:
		return true
	default:
		return false
	}
}

func validPaymentOrderResource(resource PaymentOrderResource) bool {
	if !validUUID(resource.PaymentOrderID) || !validIdentifier(resource.ProductOrderNo, 6, 64) ||
		!validLowerIdentifier(resource.OrderType, 3, 64) || resource.AmountFen < 1 || resource.PaidAmountFen < 0 ||
		resource.RefundedAmountFen < 0 || resource.ReservedRefundAmountFen < 0 || resource.RefundableAmountFen < 0 ||
		resource.Currency != "CNY" || resource.PaymentMethod != PaymentMethodAlipay ||
		len(resource.ChannelOutTradeNo) < 8 || len(resource.ChannelOutTradeNo) > 64 {
		return false
	}
	switch resource.Status {
	case StatusConfirmationPending, StatusPaid, StatusPaidAfterClose, StatusClosed, StatusExpired,
		StatusPartiallyRefunded, StatusRefunded:
		return true
	default:
		return false
	}
}

func validRefundResource(resource WebhookRefundResource) bool {
	return validUUID(resource.RefundRequestID) && validIdentifier(resource.ProductRefundNo, 6, 64) &&
		len(resource.ChannelOutRefundNo) >= 9 && len(resource.ChannelOutRefundNo) <= 64 &&
		resource.AmountFen >= 1 && resource.PaymentMethod == PaymentMethodAlipay &&
		(resource.Status == RefundStatusSucceeded || resource.Status == RefundStatusFailed)
}

package unifiedpay

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRefundWebhookEvidenceFieldsAreBounded(t *testing.T) {
	valid := WebhookRefundResource{RefundRequestID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", ProductRefundNo: "sub2-refund-001", ChannelOutRefundNo: "sandbox_sub2_refund_001", AmountFen: 1, PaymentMethod: PaymentMethodWechatPay, Status: RefundStatusFailed}
	require.True(t, validRefundResource(valid))
	for _, field := range []string{"provider id", "provider status", "failure code", "completion time", "channel id"} {
		t.Run(field, func(t *testing.T) {
			r := valid
			switch field {
			case "provider id":
				value := strings.Repeat("a", 161)
				r.ProviderRefundID = &value
			case "provider status":
				value := "FAILED\nprivate"
				r.ProviderStatus = &value
			case "failure code":
				value := "unbounded failure explanation"
				r.FailureCode = &value
			case "completion time":
				value := time.Time{}
				r.CompletedAt = &value
			case "channel id":
				r.ChannelOutRefundNo = "refund\ninvalid"
			}
			require.False(t, validRefundResource(r))
		})
	}
}

package service

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const OpsAlertCustomMessageMaxRunes = 500

func NormalizeOpsAlertEmails(values []string) ([]string, error) {
	if len(values) > 20 {
		return nil, fmt.Errorf("notify_emails must contain at most 20 addresses")
	}
	addresses := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		address, err := parseOpsAlertEmail(value)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(address)
		if !seen[key] {
			seen[key] = true
			addresses = append(addresses, address)
		}
	}
	return addresses, nil
}

func parseOpsAlertEmail(value string) (string, error) {
	if strings.ContainsFunc(value, unicode.IsControl) || len(value) > 254 {
		return "", fmt.Errorf("notify_emails contains an invalid address")
	}
	value = strings.TrimSpace(value)
	address, err := mail.ParseAddress(value)
	if err != nil || address.Name != "" || address.Address != value {
		return "", fmt.Errorf("notify_emails contains an invalid address")
	}
	return address.Address, nil
}

var (
	opsAlertSensitiveLabel = regexp.MustCompile(`(?i)(?:\b(?:api[_ -]?key|authorization|bearer|token|password|proxy[_ -]?url|prompt|user(?:[_ -]?id|name|[_ -]?email))\b|密钥|令牌|密码|提示词|用户(?:名|ID|邮箱)|代理地址)\s*[:=：]\s*[^\r\n]*`)
	opsAlertSensitiveText  = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*://[^\s]+|www\.[^\s]+|\b[\w.+-]+@[\w.-]+\.[a-z]{2,}\b|\bBearer\s+[^\s]+|\b(?:sk[-_]|rk[-_]|gh[opstu]_)[a-z0-9_-]{6,}\b|\beyJ[a-z0-9_-]+\.[a-z0-9_.-]+\b|\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b|\b[a-z0-9_-]{32,}\b)`)
)

// OpsAlertSafeNotificationText is only for administrator-supplied text, not
// upstream error text. Strip URLs, credentials, common identifiers and control
// characters before constructing a plain-text outbound notification.
func OpsAlertSafeNotificationText(text string, limit int) string {
	text = strings.Map(func(ch rune) rune {
		if unicode.Is(unicode.Cf, ch) || (unicode.IsControl(ch) && ch != '\n' && ch != '\r') {
			return -1
		}
		return ch
	}, text)
	text = opsAlertSensitiveLabel.ReplaceAllString(text, "[redacted]")
	text = opsAlertSensitiveText.ReplaceAllString(text, "[redacted]")
	runes := []rune(strings.Join(strings.Fields(text), " "))
	return string(runes[:min(len(runes), limit)])
}

var opsAlertBeijing = time.FixedZone("UTC+08:00", 8*60*60)

func buildOpsAlertFeishuText(rule *OpsAlertRule, event *OpsAlertEvent) string {
	severity := event.Severity
	switch severity {
	case "P0", "P1", "P2", "P3":
	default:
		severity = "unknown"
	}
	text := []string{
		fmt.Sprintf("[Ops 告警][%s] %s", severity, OpsAlertSafeNotificationText(rule.Name, 100)),
		fmt.Sprintf("北京时间：%s · 事件 #%d", event.FiredAt.In(opsAlertBeijing).Format("2006-01-02 15:04:05"), event.ID),
	}
	metric := rule.MetricType
	if !IsValidProviderErrorCode(metric) {
		metric = "unknown"
	}
	if event.MetricValue != nil {
		text = append(text, fmt.Sprintf("指标：%s · 数值：%g", metric, *event.MetricValue))
	} else {
		text = append(text, "指标："+metric)
	}
	if id := opsAlertDimensionInt(event.Dimensions["latest_account_id"]); id > 0 {
		name, _ := event.Dimensions["latest_account_name"].(string)
		if name = OpsAlertSafeNotificationText(name, 80); name != "" {
			text = append(text, fmt.Sprintf("账号：%s (#%d)", name, id))
		} else {
			text = append(text, fmt.Sprintf("账号：#%d", id))
		}
	}
	if status := opsAlertDimensionInt(event.Dimensions["latest_upstream_status_code"]); status >= 400 && status <= 599 {
		text = append(text, fmt.Sprintf("上游状态：%d", status))
	}
	if code, ok := event.Dimensions["latest_provider_code"].(string); ok && IsValidProviderErrorCode(code) {
		// The grammar is necessary for provider codes; scrub credential-shaped
		// values too in case a provider misuses the field.
		if safe := OpsAlertSafeNotificationText(code, 64); safe == code {
			text = append(text, "供应商错误码："+safe)
		}
	}
	if custom := OpsAlertSafeNotificationText(rule.CustomMessage, OpsAlertCustomMessageMaxRunes); custom != "" {
		text = append(text, "备注："+custom)
	}
	return strings.Join(text, "\n")
}

func opsAlertDimensionInt(value any) int64 {
	switch v := value.(type) {
	case json.Number:
		n, _ := v.Int64()
		return n
	case int64:
		return v
	case int:
		return int64(v)
	default:
		return 0
	}
}

package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

func isAnthropicWebMessageLimitEvent(eventType, payload string) bool {
	eventType = strings.TrimSpace(eventType)
	payloadType := gjson.Get(payload, "type").String()
	if eventType == "error" || payloadType == "error" {
		return false
	}
	return eventType == "message_limit" || payloadType == "message_limit"
}

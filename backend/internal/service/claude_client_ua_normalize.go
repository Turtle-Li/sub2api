package service

import (
	"net/http"
	"regexp"
	"strings"
)

// Claude Code 的 UA 在拿不到终端类型时会退化成：
//
//	claude-cli/2.1.281 (external, unknown)
//
// 这条请求本身完全合法（system 身份块、metadata、beta 都在），只是"产品标识"
// 不讨上游喜欢：实测同一份 body 仅把 product 从 unknown 换成 sdk-cli/cli，
// 上游就从 403 gateway_client_policy_denied 变成 200。
//
// 因此在**出站**做一次归一化，而不是把这类请求拒掉：退化的只是本机终端探测，
// 不是客户端身份造假。
var (
	// 匹配 claude-cli/<版本> 后的 "(external, unknown" 前缀，保留分隔符细节。
	claudeCLIUnknownProductPattern = regexp.MustCompile(
		`(?i)^(claude-cli/\d+\.\d+\.\d+\s*\(\s*[^,)]*,\s*)unknown(\s*[,)])`,
	)
)

// claudeCLINormalizedProduct 是退化标识被改写成的合法产品标识。
// 选 sdk-cli 而不是 cli：无终端场景通常正是被 SDK / 守护进程拉起，
// 与真实形态最接近，且上游实测放行。
const claudeCLINormalizedProduct = "sdk-cli"

// normalizeClaudeCLIUserAgent 把 "(external, unknown)" 归一化成合法产品标识。
// 其余形态（含正常 cli / sdk-cli / claude-desktop-3p / local-agent）原样返回。
func normalizeClaudeCLIUserAgent(ua string) string {
	if !claudeCLIUnknownProductPattern.MatchString(ua) {
		return ua
	}
	return claudeCLIUnknownProductPattern.ReplaceAllString(ua, "${1}"+claudeCLINormalizedProduct+"${2}")
}

// normalizeOutboundClaudeCLIUserAgent 在转发前就地改写请求头里的 UA。
// 只处理 Anthropic 家族；对账号级 header override 不抢夺决定权——调用方应
// 在本函数之后再应用 override。
func normalizeOutboundClaudeCLIUserAgent(header http.Header) {
	if header == nil {
		return
	}
	raw := ""
	for key, values := range header {
		if strings.EqualFold(key, "user-agent") && len(values) > 0 {
			raw = values[0]
			_ = key
			break
		}
	}
	if raw == "" {
		return
	}
	normalized := normalizeClaudeCLIUserAgent(raw)
	if normalized == raw {
		return
	}
	// 复用既有的大小写保留写入逻辑：先删掉所有形态，再按原 wire 大小写写回。
	deleteHeaderAllForms(header, "user-agent")
	setHeaderRaw(header, "User-Agent", normalized)
}

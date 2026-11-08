package service

import "strings"

// 上游「客户端不在允许范围」类 403 的识别。
//
// 这类响应描述的是**这次请求的客户端形态**不被上游分组接受，而不是账号凭据失效：
// 同一个账号换一个官方客户端的请求就能成功。把它当作账号级鉴权失败处理，会得到
// 一个非常糟糕的放大效应——任意一个非官方客户端（Codex、脚本、探针、旧版 SDK）
// 打进来一次，账号就被永久置为 error，随后该分组所有人的正常调用一起失败。
//
// 与 Cloudflare 1010（请求/边缘级拦截）同口径：仍然按失败处理并触发 failover，
// 但不写账号错误状态、不消耗 403 升级计数。
//
// 已知措辞来源：
//   - 第三方 Sub2API 系网关：{"code":"gateway_client_policy_denied", ...}
//   - 中文分组限制：「当前分组限制了可用的客户端 / 你使用的客户端不在允许范围内」
//   - 英文分组限制："This group is restricted to Claude Code clients"
//   - 调度层本地错误："this group only allows Claude Code clients"
var upstreamClientPolicyDeniedMarkers = []string{
	"gateway_client_policy_denied",
	"client_policy_denied",
	"客户端不在允许范围",
	"限制了可用的客户端",
	"restricted to claude code clients",
	"only allows claude code clients",
}

// isUpstreamClientPolicyDenied 报告上游 403 是否属于客户端策略拒绝。
// upstreamMsg 与原始响应体都参与判断：不同上游把 code 放在 JSON body，
// 把可读文案放在 message 或 error_message 里。
func isUpstreamClientPolicyDenied(upstreamMsg string, responseBody []byte) bool {
	haystack := strings.ToLower(upstreamMsg)
	if len(responseBody) > 0 && len(responseBody) <= 64*1024 {
		haystack += "\n" + strings.ToLower(string(responseBody))
	}
	for _, marker := range upstreamClientPolicyDeniedMarkers {
		if strings.Contains(haystack, marker) {
			return true
		}
	}
	return false
}

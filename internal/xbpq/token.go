package xbpq

import (
	"context"
	"sort"
	"strings"
)

// 令牌自动续期。行为对齐 guoapp3 native/core/provider_xbpq_token.go：
// 播放解析失败时用 refresh token 换新 access token，改写「头部集合」后重试一次。
//
// 规则字段：
//
//	刷新地址     换取新令牌的接口，绝对地址或相对路径
//	刷新令牌     refresh token
//	令牌路径     响应里新 access token 的路径，默认 data.token
//	刷新令牌路径  响应里新 refresh token 的路径，默认 data.refreshToken

const (
	defaultTokenAccessPath  = "data.token"
	defaultTokenRefreshPath = "data.refreshToken"
)

// RefreshEndpoint 刷新接口地址；未配置或非法时返回空。
func (r Rule) RefreshEndpoint(base string) string {
	raw := strings.TrimSpace(r.Field("刷新地址", "令牌刷新地址", "刷新url", "刷新接口"))
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "/") {
		origin := hostOrigin(base)
		if origin == "" {
			origin = hostOrigin(r.HomeURL())
		}
		return strings.TrimRight(origin, "/") + raw
	}
	if !strings.HasPrefix(raw, "http") {
		return ""
	}
	return raw
}

// RefreshToken 规则里配置的 refresh token。
func (r Rule) RefreshToken() string {
	return strings.TrimSpace(r.Field("刷新令牌", "refreshToken", "刷新token", "令牌刷新值"))
}

// TokenAccessPath 刷新响应里新 access token 的取值路径。
func (r Rule) TokenAccessPath() string {
	if path := strings.TrimSpace(r.Field("令牌路径", "新令牌路径", "访问令牌路径")); path != "" {
		return path
	}
	return defaultTokenAccessPath
}

// TokenRefreshPath 刷新响应里新 refresh token 的取值路径。
func (r Rule) TokenRefreshPath() string {
	if path := strings.TrimSpace(r.Field("刷新令牌路径", "新刷新令牌路径")); path != "" {
		return path
	}
	return defaultTokenRefreshPath
}

// FormatHeaderSet 把头部表还原成 `名$值#名$值` 写法。
func FormatHeaderSet(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"$"+headers[name])
	}
	return strings.Join(parts, "#")
}

// headerSetWithToken 把头部集合里的 Authorization 换成新令牌，其余头保留。
func headerSetWithToken(raw, access string) string {
	headers := ParseHeaderSet(raw)
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Authorization"] = "Bearer " + access
	return FormatHeaderSet(headers)
}

// WithToken 返回替换了新令牌的规则副本（不改原规则）。
func (r Rule) WithToken(access, refresh string) Rule {
	fields := make(map[string]string, len(r.fields))
	for key, value := range r.fields {
		fields[key] = value
	}
	fields[normalizeKey("头部集合")] = headerSetWithToken(r.Field("头部集合", "请求头集合", "Headers"), access)
	if refresh != "" {
		fields[normalizeKey("刷新令牌")] = refresh
	}
	return Rule{fields: fields, order: r.order, declared: r.declared}
}

// withRule 返回替换了规则的引擎副本（共享 Fetcher，不改原引擎）。
func (e *Engine) withRule(rule Rule) *Engine {
	clone := *e
	clone.Rule = rule
	return &clone
}

// refreshAccessToken 用 refresh token 换一套新令牌。
func (e *Engine) refreshAccessToken(ctx context.Context) (Rule, bool) {
	endpoint := e.Rule.RefreshEndpoint(e.Base)
	refresh := e.Rule.RefreshToken()
	if endpoint == "" || refresh == "" {
		return Rule{}, false
	}
	fetcher := e.Fetcher.WithHeaders(map[string]string{
		"Authorization": "Bearer " + refresh,
		"Accept":        "application/json",
	})
	// `;post` 走现有抓取通道；body 为空，令牌放在 Authorization 头里。
	body, err := fetcher.Get(ctx, endpoint+";post", strings.TrimRight(e.Base, "/")+"/")
	if err != nil {
		return Rule{}, false
	}
	access := strings.TrimSpace(jsonStringValue(body, e.Rule.TokenAccessPath()))
	if access == "" || access == refresh {
		return Rule{}, false
	}
	next := strings.TrimSpace(jsonStringValue(body, e.Rule.TokenRefreshPath()))
	if next == "" {
		next = refresh
	}
	return e.Rule.WithToken(access, next), true
}

// Package ai 封装 OpenAI 兼容的 Chat Completions 调用。
//
// 任何提供 /v1/chat/completions 的服务都能接：OpenAI、DeepSeek、通义、月之暗面、
// Groq、OpenRouter、本地 Ollama / vLLM / One-API / New-API 聚合网关等。
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Message 是一条对话消息。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Config 是调用配置。
type Config struct {
	BaseURL string // 例如 https://api.deepseek.com/v1 或 https://your-gateway.com/v1
	APIKey  string
	Model   string
	Timeout int // 秒，<=0 用默认 120
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// NormalizeBaseURL 把用户填写的 baseURL 规整成 Chat Completions 端点。
// 兼容以下几种写法：
//   - https://api.openai.com/v1           → /v1/chat/completions
//   - https://api.openai.com              → 自动补 /v1
//   - https://api.openai.com/v1/chat/completions → 原样
//   - 末尾斜杠、空白                       → 清理
func NormalizeBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.TrimRight(raw, "/")
	switch {
	case strings.HasSuffix(raw, "/chat/completions"):
		return raw
	case strings.HasSuffix(raw, "/v1"), strings.HasSuffix(raw, "/v2"), strings.HasSuffix(raw, "/v3"):
		return raw + "/chat/completions"
	default:
		return raw + "/v1/chat/completions"
	}
}

// clientFor 构造 HTTP 客户端。若目标是回环地址（本地 Ollama / vLLM / one-api 网关 / 测试桩），
// 强制绕过系统代理——Windows 上"局域网与本机地址使用代理"或 NO_PROXY 未配时，
// 默认代理会把 127.0.0.1 请求转给上游导致连接失败。
func clientFor(endpoint string, timeout int) *http.Client {
	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	u, err := url.Parse(endpoint)
	if err != nil {
		return client
	}
	host := u.Hostname()
	if host == "localhost" {
		// fallthrough
	} else if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return client
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client.Transport = transport
	return client
}

// retryableAIStatus 判断该状态码是否值得重试：408 请求超时、429 限流、5xx 服务端错误。
// 400/401/403 属请求体、鉴权或权限问题，重试只会白等，直接失败。
func retryableAIStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

// Chat 发起一轮对话，返回助手回复文本（不带 ctx 的便捷入口）。
func Chat(cfg Config, messages []Message) (string, error) {
	return ChatCtx(context.Background(), cfg, messages)
}

// ChatCtx 发起一轮对话，返回助手回复文本。
//
// 为什么需要重试：写源 prompt 体积很大（4 页样本各 24k 字符 + 站点指纹，约 12 万字符），
// 模型生成耗时长，部分网关/反向代理会在窗口耗尽时回 408 Request Timeout。
// 这类超时是「等待窗口不够」而非「请求本身有问题」，退避后重试通常第二次就能拿到结果，
// 一次性失败会让人误以为模型或 Key 不可用。
//
// ctx 由调用方给出（HTTP 请求 ctx 或专家写源的 270 秒预算），重试总耗时严格受它约束，
// 不会把 Vercel 函数的 300 秒上限撞穿。
func ChatCtx(ctx context.Context, cfg Config, messages []Message) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	endpoint := NormalizeBaseURL(cfg.BaseURL)
	if endpoint == "" {
		return "", fmt.Errorf("缺少 AI 接口 BaseURL")
	}
	if cfg.Model == "" {
		return "", fmt.Errorf("缺少模型名称")
	}
	payload, err := json.Marshal(chatRequest{
		Model:    cfg.Model,
		Messages: messages,
		Stream:   false,
	})
	if err != nil {
		return "", err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 120
	}
	// 整体预算：两次单次超时 + 全部退避；再与 ctx 的 deadline 取较小者。
	budget := time.Duration(timeout)*time.Second*2 + 16*time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if left := time.Until(deadline); left > 0 && left < budget {
			budget = left
		}
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	backoff := []time.Duration{1500 * time.Millisecond, 4000 * time.Millisecond, 8000 * time.Millisecond}
	var lastErr error
	for attempt := 0; attempt <= len(backoff); attempt++ {
		content, status, err := chatOnce(ctx, cfg, endpoint, payload, timeout)
		if err == nil {
			return content, nil
		}
		lastErr = err
		if status > 0 && !retryableAIStatus(status) {
			break // 确定性错误，重试无意义
		}
		if attempt < len(backoff) {
			select {
			case <-ctx.Done():
				return "", lastErr
			case <-time.After(backoff[attempt]):
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("AI 接口无响应")
	}
	return "", lastErr
}

// chatOnce 发起一次真实请求，返回 (回复文本, HTTP 状态码, 错误)。
// 状态码一并回传给 ChatCtx 判断是否值得重试。
func chatOnce(ctx context.Context, cfg Config, endpoint string, payload []byte, timeout int) (string, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(cfg.APIKey) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.APIKey))
	}
	client := clientFor(endpoint, timeout)
	response, err := client.Do(request)
	if err != nil {
		// ctx 已取消时直接把原始错误带出去，便于上层区分「超时预算用尽」与「网络故障」。
		return "", 0, fmt.Errorf("调用 AI 接口失败: %w", err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if response.StatusCode >= 400 {
		return "", response.StatusCode, fmt.Errorf("AI 接口 HTTP %d: %s", response.StatusCode, clip(string(raw), 300))
	}
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", response.StatusCode, fmt.Errorf("AI 返回无法解析: %s", clip(string(raw), 300))
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", response.StatusCode, fmt.Errorf("AI 接口报错: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", response.StatusCode, fmt.Errorf("AI 未返回内容")
	}
	return parsed.Choices[0].Message.Content, response.StatusCode, nil
}

func clip(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

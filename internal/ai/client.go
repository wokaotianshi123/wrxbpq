// Package ai 封装 OpenAI 兼容的 Chat Completions 调用。
//
// 任何提供 /v1/chat/completions 的服务都能接：OpenAI、DeepSeek、通义、月之暗面、
// Groq、OpenRouter、本地 Ollama / vLLM / One-API / New-API 聚合网关等。
package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// Chat 发起一轮对话，返回助手回复文本。
func Chat(cfg Config, messages []Message) (string, error) {
	endpoint := NormalizeBaseURL(cfg.BaseURL)
	if endpoint == "" {
		return "", fmt.Errorf("缺少 AI 接口 BaseURL")
	}
	if cfg.Model == "" {
		return "", fmt.Errorf("缺少模型名称")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 120
	}
	payload, err := json.Marshal(chatRequest{
		Model:    cfg.Model,
		Messages: messages,
		Stream:   false,
	})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(cfg.APIKey) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.APIKey))
	}
	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("调用 AI 接口失败: %w", err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if response.StatusCode >= 400 {
		return "", fmt.Errorf("AI 接口 HTTP %d: %s", response.StatusCode, clip(string(raw), 300))
	}
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("AI 返回无法解析: %s", clip(string(raw), 300))
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("AI 接口报错: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("AI 未返回内容")
	}
	return parsed.Choices[0].Message.Content, nil
}

func clip(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

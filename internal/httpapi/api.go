// Package httpapi 提供全部 HTTP 处理逻辑。
// Vercel 的 api/*.go 与本地 cmd serve 共用这里的实现，避免两套代码走偏。
package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/wrxbpq/wrxbpq/internal/ai"
	"github.com/wrxbpq/wrxbpq/internal/verify"
	"github.com/wrxbpq/wrxbpq/internal/xbpq"
)

// AllSteps 是默认验证步骤顺序。
var AllSteps = []string{"parse", "catalog", "paging", "search", "detail", "play"}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func readJSON(w http.ResponseWriter, r *http.Request, target any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(target)
}

// ---- /api/probe ----

type probeRequest struct {
	Site  string `json:"site"`
	Limit int    `json:"limit"`
}

type probeResponse struct {
	OK      bool        `json:"ok"`
	Samples []ai.Sample `json:"samples,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// HandleProbe 抓取站点样本。
func HandleProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, probeResponse{Error: "仅支持 POST"})
		return
	}
	var request probeRequest
	if err := readJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, probeResponse{Error: "请求体解析失败"})
		return
	}
	samples, err := verify.Probe(r.Context(), request.Site, request.Limit)
	if err != nil {
		writeJSON(w, http.StatusOK, probeResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, probeResponse{OK: true, Samples: samples})
}

// ---- /api/generate ----

type generateRequest struct {
	Site     string      `json:"site"`
	Rule     string      `json:"rule"`
	Problems []string    `json:"problems"`
	Samples  []ai.Sample `json:"samples"`
	BaseURL  string      `json:"baseUrl"`
	APIKey   string      `json:"apiKey"`
	Model    string      `json:"model"`
	Timeout  int         `json:"timeout"`
}

type generateResponse struct {
	OK    bool   `json:"ok"`
	Rule  string `json:"rule,omitempty"`
	Raw   string `json:"raw,omitempty"`
	Error string `json:"error,omitempty"`
}

// HandleGenerate 调 AI 生成（或修复）规则。
func HandleGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, generateResponse{Error: "仅支持 POST"})
		return
	}
	var request generateRequest
	if err := readJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, generateResponse{Error: "请求体解析失败"})
		return
	}
	cfg := ai.Config{
		BaseURL: firstNonEmpty(request.BaseURL, os.Getenv("AI_BASE_URL")),
		APIKey:  firstNonEmpty(request.APIKey, os.Getenv("AI_API_KEY")),
		Model:   firstNonEmpty(request.Model, os.Getenv("AI_MODEL")),
		Timeout: request.Timeout,
	}
	if cfg.BaseURL == "" || cfg.Model == "" {
		writeJSON(w, http.StatusOK, generateResponse{OK: false, Error: "缺少 AI 配置（baseUrl / model）"})
		return
	}
	samples := request.Samples
	if len(samples) == 0 && request.Site != "" {
		probed, err := verify.Probe(r.Context(), request.Site, 0)
		if err != nil {
			writeJSON(w, http.StatusOK, generateResponse{OK: false, Error: "站点样本抓取失败: " + err.Error()})
			return
		}
		samples = probed
	}
	var messages []ai.Message
	if strings.TrimSpace(request.Rule) != "" {
		messages = ai.BuildFixMessages(request.Site, request.Rule, request.Problems, samples)
	} else {
		messages = ai.BuildGenerateMessages(request.Site, samples)
	}
	content, err := ai.Chat(cfg, messages)
	if err != nil {
		writeJSON(w, http.StatusOK, generateResponse{OK: false, Error: err.Error()})
		return
	}
	rule := ExtractRule(content)
	if rule == "" {
		writeJSON(w, http.StatusOK, generateResponse{OK: false, Raw: content, Error: "AI 返回内容里没有找到合法 JSON"})
		return
	}
	if _, ok := xbpq.ParseRule(rule); !ok {
		writeJSON(w, http.StatusOK, generateResponse{OK: false, Rule: rule, Raw: content, Error: "AI 产出无法识别为 XBPQ 规则（缺少 主页url/首页url/请求）"})
		return
	}
	writeJSON(w, http.StatusOK, generateResponse{OK: true, Rule: rule, Raw: content})
}

// ExtractRule 从模型回复里剥离 Markdown 代码块与前后废话，取出 JSON。
func ExtractRule(content string) string {
	value := strings.TrimSpace(content)
	if fence := strings.Index(value, "```"); fence >= 0 {
		rest := value[fence+3:]
		if newline := strings.Index(rest, "\n"); newline >= 0 {
			rest = rest[newline+1:]
		}
		if end := strings.LastIndex(rest, "```"); end >= 0 {
			rest = rest[:end]
		}
		value = strings.TrimSpace(rest)
		if strings.HasPrefix(value, "json") {
			value = strings.TrimSpace(strings.TrimPrefix(value, "json"))
		}
	}
	start := strings.IndexAny(value, "[{")
	end := strings.LastIndexAny(value, "]}")
	if start < 0 || end < start {
		return ""
	}
	candidate := strings.TrimSpace(value[start : end+1])
	candidate = trimTrailingComma(candidate)
	return candidate
}

var trailingCommaPattern = regexp.MustCompile(`,\s*([}\]])`)

// trimTrailingComma 去掉 JSON 尾部多余逗号（模型输出里很常见）。
func trimTrailingComma(text string) string {
	for {
		cleaned := trailingCommaPattern.ReplaceAllString(text, "$1")
		if cleaned == text {
			return text
		}
		text = cleaned
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// ---- /api/verify ----

type verifyRequest struct {
	Rule     string   `json:"rule"`
	Site     string   `json:"site"`
	Keyword  string   `json:"keyword"`
	DetailID string   `json:"detailId"`
	Episode  int      `json:"episode"`
	Steps    []string `json:"steps"`
}

type verifyResponse struct {
	OK      bool                `json:"ok"`
	Results []verify.StepResult `json:"results"`
	Error   string              `json:"error,omitempty"`
}

// HandleVerify 分步验证规则。
func HandleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, verifyResponse{Error: "仅支持 POST"})
		return
	}
	var request verifyRequest
	if err := readJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, verifyResponse{Error: "请求体解析失败"})
		return
	}
	steps := request.Steps
	if len(steps) == 0 {
		steps = AllSteps
	}
	options := verify.Options{
		Rule:      request.Rule,
		Site:      request.Site,
		Keyword:   request.Keyword,
		DetailID:  request.DetailID,
		Episode:   request.Episode,
		ProbeM3U8: true,
	}
	results := verify.RunSteps(r.Context(), options, steps)
	allOK := true
	for _, result := range results {
		if !result.OK {
			allOK = false
			break
		}
	}
	writeJSON(w, http.StatusOK, verifyResponse{OK: allOK, Results: results})
}

// ---- /api/health ----

// HandleHealth 健康检查。
func HandleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"steps": AllSteps,
		"ai":    os.Getenv("AI_BASE_URL") != "" || os.Getenv("AI_MODEL") != "",
	})
}

// NewMux 构建完整路由（API + 磁盘上的 public/ 静态资源），供本地 `wrxbpq serve` 使用。
func NewMux() http.Handler {
	mux := http.NewServeMux()
	RegisterAPI(mux)
	mux.Handle("/", http.FileServer(http.Dir("public")))
	return withCORS(mux)
}

// RegisterAPI 把四个 API 路由注册到给定 mux 上（不含静态资源）。
// Vercel 的 main.go 入口用它，静态资源由 go:embed 单独提供。
func RegisterAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/health", HandleHealth)
	mux.HandleFunc("/api/probe", HandleProbe)
	mux.HandleFunc("/api/generate", HandleGenerate)
	mux.HandleFunc("/api/verify", HandleVerify)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

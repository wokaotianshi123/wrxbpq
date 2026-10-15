// Package httpapi 提供全部 HTTP 处理逻辑。
// Vercel 的 api/*.go 与本地 cmd serve 共用这里的实现，避免两套代码走偏。
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wrxbpq/wrxbpq/internal/ai"
	"github.com/wrxbpq/wrxbpq/internal/expert"
	"github.com/wrxbpq/wrxbpq/internal/fingerprint"
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
	OK          bool        `json:"ok"`
	Samples     []ai.Sample `json:"samples,omitempty"`
	Fingerprint string      `json:"fingerprint,omitempty"`
	Error       string      `json:"error,omitempty"`
}

// HandleProbe 抓取站点样本，并同步产出站点指纹（含模板/简写判定）供前端回显。
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
	writeJSON(w, http.StatusOK, probeResponse{
		OK:          true,
		Samples:     samples,
		Fingerprint: fingerprint.Analyze(samples),
	})
}

// ---- /api/generate ----

type generateRequest struct {
	Site      string      `json:"site"`
	Rule      string      `json:"rule"`
	Problems  []string    `json:"problems"`
	Notes     string      `json:"notes"`
	Samples   []ai.Sample `json:"samples"`
	Version   string      `json:"version"` // "simple"=简写版（默认） "full"=完整版（字段全部显式写全，不靠模板兜底） "expert"=专家写源（逐字段实测验证后才输出）
	MaxRounds int         `json:"maxRounds"`
	BaseURL   string      `json:"baseUrl"`
	APIKey    string      `json:"apiKey"`
	Model     string      `json:"model"`
	Timeout   int         `json:"timeout"`
}

type generateResponse struct {
	OK           bool        `json:"ok"`
	Rule         string      `json:"rule,omitempty"`
	Raw          string      `json:"raw,omitempty"`
	Error        string      `json:"error,omitempty"`
	Samples      []ai.Sample `json:"samples,omitempty"`
	Fingerprint  string      `json:"fingerprint,omitempty"`
	TemplateHit  string      `json:"templateHit,omitempty"`
	Version      string      `json:"version,omitempty"`      // 回显本次实际使用的版本（simple/full/expert）
	ExpertRounds int         `json:"expertRounds,omitempty"` // 专家模式实际执行的逐字段实测回喂轮数
	ExpertClean  bool        `json:"expertClean,omitempty"`  // 专家模式：最终规则是否全部字段实测通过
	ExpertLog    string      `json:"expertLog,omitempty"`    // 专家模式：最后一轮逐字段实测报告（✓/✗ 每字段一行）
}

// expertBudget 是专家写源整个 handler 的时间预算：
// 前端 fetch 290 秒放弃、Vercel 函数 300 秒硬掐，留 270 秒给「生成+修复+逐字段实测回喂」，
// 超时前 Refine 会带着最优候选提前收工，不让用户拿不到任何结果。
const expertBudget = 270 * time.Second

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
		BaseURL: xbpq.FirstNonEmpty(request.BaseURL, os.Getenv("AI_BASE_URL")),
		APIKey:  xbpq.FirstNonEmpty(request.APIKey, os.Getenv("AI_API_KEY")),
		Model:   xbpq.FirstNonEmpty(request.Model, os.Getenv("AI_MODEL")),
		Timeout: request.Timeout,
	}
	if cfg.BaseURL == "" || cfg.Model == "" {
		writeJSON(w, http.StatusOK, generateResponse{OK: false, Error: "缺少 AI 配置（baseUrl / model）"})
		return
	}
	samples := request.Samples
	fixMode := strings.TrimSpace(request.Rule) != ""
	if len(samples) == 0 {
		if !fixMode {
			// 生成模式强制依赖抓取样本：没有基础数据就不让 AI 凭空猜规则。
			writeJSON(w, http.StatusOK, generateResponse{OK: false, Error: "必须先点击「抓取样本」获取站点基础数据后再生成规则。"})
			return
		}
		// 修复模式允许补抓（前端样本可能因服务重启丢失）。
		if request.Site != "" {
			probed, err := verify.Probe(r.Context(), request.Site, 0)
			if err != nil {
				writeJSON(w, http.StatusOK, generateResponse{OK: false, Error: "站点样本抓取失败: " + err.Error()})
				return
			}
			samples = probed
		}
	}
	var messages []ai.Message
	ruleVersion := ai.NormalizeRuleVersion(request.Version)
	siteFingerprint := fingerprint.Analyze(samples)
	if strings.TrimSpace(request.Rule) != "" {
		messages = ai.BuildFixMessages(request.Site, request.Rule, request.Problems, samples, request.Notes, siteFingerprint, ruleVersion)
	} else {
		messages = ai.BuildGenerateMessages(request.Site, samples, request.Notes, siteFingerprint, ruleVersion)
	}
	content, err := ai.ChatCtx(r.Context(), cfg, messages)
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
	// 格式自检：锚点一字未现、缺 &&、字面 \" 这类确定性错误直接回喂 AI 自动修一轮，
	// 省掉用户"验证→修复"的一个来回。
	if issues := fingerprint.Check(rule, samples); len(issues) > 0 {
		var problems []string
		for _, issue := range issues {
			problems = append(problems, "字段「"+issue.Field+"」："+issue.Problem)
		}
		retryMessages := ai.BuildFixMessages(request.Site, rule, problems, samples, request.Notes, siteFingerprint, ruleVersion)
		if retryContent, retryErr := ai.ChatCtx(r.Context(), cfg, retryMessages); retryErr == nil {
			if retried := ExtractRule(retryContent); retried != "" {
				if _, retryOK := xbpq.ParseRule(retried); retryOK && len(fingerprint.Check(retried, samples)) < len(issues) {
					rule = retried
				}
			}
		}
	}
	// 专家写源：把候选规则逐字段拿去真实样本上实测截值，失败证据回喂再修，
	// 全部实测通过（或轮数/时间用尽后的最优版）才作为最终输出。
	var (
		expertRounds int
		expertClean  bool
		expertLog    string
	)
	if ruleVersion == ai.RuleVersionExpert {
		ctx, cancel := context.WithTimeout(r.Context(), expertBudget)
		defer cancel()
		result := expert.Refine(ctx, cfg, request.Site, samples, request.Notes, siteFingerprint, rule, request.MaxRounds)
		rule = result.Rule
		expertRounds = result.Rounds
		expertClean = result.Clean
		expertLog = result.Evidence
	}
	rule = xbpq.SanitizeValue(rule)
	writeJSON(w, http.StatusOK, generateResponse{
		OK:           true,
		Rule:         rule,
		Raw:          content,
		Samples:      samples,
		Fingerprint:  siteFingerprint,
		TemplateHit:  templateHitNames(rule),
		Version:      ruleVersion,
		ExpertRounds: expertRounds,
		ExpertClean:  expertClean,
		ExpertLog:    expertLog,
	})
}

// templateHitNames 从规则文本回读命中的模板家族名（供前端提示"本规则靠哪些模板兜底"）。
func templateHitNames(ruleText string) string {
	parsed, ok := xbpq.ParseRule(ruleText)
	if !ok {
		return ""
	}
	return parsed.TemplateHitNames()
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
	return xbpq.SanitizeValue(candidate)
}

var trailingCommaPattern = regexp.MustCompile(`,\s*([}\]])`)

// extractFieldFromJSON 从 AI 返回的 JSON 文本里取出指定字段的值。
//
// 与 ParseRule 不同：它不要求规则整体合法（即不强制含 主页url/分类url 等锚点字段），
// 因此 AI 只回「被纠正的那一个字段」时也能正确取到值。字段名按 xbpq.NormalizeKey 归一，
// 并额外尝试该字段在字典里的全部别名（如「图片」↔「列表图片」↔「封面图」），
// 兼容模型用别名输出的情形。返回前剔除乱码替换符，保证写出的源不含坏字符。
func extractFieldFromJSON(text, fieldName string) string {
	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return ""
	}
	targets := map[string]bool{xbpq.NormalizeKey(fieldName): true}
	if spec, ok := xbpq.LookupField(fieldName); ok {
		targets[xbpq.NormalizeKey(spec.Name)] = true
		for _, alias := range spec.Aliases {
			targets[xbpq.NormalizeKey(alias)] = true
		}
	}
	var found string
	collect := func(node map[string]any) {
		for key, value := range node {
			if targets[xbpq.NormalizeKey(key)] {
				if s := stringValueLocal(value); strings.TrimSpace(s) != "" && found == "" {
					found = s
				}
			}
		}
	}
	switch typed := decoded.(type) {
	case map[string]any:
		collect(typed)
	case []any:
		for _, entry := range typed {
			if node, ok := entry.(map[string]any); ok {
				collect(node)
			}
		}
	}
	return xbpq.SanitizeValue(found)
}

func stringValueLocal(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case nil:
		return ""
	default:
		return ""
	}
}

// replaceFieldInRule 把规则 JSON 中某个字段替换为新值，返回新 JSON 文本。
// 清掉会归一到同一规范名的别名键，避免重复键；保留其余字段，供服务端回验时依赖
// （例如改「标题」时仍需要 数组 才能跑通条目段内截取）。
func replaceFieldInRule(ruleJSON, field, value string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(ruleJSON), &m); err != nil || m == nil {
		m = map[string]any{}
	}
	canon := xbpq.CanonicalFieldName(field)
	if spec, ok := xbpq.LookupField(field); ok {
		for _, alias := range spec.Aliases {
			delete(m, alias)
		}
	}
	m[canon] = xbpq.SanitizeValue(value)
	// 用关闭 HTML 转义的序列化，避免 &&、</ul> 等被转成 \u0026\u003c。
	body := xbpq.MarshalNoEscape(m)
	if body == "" {
		return ruleJSON
	}
	return body
}

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

// ---- /api/fields & /api/check-field & /api/ai-fix-field ----

// HandleFields 返回完整字段字典（按界面分组）+ 当前 分类url 命中的模板家族默认字段。
// 写源界面的字段表格由它驱动；templateFields 用于「简写版临时改模板」编辑区预填。
func HandleFields(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "仅支持 POST"})
		return
	}
	var request struct {
		CategoryURL string `json:"categoryUrl"`
	}
	_ = readJSON(w, r, &request)
	type group struct {
		Name   string            `json:"name"`
		Fields []xbpq.FieldSpec  `json:"fields"`
	}
	var groups []group
	dict := xbpq.FieldDictionary()
	for _, groupName := range xbpq.GroupOrder {
		g := group{Name: groupName}
		for _, spec := range dict {
			if spec.Group == groupName {
				g.Fields = append(g.Fields, spec)
			}
		}
		if len(g.Fields) > 0 {
			groups = append(groups, g)
		}
	}
	response := map[string]any{
		"ok":            true,
		"groups":        groups,
		"templateJSON":  xbpq.DefaultTemplateFieldsJSON(request.CategoryURL),
	}
	if name, _ := xbpq.MatchTemplate(request.CategoryURL); name != "" {
		response["templateHit"] = name
	}
	writeJSON(w, http.StatusOK, response)
}

// HandleCheckField 单字段即时验证：前端每填完一个字段调用一次。
// 简写版「临时改模板」时，前端把改过的模板字段直接作为显式字段拼进 rule 传来——
// 显式字段会覆盖模板兜底，CheckFields 自然按显式声明实测，无需额外参数。
type checkFieldRequest struct {
	Rule    string      `json:"rule"` // 当前已填字段拼出的 JSON（或完整规则文本）
	Field   string      `json:"field"`
	Site    string      `json:"site"`
	Samples []ai.Sample `json:"samples"`
	Keyword string      `json:"keyword"`
}

// HandleCheckField 对单个字段做网页抓取/样本实测，返回 pass/fail/warn/skip 与证据。
func HandleCheckField(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "仅支持 POST"})
		return
	}
	var request checkFieldRequest
	if err := readJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败"})
		return
	}
	ruleText := strings.TrimSpace(request.Rule)
	if ruleText == "" {
		writeJSON(w, http.StatusOK, map[string]any{"findings": []verify.FieldFinding{{Field: request.Field, Status: "fail", Message: "还没有可验证的规则内容"}}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	target := []string{request.Field}
	if strings.TrimSpace(request.Field) == "" {
		target = nil // 空字段名 = 验证全部显式字段
	}
	findings := verify.CheckFields(ctx, verify.FieldOptions{
		Rule:    ruleText,
		Site:    request.Site,
		Samples: request.Samples,
		Keyword: request.Keyword,
	}, target)
	writeJSON(w, http.StatusOK, map[string]any{"findings": findings})
}

// HandleAIFixField 单字段 AI 纠正：带字段名/当前值/实测问题/样本，AI 只改这一个字段。
type aiFixFieldRequest struct {
	Site      string      `json:"site"`
	Field     string      `json:"field"`
	Value     string      `json:"value"`
	Problem   string      `json:"problem"`
	Samples   []ai.Sample `json:"samples"`
	Notes     string      `json:"notes"`
	Rule      string      `json:"rule"` // 当前规则文本（AI 输出与其合并后回传）
	Keyword   string      `json:"keyword"`
	BaseURL   string      `json:"baseUrl"`
	APIKey    string      `json:"apiKey"`
	Model     string      `json:"model"`
	Timeout   int         `json:"timeout"`
}

// HandleAIFixField 调 AI 对单个字段核实纠正，返回该字段的新值。
func HandleAIFixField(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "仅支持 POST"})
		return
	}
	var request aiFixFieldRequest
	if err := readJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败"})
		return
	}
	cfg := ai.Config{
		BaseURL: xbpq.FirstNonEmpty(request.BaseURL, os.Getenv("AI_BASE_URL")),
		APIKey:  xbpq.FirstNonEmpty(request.APIKey, os.Getenv("AI_API_KEY")),
		Model:   xbpq.FirstNonEmpty(request.Model, os.Getenv("AI_MODEL")),
		Timeout: request.Timeout,
	}
	if cfg.Timeout <= 0 || cfg.Timeout > 90 {
		cfg.Timeout = 90 // 闭环最多 2 次尝试，单次 AI 调用封顶 90s，避免撞穿 Vercel 300s
	}
	if cfg.BaseURL == "" || cfg.Model == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "缺少 AI 配置（baseUrl / model）"})
		return
	}
	samples := request.Samples
	if len(samples) == 0 && request.Site != "" {
		probed, err := verify.Probe(r.Context(), request.Site, 0)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "站点样本抓取失败: " + err.Error()})
			return
		}
		samples = probed
	}
	fingerprintText := fingerprint.Analyze(samples)

	// 用界面当前规则作合并基底：AI 只回那个字段的值，我们覆盖回去再服务端回验。
	baseRule := strings.TrimSpace(request.Rule)
	if baseRule == "" {
		baseRule = "{}"
	}

	problem := strings.TrimSpace(request.Problem)
	currentValue := request.Value

	// 闭环：AI 给值后，服务端用和「验证」完全相同的逐字段实测去回验；不通过就把
	// 最新失败原因喂回 AI 再改，直到通过或耗尽次数。这样界面再点验证一定不会还是红。
	const maxAttempts = 2
	var lastRaw, lastProblem string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		messages := ai.BuildFieldFixMessages(request.Site, request.Field, currentValue, problem, samples, request.Notes, fingerprintText)
		content, err := ai.ChatCtx(r.Context(), cfg, messages)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		extracted := ExtractRule(content)
		if extracted == "" {
			lastRaw = content
			if attempt < maxAttempts {
				problem = "你返回的内容里没有合法 JSON，请只输出一个 {字段名: 值} 的 JSON 对象，不要解释、不要代码块"
				continue
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "AI 返回内容里没有找到合法 JSON", "raw": content})
			return
		}
		newValue := extractFieldFromJSON(extracted, request.Field)
		if strings.TrimSpace(newValue) == "" {
			lastRaw = content
			if attempt < maxAttempts {
				problem = "你返回的 JSON 里没有字段「" + request.Field + "」的值，请把它修正后的值写进这个字段"
				continue
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "AI 没有给出字段「" + request.Field + "」的值", "raw": content})
			return
		}
		// 服务端回验：把新值并入规则，跑一遍逐字段验证（与「验证」按钮同一套实测）。
		candidate := replaceFieldInRule(baseRule, request.Field, newValue)
		findings := verify.CheckFields(r.Context(), verify.FieldOptions{
			Rule:    candidate,
			Site:    request.Site,
			Samples: samples,
			Keyword: request.Keyword,
		}, []string{request.Field})
		if len(findings) == 0 {
			// 没有对应层样本、回验被 skip：放行但明确提示用户再点验证确认。
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "field": request.Field, "value": newValue,
				"note": "AI 已按网页源码更新该字段（服务端未能回验，请再点一次「验证」确认）"})
			return
		}
		f := findings[0]
		if f.Status == "pass" {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "field": request.Field, "value": newValue,
				"note": "AI 已按网页源码核实，服务端回验通过"})
			return
		}
		if f.Status == "warn" && attempt >= 2 {
			// warn 不算标红，放行但带上提醒。
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "field": request.Field, "value": newValue,
				"note": "AI 已更新（仅 warn：" + f.Message + "），可用"})
			return
		}
		// fail，或 warn 还想再搏一次：把最新失败原因喂回 AI。
		lastRaw = content
		lastProblem = f.Message
		problem = fmt.Sprintf("你刚给的「%s」值服务端回验仍【不通过】：%s。\n请回到下方网页源码，逐字确认锚点真的存在、start&&end 能截出正确内容，再给出能通过验证的写法。", request.Field, f.Message)
		currentValue = newValue // 让 AI 看到上一轮它写出的内容，避免原地打转
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": false,
		"error": "AI 多次修正仍不通过（最新问题：" + lastProblem + "）。建议：先修好该字段依赖的上一层字段（如 数组/播放数组/分类url），再回来点 AI 纠正。",
		"raw":   lastRaw})
}

// ---- /api/gen-json ----

// HandleGenJSON 把界面上逐字段填写的字段集【按字段字典顺序】统一生成源 JSON。
// 这是「完成后统一按字段生成源 json」的出口：空值不写、别名归一、键序稳定。
func HandleGenJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "仅支持 POST"})
		return
	}
	var request struct {
		Rule string `json:"rule"` // 前端已拼好的字段 JSON（含简写版临时模板覆盖字段）
	}
	if err := readJSON(w, r, &request); err != nil || strings.TrimSpace(request.Rule) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败或规则为空"})
		return
	}
	// 表格可能只填了一部分字段（缺 主页url/分类url 时 ParseRule 会拒），
	// 这里按“字段集合”直接解码 JSON，不走规则识别条件。
	var decoded map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(request.Rule)), &decoded); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "字段 JSON 无法解析：" + err.Error()})
		return
	}
	fields := map[string]string{}
	hasGarbled := false
	for key, value := range decoded {
		text := ""
		switch typed := value.(type) {
		case string:
			text = typed
		default:
			body, err := json.Marshal(typed)
			if err == nil {
				text = string(body)
			}
		}
		if strings.Contains(text, string(xbpq.ReplacementRune)) {
			hasGarbled = true
		}
		text = xbpq.SanitizeValue(text)
		if strings.TrimSpace(text) == "" {
			continue
		}
		fields[xbpq.CanonicalFieldName(key)] = text
	}
	if len(fields) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "没有已填写的字段可生成"})
		return
	}
	response := map[string]any{"ok": true, "json": xbpq.GenerateJSON(fields)}
	if hasGarbled {
		response["warning"] = "部分字段含乱码字符（已自动清除）；请核对相关锚点是否因样本抓取/解码不完整而残缺，必要时重新抓取样本再生成。"
	}
	writeJSON(w, http.StatusOK, response)
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
	mux.HandleFunc("/api/fields", HandleFields)
	mux.HandleFunc("/api/check-field", HandleCheckField)
	mux.HandleFunc("/api/ai-fix-field", HandleAIFixField)
	mux.HandleFunc("/api/gen-json", HandleGenJSON)
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

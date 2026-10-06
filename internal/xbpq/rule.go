// Package xbpq 是 TVBox/XBPQ 爬虫规则的解析与执行引擎。
//
// 行为对齐 guoapp3 项目 native/core/provider_xbpq.go（含该项目的三处修复：
// 非数字分类 ID 放宽、详情页封面先提取再补全、线路数组多线路）。
// 本包只依赖标准库 + x/net/html + x/text，可在 Serverless 环境直接编译。
package xbpq

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Rule 是一份解析后的 XBPQ 规则。
type Rule struct {
	fields map[string]string
	order  []string
}

// customSourceMaxBytes 规则文本上限（与核心一致）。
const customSourceMaxBytes = 1 << 20

func normalizeKey(key string) string {
	key = strings.TrimSpace(key)
	key = strings.ReplaceAll(key, "〔", "[")
	key = strings.ReplaceAll(key, "〕", "]")
	return strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "").Replace(key)
}

func normalizeValue(value string) string {
	value = strings.ReplaceAll(value, "〔", "[")
	value = strings.ReplaceAll(value, "〕", "]")
	return strings.TrimSpace(value)
}

// ParseRule 解析爬虫 JSON 文本；识别成功返回规则与 true。
// 兼容形态：整体对象、[{对象}] 数组、@long-text 拼接片段、尾部多余逗号。
func ParseRule(text string) (Rule, bool) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > customSourceMaxBytes {
		return Rule{}, false
	}
	if index := strings.Index(text, `@long-text:`); index >= 0 {
		head := trimTrailingComma(text[:index])
		tail := text[index:]
		if json.Valid([]byte(head)) {
			if embedded := extractLongText(tail); embedded != "" {
				if merged := mergeLongText(head, embedded); merged != "" {
					text = merged
				}
			}
		}
	}
	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		repaired := trimTrailingComma(text)
		if json.Unmarshal([]byte(repaired), &decoded) != nil {
			return Rule{}, false
		}
	}
	fields := map[string]string{}
	var order []string
	collect := func(node map[string]any) {
		for key, value := range node {
			nk := normalizeKey(key)
			if nk == "" {
				continue
			}
			nv := normalizeValue(stringValue(value))
			if _, exists := fields[nk]; !exists {
				order = append(order, nk)
			}
			fields[nk] = nv
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
	default:
		return Rule{}, false
	}
	if fields["主页url"] == "" && fields["首页url"] == "" && fields["请求"] == "" {
		return Rule{}, false
	}
	return Rule{fields: fields, order: order}, true
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case nil:
		return ""
	default:
		body, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(body)
	}
}

var longTextPattern = regexp.MustCompile(`(?s)@long-text:\s*"((?:[^"\\]|\\.)*)"\s*;?\s*$`)

func extractLongText(tail string) string {
	matches := longTextPattern.FindStringSubmatch(strings.TrimSpace(tail))
	if len(matches) < 2 {
		return ""
	}
	var decoded string
	if err := json.Unmarshal([]byte(`"`+matches[1]+`"`), &decoded); err != nil {
		return ""
	}
	return decoded
}

var trailingComma = regexp.MustCompile(`,\s*([}\]])`)

func trimTrailingComma(text string) string {
	for {
		cleaned := trailingComma.ReplaceAllString(text, "$1")
		if cleaned == text {
			return text
		}
		text = cleaned
	}
}

func mergeLongText(head, embedded string) string {
	var headNode, embeddedNode map[string]any
	if json.Unmarshal([]byte(head), &headNode) != nil {
		return ""
	}
	if json.Unmarshal([]byte(embedded), &embeddedNode) != nil {
		return head
	}
	for key, value := range embeddedNode {
		if _, exists := headNode[key]; !exists {
			headNode[key] = value
		}
	}
	body, err := json.Marshal(headNode)
	if err != nil {
		return head
	}
	return string(body)
}

// Field 依次尝试多个字段名（含 XBPQ 常见别名）。
func (r Rule) Field(names ...string) string {
	for _, name := range names {
		if value, found := r.fields[normalizeKey(name)]; found && value != "" {
			return value
		}
	}
	return ""
}

// HomeURL 站源主页地址（归一化为 scheme://host/path，无 query/fragment）。
func (r Rule) HomeURL() string {
	value := r.Field("主页url", "首页url", "请求")
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return ""
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// UserAgent 规则声明的 UA（可能为空）。
func (r Rule) UserAgent() string {
	value := r.Field("请求头", "User-Agent", "UA")
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if index := strings.Index(lower, "user-agent"); index >= 0 {
		value = strings.TrimSpace(value[index+len("user-agent"):])
		value = strings.TrimLeft(value, "$:：= \t")
		if first := strings.Fields(value); len(first) == 1 {
			value = first[0]
		}
	}
	if value == "" || len(value) > 240 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

// ---- 分类 ----

// Category 是一个分类项。
type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// categoryIDPattern 分类标识：既收 MacCMS 模板的纯数字 ID，也收站点把分类
// 写成路径片段的形态（tv、/fenlei/1…）。
var categoryIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_/-]{0,79}$`)

// Categories 解析「分类」串：name$id#name$id（含 `1--子分类$5#...` 变体）。
func (r Rule) Categories() []Category {
	raw := r.Field("分类")
	if raw == "" {
		return nil
	}
	var categories []Category
	seen := map[string]bool{}
	add := func(id, name string) {
		if id == "" || name == "" || seen[id] {
			return
		}
		seen[id] = true
		categories = append(categories, Category{ID: id, Name: name})
	}
	type subEntry struct {
		main, id, name string
	}
	var subs []subEntry
	for _, line := range strings.Split(raw, "||") {
		line = strings.TrimSpace(line)
		if line == "" || line == "空" {
			continue
		}
		for _, entry := range strings.Split(line, "#") {
			name, id, found := strings.Cut(entry, "$")
			if !found {
				continue
			}
			name = CleanText(name)
			id = strings.TrimSpace(id)
			if main, subName, hasMain := strings.Cut(name, "--"); hasMain && categoryIDPattern.MatchString(main) {
				if subName != "" {
					subs = append(subs, subEntry{main: main, id: id, name: subName})
				}
				continue
			}
			if categoryIDPattern.MatchString(id) && name != "" {
				add(id, name)
			}
		}
	}
	for _, sub := range subs {
		if seen[sub.id] {
			continue
		}
		seen[sub.id] = true
		categories = append(categories, Category{ID: sub.id, Name: sub.name})
	}
	return categories
}

// CategoryURL 渲染分类页地址（含翻页）。
func (r Rule) CategoryURL(base, categoryID string, page int) string {
	template := r.Field("分类url", "分类Url")
	if template == "" {
		return ""
	}
	values := map[string]string{
		"cateId":  categoryID,
		"catePg":  strconv.Itoa(page),
		"handurl": base + "/",
		"limit":   "20",
		"area":    "", "class": "", "lang": "", "year": "", "letter": "", "by": "",
	}
	if primary, _, hasSecond := strings.Cut(template, "#"); hasSecond && strings.Contains(template, "二级") {
		template = primary
	}
	return RenderURL(template, base, values)
}

// ---- URL 组装 ----

// RenderURL 填充占位符并把相对路径转绝对。规则里的绝对域名与站源当前地址
// 不一致时（镜像、换域名），以站源地址为准重写 host。
func RenderURL(template, base string, values map[string]string) string {
	template = strings.TrimSpace(template)
	if index := strings.Index(template, ";;"); index >= 0 {
		template = template[:index]
	}
	for key, value := range values {
		template = strings.ReplaceAll(template, "{"+key+"}", value)
	}
	template = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(strings.TrimSpace(template))
	if template == "" {
		return ""
	}
	if !strings.HasPrefix(template, "http") && !strings.HasPrefix(template, "//") {
		if !strings.HasPrefix(template, "/") {
			template = "/" + template
		}
		template = base + template
	}
	if strings.HasPrefix(template, "//") {
		template = "https:" + template
	}
	parsed, err := url.Parse(template)
	if err != nil || parsed.Host == "" {
		return ""
	}
	if baseParsed, baseErr := url.Parse(base); baseErr == nil && baseParsed.Host != "" && parsed.Host != baseParsed.Host {
		parsed.Scheme = baseParsed.Scheme
		parsed.Host = baseParsed.Host
		template = parsed.String()
	}
	return template
}

// Absolute 把相对引用解析成绝对地址。
func Absolute(base, reference string) string {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return ""
	}
	if strings.HasPrefix(reference, "//") {
		return "https:" + reference
	}
	address, err := url.Parse(reference)
	if err != nil {
		return ""
	}
	if address.IsAbs() {
		return address.String()
	}
	origin, err := url.Parse(strings.TrimRight(base, "/") + "/")
	if err != nil {
		return ""
	}
	return origin.ResolveReference(address).String()
}

// JoinLink 处理「多线链接」风格的 + 拼接（字面量用引号包裹，其余段丢弃）。
func JoinLink(pageURL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "$") {
		return ""
	}
	if lowered := strings.ToLower(raw); strings.HasPrefix(lowered, "href=") {
		raw = raw[len("href="):]
	}
	if strings.Contains(raw, "+") {
		var builder strings.Builder
		for _, segment := range strings.Split(raw, "+") {
			if len(segment) >= 2 && strings.HasPrefix(segment, `"`) && strings.HasSuffix(segment, `"`) {
				builder.WriteString(segment[1 : len(segment)-1])
			}
		}
		raw = builder.String()
	}
	if raw == "" {
		return ""
	}
	return Absolute(pageURL, raw)
}

var numericIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
var detailPathPattern = regexp.MustCompile(`(?i)/(?:voddetail|detail|show|vod|drama|movie|tv)/([0-9]+)(?:[-./]|$)`)

// SourceIDFromURL 从详情链接提取数字 ID。
func SourceIDFromURL(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	path := strings.TrimSuffix(parsed.Path, ".html")
	if matches := detailPathPattern.FindStringSubmatch(path + "/"); len(matches) > 1 {
		return matches[1]
	}
	cleaned := strings.Trim(path, "/")
	parts := strings.Split(cleaned, "/")
	for index := len(parts) - 1; index >= 0; index-- {
		candidate := strings.TrimSuffix(parts[index], ".html")
		if numericIDPattern.MatchString(candidate) {
			return candidate
		}
		if _, tail, found := strings.Cut(candidate, "-"); found && numericIDPattern.MatchString(tail) {
			return tail
		}
	}
	if id := parsed.Query().Get("id"); numericIDPattern.MatchString(id) {
		return id
	}
	return ""
}

// IDFromLink 从详情链接提取稳定 ID。numeric 为真时提数字 ID，
// 否则整个链接编码成 ID（详情阶段可无损还原页面地址）。
func IDFromLink(link string, numeric bool) string {
	if numeric {
		if id := SourceIDFromURL(link); id != "" {
			return id
		}
	}
	return "u" + url.QueryEscape(link)
}

// LinkFromID 还原 SourceID 对应的详情页 URL。
func LinkFromID(base, id string) string {
	if strings.HasPrefix(id, "u") {
		if decoded, err := url.QueryUnescape(strings.TrimPrefix(id, "u")); err == nil && strings.HasPrefix(decoded, "http") {
			return decoded
		}
	}
	return ""
}

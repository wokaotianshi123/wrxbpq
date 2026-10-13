// Package expert 实现「专家写源」的逐字段实测：把规则里每个显式声明的截取字段
// 拿到真实页面样本上跑一遍 xbpq 截取引擎，看它到底截不截得出值、值像不像样。
// 与 fingerprint.Check 互补——Check 管静态格式（锚点是否存在、语法是否合法），
// 本包管提取语义实测（跑引擎取值），两者合并作为专家模式回喂 AI 的失败证据。
package expert

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/wrxbpq/wrxbpq/internal/ai"
	"github.com/wrxbpq/wrxbpq/internal/fingerprint"
	"github.com/wrxbpq/wrxbpq/internal/xbpq"
)

// Issue 是一条字段实测结果：Problem 为空表示实测通过，Detail 给出实测证据。
type Issue struct {
	Field   string
	Problem string
	Detail  string
}

// OK 表示该字段实测通过。
func (i Issue) OK() bool { return i.Problem == "" }

// hrefPattern 条目链接形态：href="..."。
var hrefPattern = regexp.MustCompile(`href="[^"]+"`)

// Audit 对规则的每个显式字段在样本上做截取实测，返回逐字段结果（含通过项，供证据展示）。
// ruleText 无法解析为规则时只回一条「整体」失败。
func Audit(ruleText string, samples []ai.Sample) []Issue {
	rule, ok := xbpq.ParseRule(ruleText)
	if !ok {
		return []Issue{{Field: "整体", Problem: "不是合法 XBPQ 规则 JSON，无法逐字段实测"}}
	}
	declared := rule.DeclaredFields()
	catalog := pickSample(samples, "分类页")
	home := pickSample(samples, "首页")
	if catalog == "" {
		catalog = home
	}
	detail := pickSample(samples, "详情页")
	play := pickSample(samples, "播放页")

	var issues []Issue
	add := func(field string, problem string, format string, args ...any) {
		item := Issue{Field: field, Problem: problem}
		if format != "" {
			item.Detail = fmt.Sprintf(format, args...)
		}
		issues = append(issues, item)
	}

	// ---- 目录链（在分类页样本上实测） ----
	entries := []string(nil)
	if declared["数组"] != "" && catalog != "" {
		pattern := declared["数组"]
		if !hasAnchor(pattern) || strings.HasPrefix(pattern, "j:") || strings.HasPrefix(pattern, "p:") {
			add("数组", "", "非截取形态（j:/p:），跳过静态实测")
		} else if segments := xbpq.List(catalog, pattern); len(segments) == 0 {
			add("数组", "在分类页样本里一段条目都截不出来", "pattern=%q 执行 List 返回 0 段", clip(pattern, 80))
		} else {
			usable := 0
			for _, segment := range segments {
				if hrefPattern.MatchString(segment) {
					usable++
				}
			}
			if usable == 0 {
				add("数组", fmt.Sprintf("截出 %d 段，但没有一段含 href 链接——锚点框住的不是条目（多半框进了导航/筛选区，或起始锚点吃掉了条目标签开头）", len(segments)),
					"pattern=%q 截出 %d 段", clip(pattern, 80), len(segments))
			} else {
				entries = segments
				add("数组", "", "分类页实测截出 %d 段条目，其中 %d 段含链接", len(segments), usable)
			}
		}
	}
	for _, field := range []string{"二次截取", "标题", "链接", "副标题", "列表图片", "搜索图片"} {
		pattern := declared[field]
		if pattern == "" || strings.HasPrefix(pattern, "j:") || strings.HasPrefix(pattern, "p:") ||
			strings.HasPrefix(pattern, "jsoup:") || !strings.Contains(pattern, "&&") {
			continue
		}
		source := catalog
		if field == "二次截取" {
			// 二次截取 框的是页面主体区域，整页上验出现次数
			if count := anchorCount(source, pattern); count == 0 {
				add(field, "起始锚点在分类页样本里一字未现", "pattern=%q", clip(pattern, 80))
			} else {
				add(field, "", "锚点在分类页出现 %d 处", count)
			}
			continue
		}
		scope := entries
		if len(scope) == 0 {
			scope = []string{source}
		}
		got, samples := cutValues(scope, pattern, 3)
		switch field {
		case "链接":
			if got == 0 {
				add(field, fmt.Sprintf("在 %d 个条目段里一次都截不出链接", len(scope)), "pattern=%q", clip(pattern, 80))
			} else if bareRatioBad(samples) {
				add(field, "截出的链接是裸 ID（无 / 无域名），拼不成可跳转地址——把路径前缀从锚点里去掉，用 \"href=\\\"&&\\\"\" 截完整路径", "样例值：%s", joinQuotes(samples))
			} else {
				add(field, "", "%d/%d 段截出链接，样例：%s", got, len(scope), joinQuotes(samples))
			}
		case "列表图片", "搜索图片":
			if got == 0 {
				add(field, fmt.Sprintf("在 %d 个条目段里一次都截不出图片地址（懒加载占位坑：锚点别只写 src，检查 data-original/data-src）", len(scope)), "pattern=%q", clip(pattern, 80))
			} else {
				add(field, "", "%d/%d 段截出图片，样例：%s", got, len(scope), joinQuotes(samples))
			}
		case "标题", "副标题":
			if got == 0 {
				// 副标题是角标（集数/评分），部分条目没有属正常；标题截不出必坏。
				if field == "标题" {
					add(field, fmt.Sprintf("在 %d 个条目段里一次都截不出标题", len(scope)), "pattern=%q", clip(pattern, 80))
				} else {
					add(field, "", "该样本段截不出副标题（角标可能只在部分条目出现），不判失败")
				}
			} else {
				add(field, "", "%d/%d 段截出值，样例：%s", got, len(scope), joinQuotes(samples))
			}
		}
	}

	// ---- 播放链（在详情页样本上实测） ----
	segments := []string(nil)
	containerField := ""
	container := declared["播放数组"]
	if len(container) == 0 {
		container = declared["线路数组"]
		if container != "" {
			containerField = "线路数组"
		}
	} else {
		containerField = "播放数组"
	}
	if container != "" && detail != "" && !strings.HasPrefix(container, "j:") && !strings.HasPrefix(container, "p:") && strings.Contains(container, "&&") {
		segments = xbpq.List(detail, container)
		if len(segments) == 0 {
			add(containerField, "在详情页样本里一段播放容器都截不出来", "pattern=%q 执行 List 返回 0 段", clip(container, 80))
		} else {
			withLink := 0
			for _, segment := range segments {
				if hrefPattern.MatchString(segment) {
					withLink++
				}
			}
			if withLink == 0 {
				add(containerField, fmt.Sprintf("截出 %d 段但没有一段含分集链接——锚点框到了线路切换按钮/标题行而不是分集容器", len(segments)), "pattern=%q", clip(container, 80))
			} else {
				add(containerField, "", "详情页实测截出 %d 段容器，其中 %d 段含分集链接", len(segments), withLink)
			}
		}
	}
	// 播放列表（分隔符）：模拟真 jar 切分集
	if detail != "" && container != "" && len(segments) > 0 {
		jarSplit := "#"
		if value := declared["播放列表"]; value != "" && !strings.Contains(value, "&&") {
			jarSplit = value
		}
		worstReal, worstSplit := 0, 0
		for _, segment := range segments {
			real := strings.Count(segment, `href="`) + strings.Count(segment, "href='")
			if real == 0 {
				continue
			}
			splitCount := len(strings.Split(segment, jarSplit))
			if real > worstReal {
				worstReal, worstSplit = real, splitCount
			}
		}
		if worstReal >= 2 && worstSplit < worstReal {
			field := "播放列表"
			if declared["播放列表"] == "" {
				field = "播放列表(缺省)"
				add(field, fmt.Sprintf("规则没写 播放列表——真 jar 默认按 # 切，本组分集 %d 条链接只切出 %d 段，整段会被当 1 集。按分集结构写真实分隔串（<li> 连排写 </li>，<a> 连排写 </a>）", worstReal, worstSplit), "")
			} else {
				add(field, fmt.Sprintf("分隔符切不开：分集链接 %d 条但只切出 %d 段，真机每线路只剩少数集", worstReal, worstSplit), "分隔符=%q", clip(jarSplit, 40))
			}
		} else if worstReal >= 2 {
			add("播放列表", "", "按分隔符实测每线路可切出 ≥%d 段，与页面分集数一致", worstSplit)
		}
	}
	for _, field := range []string{"播放标题", "播放链接"} {
		pattern := declared[field]
		if pattern == "" || strings.HasPrefix(pattern, "j:") || strings.HasPrefix(pattern, "p:") || !strings.Contains(pattern, "&&") {
			continue
		}
		scope := segments
		if len(scope) == 0 {
			scope = []string{detail}
		}
		got, values := cutValues(scope, pattern, 3)
		if got == 0 {
			add(field, fmt.Sprintf("在 %d 段播放容器里一次都截不出值", len(scope)), "pattern=%q", clip(pattern, 80))
		} else {
			add(field, "", "%d/%d 段截出值，样例：%s", got, len(scope), joinQuotes(values))
		}
	}
	if pattern := declared["跳转播放链接"]; pattern != "" && play != "" &&
		strings.Contains(pattern, "&&") && !strings.HasPrefix(pattern, "j:") {
		value := strings.TrimSpace(xbpq.CutOnce(play, pattern))
		if value == "" {
			add("跳转播放链接", "在播放页样本里截不出任何内容", "pattern=%q", clip(pattern, 80))
		} else if !strings.HasPrefix(value, "http") && !strings.Contains(value, ".m3u8") && !strings.Contains(value, ".mp4") &&
			!strings.HasPrefix(value, "/") {
			add("跳转播放链接", "截出的不是可播放地址（不 http 开头、不含 m3u8/mp4）——锚点吃到了配置名或相邻脚本，加长上下文", "截出值：%q", clip(value, 120))
		} else {
			add("跳转播放链接", "", "播放页实测截出地址：%s", clip(value, 120))
		}
	}

	// ---- 合并格式自检：Check 的确定性格式问题直接进失败列表，通过项不混入 ----
	for _, issue := range fingerprint.Check(ruleText, samples) {
		issues = append(issues, Issue{Field: issue.Field, Problem: issue.Problem})
	}
	return issues
}

// FailureMessages 把未通过实测的字段转成回喂 AI 的结构化证据。
func FailureMessages(issues []Issue) []ai.ExpertIssue {
	var out []ai.ExpertIssue
	for _, issue := range issues {
		if issue.OK() {
			continue
		}
		text := issue.Problem
		if issue.Detail != "" {
			text += "（" + issue.Detail + "）"
		}
		out = append(out, ai.ExpertIssue{Field: issue.Field, Problem: text})
	}
	return out
}

// EvidenceText 生成给前端展示的逐字段实测报告。
func EvidenceText(issues []Issue) string {
	var builder strings.Builder
	for _, issue := range issues {
		if issue.OK() {
			builder.WriteString("✓ " + issue.Field)
			if issue.Detail != "" {
				builder.WriteString(" — " + issue.Detail)
			}
		} else {
			builder.WriteString("✗ " + issue.Field + " — " + issue.Problem)
			if issue.Detail != "" {
				builder.WriteString("（" + issue.Detail + "）")
			}
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

// pickSample 按 label 前缀取第一份匹配样本。
func pickSample(samples []ai.Sample, label string) string {
	for _, sample := range samples {
		if strings.HasPrefix(sample.Label, label) {
			return sample.Content
		}
	}
	return ""
}

// hasAnchor 判断 pattern 是否含可用的截取锚点形态。
func hasAnchor(pattern string) bool {
	return strings.Contains(pattern, "&&")
}

// anchorCount 统计 pattern 起始锚点在文本里的出现次数（取第一组备选、&& 前段）。
func anchorCount(text, pattern string) int {
	start := firstAnchor(pattern)
	if start == "" {
		return 0
	}
	return strings.Count(text, start)
}

// cutValues 在 scope 各段里执行 pattern，返回命中段数与前几个非空样例值（清洗后）。
func cutValues(scope []string, pattern string, limit int) (int, []string) {
	got := 0
	var values []string
	for _, segment := range scope {
		value := strings.TrimSpace(xbpq.CutOnce(segment, pattern))
		value = xbpq.CleanText(value)
		if value == "" {
			continue
		}
		got++
		if len(values) < limit {
			values = append(values, clip(value, 40))
		}
	}
	return got, values
}

// bareRatioBad 判断取出的链接样例是否「全是裸 ID」（无 / 无 http）。
func bareRatioBad(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if strings.HasPrefix(value, "http") || strings.HasPrefix(value, "/") {
			return false
		}
	}
	return true
}

func joinQuotes(values []string) string {
	var parts []string
	for _, value := range values {
		parts = append(parts, `"`+value+`"`)
	}
	return strings.Join(parts, ", ")
}

// firstAnchor 取 pattern 第一组备选的起始锚点（|| 分组、&& 前段、去修饰符尾巴）。
func firstAnchor(pattern string) string {
	first := strings.SplitN(pattern, "||", 2)[0]
	if index := strings.Index(first, "&&"); index >= 0 {
		first = first[:index]
	}
	if index := strings.Index(first, "["); index >= 0 {
		first = first[:index]
	}
	return strings.TrimSpace(first)
}

func clip(value string, limit int) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	runes := []rune(value)
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

// ExtractJSON 从模型回复里剥离 Markdown 代码块与前后废话，取出 JSON 对象/数组文本。
// 与 httpapi.ExtractRule 同逻辑，但独立实现避免 expert ← httpapi 反向依赖成环。
func ExtractJSON(content string) string {
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
	return strings.TrimSpace(value[start : end+1])
}

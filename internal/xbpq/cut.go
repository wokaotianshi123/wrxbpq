package xbpq

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// step 是一次 start&&end 截取及其修饰符。
type step struct {
	start       string
	end         string
	contains    []string
	notContains []string
	replaces    [][2]string
	order       []string // [排序:a>b>c]，按关键词给条目排优先级
	index       int      // [含序号:n]，1 起；0 表示不启用
}

var modifierKeyword = regexp.MustCompile(`^(包含|不包含|替换|序号|含序号|截右|右截|排序)$`)

// splitUnescaped 按分隔符切分，但跳过被反斜杠 \ 转义的分隔符（XBPQ 转义符语义）。
func splitUnescaped(s, sep string) []string {
	if sep == "" {
		return []string{s}
	}
	var out []string
	var cur strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) {
			// 保留转义对，后续 unescape 再处理
			cur.WriteByte(s[i])
			cur.WriteByte(s[i+1])
			i += 2
			continue
		}
		if strings.HasPrefix(s[i:], sep) {
			out = append(out, cur.String())
			cur.Reset()
			i += len(sep)
			continue
		}
		cur.WriteByte(s[i])
		i++
	}
	out = append(out, cur.String())
	return out
}

// unescape 去掉 XBPQ 连接符转义：\$ \# \& \* \[ \] \\ → 原字符。
func unescape(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '$', '#', '&', '*', '[', ']', '\\':
				b.WriteByte(s[i+1])
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// anchor 把截取锚点（可能含 * 通配符与 \ 转义）编译成惰性正则并缓存。
type anchor struct {
	re      *regexp.Regexp
	literal string // 无通配符时的纯字面串（快路径）
}

var anchorCache sync.Map

func compileAnchor(pattern string) anchor {
	if a, ok := anchorCache.Load(pattern); ok {
		return a.(anchor)
	}
	a := buildAnchor(pattern)
	anchorCache.Store(pattern, a)
	return a
}

func buildAnchor(pattern string) anchor {
	// 无 * 且无 \：纯字面。
	if !strings.ContainsAny(pattern, "*\\") {
		return anchor{literal: pattern}
	}
	var b strings.Builder
	b.WriteString(`(?s)`)
	for i := 0; i < len(pattern); {
		c := pattern[i]
		if c == '\\' && i+1 < len(pattern) {
			b.WriteString(regexp.QuoteMeta(string(pattern[i+1])))
			i += 2
			continue
		}
		if c == '*' {
			b.WriteString(`.*?`) // 惰性：一个字段仅一个通配符
			i++
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(c)))
		i++
	}
	re, err := regexp.Compile(b.String())
	if err != nil {
		return anchor{literal: unescape(pattern)}
	}
	return anchor{re: re}
}

// matchStart 返回 source 中匹配锚点的「结束偏移」（即从何处开始截取），找不到返回 -1。
func matchStart(a anchor, source string) int {
	if a.literal != "" && a.re == nil {
		index := strings.Index(source, a.literal)
		if index < 0 {
			return -1
		}
		return index + len(a.literal)
	}
	loc := a.re.FindStringIndex(source)
	if loc == nil {
		return -1
	}
	return loc[1]
}

// matchStartFrom 从 offset 起匹配锚点，返回 (内容起始, 匹配结束)；找不到 (−1, −1)。
func matchStartFrom(a anchor, source string, offset int) (int, int) {
	if offset > len(source) {
		return -1, -1
	}
	sub := source[offset:]
	if a.literal != "" && a.re == nil {
		index := strings.Index(sub, a.literal)
		if index < 0 {
			return -1, -1
		}
		return offset + index, offset + index + len(a.literal)
	}
	loc := a.re.FindStringIndex(sub)
	if loc == nil {
		return -1, -1
	}
	return offset + loc[0], offset + loc[1]
}

// matchEnd 在 source 中找结束锚点的起始偏移（结束锚点同样支持 * 通配）。
func matchEnd(a anchor, source string) int {
	if a.literal != "" && a.re == nil {
		return strings.Index(source, a.literal)
	}
	loc := a.re.FindStringIndex(source)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// trimModifier 从 token 尾部剥出 [修饰符] 列表，返回剩余字面与修饰符。
func trimModifier(token string) (string, []step) {
	var mods []step
	work := token
	for {
		if !strings.HasSuffix(work, "]") {
			break
		}
		open := matchBracket(work)
		if open < 0 {
			break
		}
		body := work[open+1 : len(work)-1]
		colon := strings.IndexAny(body, ":：")
		word := body
		payload := ""
		if colon >= 0 {
			word, payload = body[:colon], body[colon+1:]
		}
		word = strings.TrimSpace(word)
		if !modifierKeyword.MatchString(word) {
			break
		}
		mod := step{}
		if len(mods) > 0 {
			mod = mods[0]
		}
		switch word {
		case "包含":
			mod.contains = append(mod.contains, splitList(payload)...)
		case "不包含":
			mod.notContains = append(mod.notContains, splitList(payload)...)
		case "替换":
			for _, pair := range strings.Split(payload, "#") {
				before, after, found := strings.Cut(pair, ">>")
				if found {
					mod.replaces = append(mod.replaces, [2]string{before, after})
				}
			}
		case "序号", "含序号":
			if number, err := strconv.Atoi(strings.TrimSpace(payload)); err == nil && number >= 1 {
				mod.index = number
			}
		case "排序":
			for _, item := range splitUnescaped(payload, ">") {
				if item = strings.TrimSpace(item); item != "" {
					mod.order = append(mod.order, item)
				}
			}
		default:
			mod.index = -1
			open = -1
		}
		if open < 0 {
			break
		}
		work = strings.TrimSpace(work[:open])
		mods = append([]step{mod}, mods...)
	}
	return work, mods
}

// matchBracket 从右往左找与末尾 ] 配对的 [（不嵌套，简单取最右）。
func matchBracket(token string) int {
	for index := len(token) - 2; index >= 0; index-- {
		switch token[index] {
		case ']':
			return -1
		case '[':
			return index
		}
	}
	return -1
}

func splitList(payload string) []string {
	var out []string
	for _, item := range strings.Split(payload, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// parseSteps 把「数组/列表」形态的 pattern 拆成截取步骤链。
// 分隔符 && 支持 \ 转义（转义后视为字面 &，不作分隔）。
func parseSteps(pattern string) []step {
	var steps []step
	parts := splitUnescaped(pattern, "&&")
	if len(parts) == 1 {
		return nil
	}
	for i := 0; i < len(parts); i++ {
		startToken, startMods := trimModifier(strings.TrimSpace(parts[i]))
		end := ""
		endMods := []step{}
		if i+1 < len(parts) {
			endRaw := strings.TrimSpace(parts[i+1])
			end, endMods = trimModifier(endRaw)
		}
		one := step{start: unescape(startToken), end: unescape(end)}
		if len(startMods) > 0 {
			one = startMods[0]
			one.start, one.end = unescape(startToken), unescape(end)
			if len(endMods) > 0 {
				one.contains = append(one.contains, endMods[0].contains...)
				one.notContains = append(one.notContains, endMods[0].notContains...)
				one.replaces = append(one.replaces, endMods[0].replaces...)
				one.order = append(one.order, endMods[0].order...)
				if endMods[0].index != 0 && one.index == 0 {
					one.index = endMods[0].index
				}
			}
		} else if len(endMods) > 0 {
			one = endMods[0]
			one.start, one.end = unescape(startToken), unescape(end)
		}
		steps = append(steps, one)
		i++
	}
	return steps
}

// concatCut 处理 + 拼接：把 pattern 按未转义的 + 拆成若干段，
// 含 && 的段作为截取指令，不含 && 的段作为字面量，顺序拼接结果。
// 若 pattern 不含 + 或不含 &&（即纯截取），返回 ok=false 交由常规截取逻辑。
// splitTopLevelPlus 按 + 切分，但跳过 [修饰符] 方括号内部与被转义的 +。
// 返回 nil 表示没有可切分的顶层 +（不是拼接形态）。
func splitTopLevelPlus(pattern string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	hasPlus := false
	for i := 0; i < len(pattern); {
		if pattern[i] == '\\' && i+1 < len(pattern) {
			cur.WriteByte(pattern[i])
			cur.WriteByte(pattern[i+1])
			i += 2
			continue
		}
		switch pattern[i] {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '+':
			if depth == 0 {
				out = append(out, cur.String())
				cur.Reset()
				hasPlus = true
				i++
				continue
			}
		}
		cur.WriteByte(pattern[i])
		i++
	}
	if !hasPlus {
		return nil
	}
	out = append(out, cur.String())
	return out
}

func concatCut(source, pattern string) (string, bool) {
	if !strings.Contains(pattern, "+") {
		return "", false
	}
	// 拼接段中必须至少一段是截取指令（含 && 或 j: 路径），否则视为普通 pattern。
	segments := splitTopLevelPlus(pattern)
	if segments == nil {
		return "", false
	}
	if len(segments) < 2 {
		return "", false
	}
	fromJSON := jsonLikely(source)
	hasCut := false
	for _, s := range segments {
		if strings.Contains(s, "&&") || (fromJSON && isJSONPattern(strings.TrimSpace(s))) {
			hasCut = true
		}
	}
	if !hasCut {
		return "", false
	}
	var b strings.Builder
	for _, s := range segments {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		// 单引号包裹的是字面量段（json 模式拼接约定）。
		if len(s) >= 2 && strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") {
			b.WriteString(s[1 : len(s)-1])
			continue
		}
		if strings.Contains(s, "&&") {
			b.WriteString(CutOnce(source, s))
		} else if fromJSON && isJSONPattern(s) {
			b.WriteString(jsonStringValue(source, s))
		} else {
			b.WriteString(unescape(s))
		}
	}
	return b.String(), true
}

// CutOnce 按 pattern 在 source 上截取第一段。pattern 多组用 || 分隔时依次尝试。
// 支持 json 模式（j: 前缀或点路径，笔记 item 7）与 Base64 解码（item 8）。
func CutOnce(source, pattern string) string {
	if source == "" || pattern == "" {
		return ""
	}
	if strings.HasPrefix(pattern, "p:") || strings.HasPrefix(pattern, "jsoup:") {
		return SelectorFirstString(source, pattern)
	}
	// 二次截取填 "Base64"：整段只解码不截取（笔记 item 8）。
	if strings.EqualFold(strings.TrimSpace(pattern), "Base64") {
		return decodeBase64Segment(source)
	}
	// Base64(a&&b)：截取结果再做 Base64 解码。
	if inner, ok := base64Wrapper(pattern); ok {
		return decodeBase64Segment(CutOnce(source, inner))
	}
	if value, ok := concatCut(source, pattern); ok {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	if isJSONPattern(pattern) && jsonLikely(source) {
		return jsonStringValue(source, pattern)
	}
	// 笔记 item 1：不使用 && 的字段值是「指定字符串」（固定标题/图片/线路标题等）。
	// 含 || 的多组备选不在此列；j:/点路径除外——它在非 JSON 源上应算取不到。
	if !isJSONPattern(pattern) && len(splitUnescaped(pattern, "&&")) == 1 && strings.Count(strings.ReplaceAll(pattern, `\||`, ""), "||") == 0 {
		return unescape(strings.TrimSpace(pattern))
	}
	for _, part := range splitUnescaped(pattern, "||") {
		steps := parseSteps(part)
		if len(steps) == 0 {
			continue
		}
		segment := source
		ok := true
		for _, one := range steps {
			segment, ok = applyStepOnce(segment, one)
			if !ok {
				break
			}
		}
		if ok && strings.TrimSpace(segment) != "" {
			return segment
		}
	}
	return ""
}

func applyStepOnce(source string, one step) (string, bool) {
	if one.start == "" && one.end == "" {
		return source, true
	}
	rest := source
	if one.start != "" {
		pos := matchStart(compileAnchor(one.start), rest)
		if pos < 0 {
			return "", false
		}
		rest = rest[pos:]
	}
	if one.end != "" {
		index := matchEnd(compileAnchor(one.end), rest)
		if index < 0 {
			return "", false
		}
		rest = rest[:index]
	}
	return finishSegment(rest, one), true
}

func finishSegment(segment string, one step) string {
	if one.index > 0 {
		matches := splitNumbered(segment)
		if one.index <= len(matches) {
			segment = matches[one.index-1]
		}
	}
	for _, word := range one.contains {
		if !strings.Contains(segment, word) {
			return ""
		}
	}
	for _, word := range one.notContains {
		if strings.Contains(segment, word) {
			return ""
		}
	}
	for _, pair := range one.replaces {
		segment = strings.ReplaceAll(segment, pair[0], pair[1])
	}
	return strings.TrimSpace(segment)
}

func splitNumbered(segment string) []string {
	return strings.FieldsFunc(segment, func(r rune) bool { return r == '\n' || r == '\t' })
}

// List 按数组 pattern 迭代抽取全部条目（列表层）。支持 j: json 路径数组。
func List(source, pattern string) []string {
	if source == "" || pattern == "" {
		return nil
	}
	if strings.HasPrefix(pattern, "j:") && jsonLikely(source) {
		return jsonArrayItems(source, pattern)
	}
	var items []string
	for _, part := range splitUnescaped(pattern, "||") {
		steps := parseSteps(part)
		if len(steps) == 0 {
			continue
		}
		first := steps[0]
		rest := source
		for {
			segment, consumed, ok := applyStepScan(rest, first)
			if !ok {
				break
			}
			if len(steps) > 1 {
				for _, one := range steps[1:] {
					segment, ok = applyStepOnce(segment, one)
					if !ok {
						segment = ""
						break
					}
				}
			}
			if segment != "" {
				items = append(items, segment)
			}
			if consumed >= len(rest) {
				break
			}
			rest = rest[consumed:]
		}
		if len(items) > 0 {
			if len(first.order) > 0 {
				items = orderItems(items, first.order, first)
			}
			break
		}
	}
	return items
}

// orderItems 按 [排序:a>b>c] 的关键词优先级重排条目：含靠前关键词的排前面。
func orderItems(items []string, order []string, one step) []string {
	rank := func(item string) int {
		for index, keyword := range order {
			if strings.Contains(item, keyword) {
				return index
			}
		}
		return len(order)
	}
	out := append([]string{}, items...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && rank(out[j]) < rank(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// applyStepScan 找第一个 start&&end 段；返回段内容与「消费到的绝对偏移」。
func applyStepScan(source string, one step) (string, int, bool) {
	if one.start == "" {
		return "", 0, false
	}
	startAnchor := compileAnchor(one.start)
	endAnchor := compileAnchor(one.end)
	offset := 0
	for offset <= len(source) {
		contentStart, matchEndOffset := matchStartFrom(startAnchor, source, offset)
		if matchEndOffset < 0 {
			return "", 0, false
		}
		body := source[matchEndOffset:]
		stop := len(source)
		if one.end != "" {
			endIndex := matchEnd(endAnchor, body)
			if endIndex < 0 {
				// 本处 start 无对应 end：从 start 之后继续找下一个 start
				offset = contentStart + 1
				continue
			}
			body = body[:endIndex]
			stop = matchEndOffset + endIndex
		}
		segment := finishSegment(body, one)
		if segment == "" {
			offset = contentStart + 1
			continue
		}
		return segment, stop, true
	}
	return "", 0, false
}

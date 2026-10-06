package xbpq

import (
	"regexp"
	"strconv"
	"strings"
)

// step 是一次 start&&end 截取及其修饰符。
type step struct {
	start       string
	end         string
	contains    []string
	notContains []string
	replaces    [][2]string
	index       int // [含序号:n]，1 起；0 表示不启用
}

var modifierKeyword = regexp.MustCompile(`^(包含|不包含|替换|序号|含序号|截右|右截)$`)

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
func parseSteps(pattern string) []step {
	var steps []step
	parts := strings.Split(pattern, "&&")
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
		one := step{start: startToken, end: end}
		if len(startMods) > 0 {
			one = startMods[0]
			one.start, one.end = startToken, end
			if len(endMods) > 0 {
				one.contains = append(one.contains, endMods[0].contains...)
				one.notContains = append(one.notContains, endMods[0].notContains...)
				one.replaces = append(one.replaces, endMods[0].replaces...)
				if endMods[0].index != 0 && one.index == 0 {
					one.index = endMods[0].index
				}
			}
		} else if len(endMods) > 0 {
			one = endMods[0]
			one.start, one.end = startToken, end
		}
		steps = append(steps, one)
		i++
	}
	return steps
}

// CutOnce 按 pattern 在 source 上截取第一段。pattern 多组用 || 分隔时依次尝试。
func CutOnce(source, pattern string) string {
	if source == "" || pattern == "" {
		return ""
	}
	if strings.HasPrefix(pattern, "p:") || strings.HasPrefix(pattern, "jsoup:") {
		return SelectorFirstString(source, pattern)
	}
	for _, part := range strings.Split(pattern, "||") {
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
		index := strings.Index(rest, one.start)
		if index < 0 {
			return "", false
		}
		rest = rest[index+len(one.start):]
	}
	if one.end != "" {
		index := strings.Index(rest, one.end)
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

// List 按数组 pattern 迭代抽取全部条目（列表层）。
func List(source, pattern string) []string {
	if source == "" || pattern == "" {
		return nil
	}
	var items []string
	for _, part := range strings.Split(pattern, "||") {
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
			break
		}
	}
	return items
}

// applyStepScan 找第一个 start&&end 段；返回段内容与「消费到的绝对偏移」。
func applyStepScan(source string, one step) (string, int, bool) {
	if one.start == "" {
		return "", 0, false
	}
	index := strings.Index(source, one.start)
	if index < 0 {
		return "", 0, false
	}
	body := source[index+len(one.start):]
	stop := len(source)
	if one.end != "" {
		endIndex := strings.Index(body, one.end)
		if endIndex < 0 {
			return "", 0, false
		}
		body = body[:endIndex]
		stop = index + len(one.start) + endIndex
	}
	segment := finishSegment(body, one)
	if segment == "" {
		if next := strings.Index(source[index+1:], one.start); next >= 0 {
			skip := index + 1 + next
			innerSegment, innerStop, ok := applyStepScan(source[skip:], one)
			return innerSegment, skip + innerStop, ok
		}
		return "", stop, false
	}
	return segment, stop, true
}

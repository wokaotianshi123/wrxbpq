package xbpq

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
)

// jsonPathValue 按 XBPQ json 模式（笔记 item 7）从 JSON 文本里取值。
// 路径语法：data.list[0].name —— 点号分隔对象键，方括号取数组下标。
// 与笔记「最小下标为 1」不同，实测规则（data.list[0]/data.urls[0]）与底层
// fastjson JSONPath 都是 0-based，这里统一按 0-based 解析，兼容 [0] 取首元素。
// 返回 (值, 是否为数组切片/完整数组)。数组场景上层需要逐元素迭代。
func jsonPathValue(source, path string) (any, bool) {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, "j:")
	if source == "" || path == "" {
		return nil, false
	}
	var root any
	if err := json.Unmarshal([]byte(source), &root); err != nil {
		return nil, false
	}
	// 允许 "data.list" 或 data.list[0].name；逐个段前进。
	cur := root
	for _, rawSeg := range strings.Split(path, ".") {
		key, idxText, hasIndex := cutSegment(rawSeg)
		if key != "" {
			obj, ok := cur.(map[string]any)
			if !ok {
				// 数组上直接取键：对每个元素取键（XBPQ 允许 list.name 形式）。
				if arr, isArr := cur.([]any); isArr {
					var picked []any
					for _, one := range arr {
						if o, ok := one.(map[string]any); ok {
							if v, exists := lookupFold(o, key); exists {
								picked = append(picked, v)
							}
						}
					}
					// 键在元素里也不存在（数组已由 二次截取 缩小过，如
					// SVIP 规则 二次截取=j:data.list + 数组=j:list）：
					// 视为对数组本身继续处理。
					if len(picked) == 0 {
						continue
					}
					cur = picked
					continue
				}
				return nil, false
			}
			v, exists := lookupFold(obj, key)
			if !exists {
				return nil, false
			}
			cur = v
		}
		if hasIndex {
			cur = applyIndex(cur, idxText)
		}
	}
	return cur, true
}

// lookupFold 大小写无关地查键（json 字段名常大小写混用）。
func lookupFold(obj map[string]any, key string) (any, bool) {
	if v, ok := obj[key]; ok {
		return v, true
	}
	for k, v := range obj {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return nil, false
}

// cutSegment 拆 "list[0]" → key="list", index="0"；"list" → 无下标；"list[]" → index=""。
func cutSegment(seg string) (key, index string, hasIndex bool) {
	open := strings.Index(seg, "[")
	if open < 0 {
		return seg, "", false
	}
	key = seg[:open]
	close := strings.LastIndex(seg, "]")
	if close <= open {
		return seg, "", false
	}
	return key, seg[open+1 : close], true
}

// applyIndex 对数组应用下标/切片表达式（0-based）。
//   "" 或 "*"     → 原数组（完整数组）
//   "n"           → 第 n 个元素
//   "a,"          → 从 a 到末尾
//   ",b"          → 开头到 b（不含 b）
//   "a-b" 或 "a:b"→ [a,b) 切片
func applyIndex(value any, expr string) any {
	arr, ok := value.([]any)
	if !ok {
		return value
	}
	expr = strings.TrimSpace(expr)
	if expr == "" || expr == "*" {
		return arr
	}
	// 单下标（含负数 -1=倒数第一个）先于切片判断处理，避免 "-1" 被误当切片。
	if n, err := strconv.Atoi(expr); err == nil {
		if n < 0 {
			n += len(arr)
		}
		if n < 0 || n >= len(arr) {
			return nil
		}
		return arr[n]
	}
	// 切片形态："a," / ",b" / "a-b" / "a:b"
	if i := strings.IndexAny(expr, "-:,"); i >= 0 {
		left := strings.TrimSpace(expr[:i])
		right := strings.TrimSpace(expr[i+1:])
		start := 0
		end := len(arr)
		if left != "" {
			if n, err := strconv.Atoi(left); err == nil {
				start = clamp(n, 0, len(arr))
			}
		}
		if right != "" {
			if n, err := strconv.Atoi(right); err == nil {
				end = clamp(n, 0, len(arr))
			}
		}
		if start >= end {
			return []any{}
		}
		out := make([]any, end-start)
		copy(out, arr[start:end])
		return out
	}
	return arr
}

func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// jsonStringValue 把路径取到的值转成字符串标量。
func jsonStringValue(source, path string) string {
	value, ok := jsonPathValue(source, path)
	if !ok {
		return ""
	}
	return stringValue(value)
}

// jsonPathRaw 把路径取到的对象/数组序列化回 JSON 文本（用于「二次截取」缩小范围）。
// 取不到返回空串。
func jsonPathRaw(source, path string) string {
	value, ok := jsonPathValue(source, path)
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any, []any:
		body, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(body)
	default:
		return stringValue(value)
	}
}

// jsonLikely source 是否为 JSON 文档（对象或数组）。
func jsonLikely(source string) bool {
	for _, r := range source {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		return r == '{' || r == '['
	}
	return false
}

// jsonArrayItems 路径解析出数组时，返回每个元素的 JSON 文本（供逐条迭代）。
// 非数组时返回单元素切片（对象也能当一条，如 SVPI 线路数组=j:data.seriesInfo）。
func jsonArrayItems(source, path string) []string {
	value, ok := jsonPathValue(source, path)
	if !ok {
		return nil
	}
	arr, isArr := value.([]any)
	if !isArr {
		body, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		return []string{string(body)}
	}
	var out []string
	for _, one := range arr {
		body, err := json.Marshal(one)
		if err != nil {
			continue
		}
		out = append(out, string(body))
	}
	return out
}

// isJSONPattern pattern 是否 json 模式（j: 前缀或纯点路径 a.b.c）。
func isJSONPattern(pattern string) bool {
	if pattern == "" {
		return false
	}
	if strings.HasPrefix(pattern, "j:") || strings.HasPrefix(pattern, "$") {
		return true
	}
	// 纯点路径（不含 && / 引号 / http），视为 json 取值。
	if strings.ContainsAny(pattern, "&\"' ") || strings.Contains(pattern, "://") {
		return false
	}
	if strings.IndexByte(pattern, '.') < 0 {
		return false
	}
	for _, r := range pattern {
		if r == '[' || r == ']' {
			continue
		}
		if !(r == '_' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// decodeBase64Segment 处理 Base64 解码（笔记 item 8）：
// 二次截取填 "Base64" → 整段只解码；Base64(a&&b) → 对截取结果解码后使用。
func decodeBase64Segment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		if txt, ok := utf8Valid(decoded); ok {
			return txt
		}
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		if txt, ok := utf8Valid(decoded); ok {
			return txt
		}
	}
	return s
}

func utf8Valid(b []byte) (string, bool) {
	for _, c := range b {
		if c == 0 {
			return "", false
		}
	}
	return string(b), true
}

// base64Wrapper 识别 "Base64(inner)" 形态，返回 inner 与是否匹配。
func base64Wrapper(pattern string) (string, bool) {
	p := strings.TrimSpace(pattern)
	lower := strings.ToLower(p)
	if !strings.HasPrefix(lower, "base64(") || !strings.HasSuffix(p, ")") {
		return "", false
	}
	return p[len("Base64(") : len(p)-1], true
}

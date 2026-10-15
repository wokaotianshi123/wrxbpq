package httpapi

import (
	"encoding/json"
	"testing"
)

func TestExtractFieldFromJSON(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		field    string
		expected string
	}{
		{"单字段返回", `{"标题":"title=\"&&\""}`, "标题", `title="&&"`},
		{"完整规则含锚点", `{"分类url":"https://a.com/{cateId}-{catePg}.html","标题":"title=\"&&\""}`, "标题", `title="&&"`},
		// 别名归一：AI 用「图片」输出，请求的是「列表图片」
		{"别名匹配", `{"图片":"src=\"&&\""}`, "列表图片", `src="&&"`},
		// 数组形态 [{...}]
		{"数组形态", `[{"分类url":"https://a.com/x-{cateId}-{catePg}.html"},{"链接":"href=\"&&\""}]`, "链接", `href="&&"`},
		// markdown 代码块包裹
		{"代码块包裹", "```json\n{\"标题\":\"title=\\\"&&\\\"\"}\n```", "标题", `title="&&"`},
		// 没给该字段 -> 空
		{"缺字段", `{"分类url":"https://a.com/x.html"}`, "标题", ""},
		// 多余解释文字 + 字段在正文里
		{"前后有文字", "纠正后：{\"标题\":\"title=\\\"&&\\\"\"} 完成", "标题", `title="&&"`},
	}
	for _, c := range cases {
		// 真实链路：先 ExtractRule 去掉 ``` 围栏/前后解释文字，再取字段值。
		got := extractFieldFromJSON(ExtractRule(c.text), c.field)
		if got != c.expected {
			t.Errorf("%s: got %q, want %q", c.name, got, c.expected)
		}
	}
}

func TestReplaceFieldInRule(t *testing.T) {
	cases := []struct {
		name     string
		rule     string
		field    string
		value    string
		expected string
	}{
		{"覆盖已存在字段", `{"分类url":"old","标题":"a&&b"}`, "标题", "x&&y", `{"分类url":"old","标题":"x&&y"}`},
		{"新增缺失字段", `{"分类url":"old"}`, "链接", `href="&&"`, `{"分类url":"old","链接":"href=\"&&\""}`},
		// 别名键清理：原规则用别名「图片」，覆盖时应归一到「列表图片」且删掉别名键。
		{"别名键归并", `{"图片":"src=\"a&&b\""}`, "列表图片", `src="x&&y"`, `{"列表图片":"src=\"x&&y\""}`},
		{"非法原规则退化为新对象", `not-json`, "标题", "v", `{"标题":"v"}`},
	}
	for _, c := range cases {
		got := replaceFieldInRule(c.rule, c.field, c.value)
		// 反序列化比对，避免键序/转义差异造成的误报。
		var gm, em map[string]any
		if err := json.Unmarshal([]byte(got), &gm); err != nil {
			t.Errorf("%s: 结果不是合法 JSON: %s (%s)", c.name, got, err)
			continue
		}
		if err := json.Unmarshal([]byte(c.expected), &em); err != nil {
			t.Fatalf("%s: 期望不是合法 JSON", c.name)
		}
		if len(gm) != len(em) {
			t.Errorf("%s: 键数不符 got=%d want=%d (%s)", c.name, len(gm), len(em), got)
			continue
		}
		for k, ev := range em {
			if gm[k] != ev {
				t.Errorf("%s: 字段 %s got %q want %q (full=%s)", c.name, k, gm[k], ev, got)
			}
		}
	}
}


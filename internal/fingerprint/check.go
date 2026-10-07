package fingerprint

import (
	"strings"

	"github.com/wrxbpq/wrxbpq/internal/ai"
	"github.com/wrxbpq/wrxbpq/internal/xbpq"
)

// CheckIssue 是一条格式问题。
type CheckIssue struct {
	Field   string `json:"field"`
	Problem string `json:"problem"`
}

// Check 在验证之前对规则做纯格式自检（不联网），返回逐字段问题。
// 覆盖"AI 写了根本不可能的语法/格式"类确定性错误：缺 &&、字面 \" 转义、
// 锚点在样本里一字未现、线路数组与播放数组同锚点等。
func Check(ruleText string, samples []ai.Sample) []CheckIssue {
	rule, ok := xbpq.ParseRule(ruleText)
	if !ok {
		return []CheckIssue{{Field: "整体", Problem: "不是合法 XBPQ 规则 JSON"}}
	}
	var issues []CheckIssue
	add := func(field, problem string) {
		issues = append(issues, CheckIssue{Field: field, Problem: problem})
	}

	patternFields := []string{"数组", "二次截取", "标题", "链接", "列表图片", "搜索图片", "副标题",
		"播放数组", "播放标题", "播放链接", "跳转播放链接", "线路数组", "影片名称", "简介", "封面", "类型", "状态", "主演", "导演"}
	for _, name := range patternFields {
		pattern := rule.Field(name)
		if pattern == "" {
			continue
		}
		if !strings.Contains(pattern, "&&") && !strings.HasPrefix(pattern, "p:") && !strings.HasPrefix(pattern, "jsoup:") {
			add(name, "缺少 && 分隔符，XBPQ 截取语法必须是 start&&end（或单侧 A&& / &&B）")
		}
		if strings.Contains(pattern, "&&&") {
			add(name, "出现 &&& 三连串，若是多步链 start&&mid&&end 请确认中间锚点在页面里真实存在，否则删掉多余 &")
		}
		if strings.Contains(pattern, `\\"`) {
			add(name, `pattern 含字面 \" （反斜杠引号），JSON 解码后引号应为 " 本身，请写成 "href=\"&&\"" 的正常转义`)
		}
	}

	// 播放列表 是分隔符字段，允许 "<li>" 这类无 && 形态；单独校验其闭合合理性
	if list := rule.Field("播放列表"); strings.Contains(list, "&&") {
		add("播放列表", "播放列表 是分隔符不是截取串，直接写 <li> 或留空（默认 #），去掉 &&")
	}

	if route := rule.Field("线路数组"); route != "" {
		if play := rule.Field("播放数组"); play != "" && sameStart(route, play) {
			add("线路数组", "线路数组 与 播放数组 起始锚点相同：会把同一线路的每一行拆成重复\"线路\"，单线路站必须删除 线路数组（多线路的锚点应是每条线路的容器标题行）")
		}
	}

	// 锚点在样本里核实：字段 pattern 的起始锚点若所有样本文档一字未现 → 大概率是 AI 改写/记错了 HTML
	for _, name := range []string{"数组", "标题", "链接", "列表图片", "播放数组", "播放标题", "播放链接", "跳转播放链接"} {
		pattern := rule.Field(name)
		if pattern == "" || strings.HasPrefix(pattern, "p:") || strings.HasPrefix(pattern, "jsoup:") {
			continue
		}
		start := strings.TrimSpace(strings.SplitN(strings.SplitN(pattern, "&&", 2)[0], "||", 2)[0])
		if len([]rune(start)) < 4 {
			continue
		}
		if occurrencesInAll(samples, start) == 0 {
			add(name, "起始锚点 \""+clipToken(start, 48)+"\" 在页面样本里一字未现——pattern 必须逐字复制样本原文，不要改写属性顺序或删空格")
		}
	}

	return issues
}

func sameStart(a, b string) bool {
	cut := func(s string) string {
		if index := strings.Index(s, "&&"); index >= 0 {
			return strings.TrimSpace(s[:index])
		}
		return strings.TrimSpace(s)
	}
	return strings.TrimSuffix(cut(a), ">") == strings.TrimSuffix(cut(b), ">")
}

func occurrencesInAll(samples []ai.Sample, token string) int {
	total := 0
	for _, sample := range samples {
		total += strings.Count(sample.Content, token)
	}
	return total
}

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

	// 简写两条铁律（实测踩坑，确定性拦截）：
	// ① 省了 主页url 时 分类url 必须含域名——相对路径 jar 定位不到站点，直接识别失败；
	// ② 分类url 必须写分页占位 {catePg}——否则第 2 页和第 1 页是同一个 URL，paging 验证必挂。
	category := rule.Field("分类url", "分类Url")
	if rule.DeclaresField("分类url") || category != "" {
		if !strings.Contains(category, "{catePg}") {
			add("分类url", "缺少分页占位 {catePg}：翻页会一直停在第 1 页，验证 paging 步骤必挂。对照样本分页链接补上真实形态，如 /vodshow/{cateId}-{catePg}.html、…/type/{cateId}/{catePg}/…、查询串 …&pg={catePg}")
		}
		if !strings.HasPrefix(category, "http") && rule.Field("主页url", "首页url", "请求") == "" {
			add("分类url", "规则省略了 主页url，分类url 就必须写含域名的绝对地址（https://站点域名/…）——相对路径无法定位站点，XBPQ 识别失败")
		}
	}

	patternFields := []string{"数组", "二次截取", "标题", "链接", "列表图片", "搜索图片", "副标题",
		"播放数组", "播放标题", "播放链接", "跳转播放链接", "线路数组", "影片名称", "简介", "封面", "类型", "状态", "主演", "导演"}
	for _, name := range patternFields {
		pattern := rule.Field(name)
		if pattern == "" {
			continue
		}
		// json 模式（j: 前缀）不需要 && 与 HTML 锚点（笔记 item 7）。
		if strings.HasPrefix(pattern, "j:") {
			continue
		}
		if !strings.Contains(pattern, "&&") && !strings.HasPrefix(pattern, "p:") && !strings.HasPrefix(pattern, "jsoup:") {
			add(name, "缺少 && 分隔符，XBPQ 截取语法必须是 start&&end（或单侧 A&& / &&B）；JSON 接口站可用 j: 前缀 json 模式")
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
			// 多线路站里 线路数组==播放数组 是正确写法；只有确认单线路时才报"拆重复线路"。
			// 两路证据（与指纹判定一致）：①「播放线路 N」标题 ≥2；②把 线路数组 真喂给
			// 截取引擎，独立截出 ≥2 段且每段含 /play/ 链接。任一成立即视为多线路不误报。
			detail := pick(samples, "详情页")
			routeTitles := routeTitlePattern.FindAllString(detail, -1)
			containerRoutes := 0
			if detail != "" && !strings.HasPrefix(route, "j:") {
				for _, segment := range xbpq.List(detail, route) {
					if playHrefPattern.MatchString(segment) {
						containerRoutes++
					}
				}
			}
			if len(routeTitles) < 2 && containerRoutes < 2 {
				add("线路数组", "线路数组 与 播放数组 起始锚点相同且样本未见多线路（无「播放线路」标题、线路容器也独立截不出 ≥2 段）：会把同一线路的每一行拆成重复\"线路\"，单线路站必须删除 线路数组（多线路的锚点应是每条线路的容器标题行）")
			}
		}
	}

	// 提取合理性：数组+链接 组合喂给真实截取引擎，截出值"全是裸 ID"（无 / 无 http）说明
	// 锚点吃掉了路径前缀（如 href="/vod/&&.html"），拼出的链接全坏——静态锚点校验发现不了这种。
	if catalog := pick(samples, "分类页"); catalog != "" {
		if arr := rule.Field("数组"); arr != "" && !strings.HasPrefix(arr, "p:") && strings.Contains(arr, "&&") {
			entries := xbpq.List(catalog, arr)
			if len(entries) >= 3 {
				link := rule.Field("链接")
				if link != "" && !strings.HasPrefix(link, "p:") {
					bare, good := 0, 0
					for _, entry := range entries {
						value := strings.TrimSpace(xbpq.CutOnce(entry, link))
						if value == "" {
							continue
						}
						if strings.HasPrefix(value, "http") || (strings.HasPrefix(value, "/") && len(value) > 1) {
							good++
						} else {
							bare++
						}
					}
					if good == 0 && bare >= 3 {
						add("链接", "按 数组 截出的条目用 链接=\""+clipToken(link, 40)+"\" 只能取到裸 ID（如 55560），拼不成可跳转链接——把 /vod/ 之类路径前缀从锚点里去掉，逐字用 \"href=\\\"&&\\\"\"")
					}
				}
			} else if len(entries) == 0 {
				// 锚点存在（过了上面的逐字校验）但组合截不出条目
				add("数组", "起始锚点在样本存在，但 \""+clipToken(arr, 40)+"\" 在分类页样本里截不出任何条目：检查结束锚点是否把条目框断了，或改用样本中真实成对出现的边界")
			}
		}
	}

	// 锚点在样本里核实：字段 pattern 的起始锚点若所有样本文档一字未现 → 大概率是 AI 改写/记错了 HTML。
	// 模板补齐的字段（简写规则）跳过——模板默认锚点本来就不保证出现在本站样本里。
	for _, name := range []string{"数组", "标题", "链接", "列表图片", "播放数组", "播放标题", "播放链接", "跳转播放链接"} {
		if !rule.DeclaresField(name) {
			continue
		}
		pattern := rule.Field(name)
		if pattern == "" || strings.HasPrefix(pattern, "p:") || strings.HasPrefix(pattern, "jsoup:") || strings.HasPrefix(pattern, "j:") {
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

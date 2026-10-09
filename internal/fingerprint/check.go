package fingerprint

import (
	"fmt"
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

	// 简写三条铁律（实测踩坑，确定性拦截）：
	// ① 省了 主页url 时 分类url 必须含域名——相对路径 jar 定位不到站点，直接识别失败；
	// ② 分类url 必须写分页占位 {catePg}——否则第 2 页和第 1 页是同一个 URL，paging 验证必挂；
	// ③ 分类url 必须写分类占位 {cateId}——声明 ≥2 个分类时，缺了它所有分类都打开同一页，
	//    catalog 切分类必串档（单分类站 ID 不体现在 URL 形态属合法例外，不报）。
	category := rule.Field("分类url", "分类Url")
	if rule.DeclaresField("分类url") || category != "" {
		if !strings.Contains(category, "{catePg}") {
			add("分类url", "缺少分页占位 {catePg}：翻页会一直停在第 1 页，验证 paging 步骤必挂。{catePg} 的具体形态（路径段/文件名段/查询参数）必须由真实拼接抓取两页比对来确认——先看指纹「分页实测」结论并照抄；没有实测结论时按样本分页链接写最可信的一种，以 paging 验证通过为准")
		}
		if !strings.Contains(category, "{cateId}") && len(rule.Categories()) >= 2 {
			add("分类url", "缺少分类占位 {cateId}：规则声明了多个分类，分类url 里却没有 {cateId} 位置——切分类会永远打开同一个页面（catalog 串档）。把指纹「分类检测」标出的 {cateId} 实测位置（路径段或 query 参数）逐字放进 分类url 对应段，与 {catePg} 同时存在，二者缺一不可")
		}
		if !strings.HasPrefix(category, "http") && rule.Field("主页url", "首页url", "请求") == "" {
			add("分类url", "规则省略了 主页url，分类url 就必须写含域名的绝对地址（https://站点域名/…）——相对路径无法定位站点，XBPQ 识别失败")
		}
	}

	// 分类铁律（实测拦截）：指纹「分类检测」实测过哪些 ID 能出内容、哪些不能。
	// 规则显式写的 分类 字段若含"实测失败分类"的 ID → 该分类点进去是空页，直接报出。
	catView := parseCategoryFinding(pick(samples, "分类检测"))
	if (catView.Status == "通过" || catView.Status == "部分通过") && rule.DeclaresField("分类") {
		failedIDs := failedCategoryIDs(catView.Failed)
		if len(failedIDs) > 0 {
			for _, pair := range strings.Split(strings.TrimSpace(rule.Field("分类")), "#") {
				dollar := strings.LastIndex(pair, "$")
				if dollar < 0 {
					continue
				}
				name, id := pair[:dollar], pair[dollar+1:]
				if failedIDs[id] {
					add("分类", "分类「"+name+"$"+id+"」的 ID "+id+" 在「分类检测」里实测失败（真实抓取该分类页取不出条目）——从 分类 字段删掉它或换用「已实测分类串」里的 ID，不要把点了没内容的分类塞给用户")
				}
			}
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

	// 播放列表 是"分集分隔符"，引擎按它做 strings.Split。
	// 常见误区：写成 前缀&&后缀（那是截取串写法）——页面上找不到该字面量，Split 切不开，
	// 整个播放容器会被当成 1 集（实测：5 条线路×4 集的站只剩 1 集）。
	if list := rule.Field("播放列表"); strings.Contains(list, "&&") {
		add("播放列表", fmt.Sprintf("播放列表 是【分集分隔符】不是截取串，不能写成 前缀&&后缀——引擎按它做 Split，页面上找不到 %q 这个字面量，整个播放容器会被当成 1 集。改成样本里真实存在的分隔串：分集是 <li> 结构就写 </li>（用闭合标签；开标签常带属性或空格如 `<li >`，匹配不稳）；分集之间本就有 # 之类符号就写那个符号", list))
	}

	// 多线路识别（与指纹四路证据一致）：
	detail := pick(samples, "详情页")
	ev := routeEvidenceOf(detail)
	// 分隔符本身必须在详情页里真的出现，否则 Split 切不开同样只剩 1 集。
	// 典型踩坑：页面是 `<li >`（带空格）而规则写 `<li>`，看似"写对了"实则一次都匹配不到。
	if list := rule.Field("播放列表"); list != "" && !strings.Contains(list, "&&") && detail != "" && !strings.Contains(detail, list) {
		add("播放列表", fmt.Sprintf("播放列表 分隔符 %q 在详情页样本里一次都没出现——Split 切不开，分集只会剩 1 条。请从详情页原文里挑一个能把每条分集分开的真实串（分集是 <li> 结构时通常写 </li>）", list))
	}
	if route := rule.Field("线路数组"); route != "" {
		// 引擎语义：线路数组 截出的【每一段】= 该线路的分集容器（用 $$$ 拼接后按 播放列表 切分集）。
		// 因此它必须锚在分集列表容器上，锚成"线路切换按钮"（ewave-tab / hl-tabs-btn / dropdown 的 li）
		// 会让每条线路都是 0 集——这是最隐蔽的写法错误，静态看锚点"确实存在"却完全取不到分集。
		if detail != "" && !strings.HasPrefix(route, "j:") {
			if segments := xbpq.List(detail, route); len(segments) > 0 {
				withLink := 0
				for _, segment := range segments {
					if episodeCountIn(segment, rule) > 0 {
						withLink++
					}
				}
				if withLink == 0 {
					add("线路数组", fmt.Sprintf("线路数组 截出 %d 段，但没有一段含分集链接——引擎把线路数组的每一段当作「该线路的分集容器」来切分集，锚成线路切换按钮/标题行会让每条线路都是 0 集。线路数组 必须用与 播放数组 相同的【分集列表容器】锚点（如 <ul class=\\\"playlist\\\">&&</ul>），不能用线路按钮（ewave-tab、hl-tabs-btn 等）的锚点", len(segments)))
				} else if withLink < len(segments) {
					// 部分段没链接：多半是第一条线路 class 带 active/样式差异被前缀漏掉，线路数会少一条。
					add("线路数组", fmt.Sprintf("线路数组 截出 %d 段，其中只有 %d 段含分集链接——起始锚点过严，漏掉了 class 带附加值的线路（如首条常写成 class=\\\"xxx active\\\"）。去掉锚点末尾的 > 或引号，只框住标签开头的公共前缀", len(segments), withLink))
				}
			}
		}
		if play := rule.Field("播放数组"); play != "" && sameStart(route, play) {
			// 多线路站里 线路数组==播放数组 是正确写法；只有确认单线路时才报"拆重复线路"。
			// 任一证据（标题/容器段/hl按钮/dropdown）≥2 即视为多线路不误报。
			containerRoutes := 0
			if detail != "" && !strings.HasPrefix(route, "j:") {
				for _, segment := range xbpq.List(detail, route) {
					if episodeCountIn(segment, rule) > 0 {
						containerRoutes++
					}
				}
			}
			if ev.titles < 2 && containerRoutes < 2 && ev.ulGroups < 2 && ev.hlTabs < 2 && ev.dropdowns < 2 {
				add("线路数组", "线路数组 与 播放数组 起始锚点相同且样本未见多线路（无「播放线路/播放源」标题、线路容器独立截不出 ≥2 段、无 ≥2 个 hl 按钮/dropdown 资源）：会把同一线路的每一行拆成重复\"线路\"，单线路站必须删除 线路数组（多线路的锚点应是每条线路的容器标题行）")
			}
		}
	} else if ev.multi() && !strings.HasPrefix(rule.Field("播放数组"), "j:") {
		// 反向校验：样本明显是多线路站（任一证据 ≥2，含 DOM 实测分集分组），规则却没写 线路数组——
		// 分集数会变成所有线路之和且无法切换线路，属确定性遗漏，直接报出回喂 AI。
		add("播放数组", fmt.Sprintf("详情页样本检测到多线路（最强证据 %d 条：文字标题 %d、分集容器独立 %d 段、DOM 实测分集列表分组 %s、hl 按钮 %d、dropdown 资源 %d）但规则没写 线路数组：会把多条线路的分集混在一起、且无法切换线路。按指纹提示的形态补 线路数组（%s）",
			ev.max(), ev.titles, ev.containers, ev.ulGroupDetail, ev.hlTabs, ev.dropdowns, routeFormHint(ev)))
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
				// 锚点过宽：截出很多段却大半提不出 标题/链接，说明框进了导航、轮播、广告等非条目区块。
				// 典型踩坑：数组 写成 <li class="&&</li> 这类"只写属性名开头"的宽锚点。
				if titlePat := rule.Field("标题"); titlePat != "" && link != "" && len(entries) >= 3 {
					usable := 0
					for _, entry := range entries {
						if strings.TrimSpace(xbpq.CutOnce(entry, titlePat)) != "" &&
							strings.TrimSpace(xbpq.CutOnce(entry, link)) != "" {
							usable++
						}
					}
					if usable*2 < len(entries) {
						add("数组", fmt.Sprintf("数组 锚点在分类页截出 %d 段，但只有 %d 段能同时提出 标题 和 链接——锚点过宽，把导航/轮播/广告区也框进来了（如 <li class=\\\"&&</li> 会命中页面里所有 li）。改写成条目独有 class 的完整开标签，如 <li class=\\\"col-xs-4 col-md-3 col-lg-2\\\"&&</li>", len(entries), usable))
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

// failedCategoryIDs 从「实测失败分类」原文（Name(ID)、Name(ID)）里提取 ID 集合。
func failedCategoryIDs(failed string) map[string]bool {
	out := map[string]bool{}
	for _, item := range strings.Split(failed, "、") {
		item = strings.TrimSpace(item)
		open := strings.LastIndex(item, "(")
		close := strings.LastIndex(item, ")")
		if open < 0 || close <= open {
			continue
		}
		id := item[open+1 : close]
		if id != "" && id != "?" {
			out[id] = true
		}
	}
	return out
}

func occurrencesInAll(samples []ai.Sample, token string) int {
	total := 0
	for _, sample := range samples {
		total += strings.Count(sample.Content, token)
	}
	return total
}

// episodeCountIn 按规则自身的 播放列表（分隔符）+ 播放链接 实测一段里能提出几条分集，
// 与引擎 episodes() 的切分行为一致。比固定正则通用得多——站点用 /bpplay/、/video/
// 之类非常见前缀时，只看 playHrefPattern 会误判成"这一段没有分集"。
func episodeCountIn(segment string, rule xbpq.Rule) int {
	split := rule.Field("播放列表")
	if split == "" || split == "&&" {
		split = "#"
	}
	linkPattern := rule.Field("播放链接")
	count := 0
	for _, one := range strings.Split(segment, split) {
		if strings.TrimSpace(one) == "" {
			continue
		}
		if linkPattern != "" {
			if strings.TrimSpace(xbpq.CutOnce(one, linkPattern)) != "" {
				count++
			}
			continue
		}
		if playHrefPattern.MatchString(one) {
			count++
		}
	}
	return count
}

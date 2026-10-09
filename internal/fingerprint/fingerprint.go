// Package fingerprint 从页面样本中解析出经过核实的截取锚点（站点指纹），
// 并对 AI 产出的规则做格式自检。目的：把"AI 猜锚点、抄错 HTML、写错截取格式"
// 这三类高频错误分别压到"照抄指纹"和"服务端逐字校验"上。
package fingerprint

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/wrxbpq/wrxbpq/internal/ai"
	"github.com/wrxbpq/wrxbpq/internal/xbpq"
	"golang.org/x/net/html"
)

// Analyze 从样本生成站点指纹文本。样本缺失的环节自动跳过。
func Analyze(samples []ai.Sample) string {
	home := pick(samples, "首页")
	catalog := pick(samples, "分类页")
	if catalog == "" {
		catalog = home
	}
	detail := pick(samples, "详情页")
	play := pick(samples, "播放页")
	origin := sampleOrigin(samples)
	paging := pick(samples, "分页实测")
	category := pick(samples, "分类检测")

	var out []string
	out = append(out, "【站点指纹】以下锚点由服务端从真实页面样本里解析，并统计过出现次数。")
	out = append(out, "规则里的 pattern 一律照抄指纹中的锚点原文（逐字符，含空格与引号），禁止凭印象改写；指纹没覆盖的字段再回到样本原文里逐字复制。")
	if block := templateBlock(home, catalog, detail, origin, paging, category); block != "" {
		out = append(out, "\n== 模板与简写（先看这里）==\n"+block)
	}
	if block := homeBlock(home, category); block != "" {
		out = append(out, "\n== 首页（分类与搜索）==\n"+block)
	}
	if block := catalogBlock(catalog); block != "" {
		out = append(out, "\n== 列表页（目录）==\n"+block)
	}
	if block := detailBlock(detail); block != "" {
		out = append(out, "\n== 详情页（分集）==\n"+block)
	}
	if block := playBlock(play); block != "" {
		out = append(out, "\n== 播放页（直链）==\n"+block)
	}
	return strings.Join(out, "\n")
}

func pick(samples []ai.Sample, label string) string {
	for _, sample := range samples {
		if strings.HasPrefix(sample.Label, label) {
			return sample.Content
		}
	}
	return ""
}

// sampleOrigin 从样本 label（形如 "首页 https://xxx.com"）里取站点根地址。
// 简写规则省掉 主页url 时，分类url 必须是含域名的绝对地址——指纹用它给出标准写法。
func sampleOrigin(samples []ai.Sample) string {
	for _, sample := range samples {
		if !strings.HasPrefix(sample.Label, "首页") {
			continue
		}
		for _, field := range strings.Fields(sample.Label) {
			if origin := xbpqSampleOrigin(field); origin != "" {
				return origin
			}
		}
	}
	return ""
}

func xbpqSampleOrigin(address string) string {
	index := strings.Index(address, "://")
	if index < 0 {
		return ""
	}
	rest := address[index+3:]
	if slash := strings.IndexAny(rest, "/?#"); slash >= 0 {
		rest = rest[:slash]
	}
	if rest == "" {
		return ""
	}
	return address[:index+3] + rest
}

// ---- 模板与简写 ----

// skinPattern 探测页面用的是哪套前端皮肤（决定模板默认备选链能否命中）。
var skinPatterns = []struct{ name, probe string }{
	{"myui", `myui-`},
	{"stui", `stui-`},
	{"hl(海蓝)", `hl-`},
	{"module(苹果新版)", `module-`},
	{"conch", `conch`},
}

// detectSkin 返回样本里出现次数最多的皮肤名与次数。
func detectSkin(bodies ...string) (string, int) {
	counts := map[string]int{}
	for _, body := range bodies {
		if body == "" {
			continue
		}
		for _, skin := range skinPatterns {
			if at := strings.Count(body, skin.probe); at > 0 {
				counts[skin.name] += at
			}
		}
	}
	best, bestName := 0, ""
	for _, skin := range skinPatterns { // 按声明顺序稳定取名次
		if counts[skin.name] > best {
			best, bestName = counts[skin.name], skin.name
		}
	}
	return bestName, best
}

// guessCategoryTemplate 从首页/分类页样本里挑一条分类链接，还原成带占位符的 分类url 形态，
// 用于喂给 MatchTemplate 判断命中哪个家族。返回 (模板串, 真实链接示例)。
func guessCategoryTemplate(home, catalog string) (string, string) {
	scope := home
	if scope == "" {
		scope = catalog
	}
	if scope == "" {
		return "", ""
	}
	// 常见分类页链接形态，按家族优先级匹配。
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)href="(/index\.php/vod/(?:type|show)/id/[0-9a-z]+/[^"]*)"`),
		regexp.MustCompile(`(?i)href="(/vod(?:type|show|list)/[0-9a-z][^"]*)"`),
		regexp.MustCompile(`(?i)href="(/list/[0-9]+(?:-[0-9]+)?\.html)"`),
		regexp.MustCompile(`(?i)href="(/(?:type|show|category|fenlei|vs|vshow|screen)/[0-9a-zA-Z][^"]*)"`),
	}
	for _, re := range patterns {
		m := re.FindStringSubmatch(scope)
		if len(m) < 2 {
			continue
		}
		link := m[1]
		tpl := templatizeCategory(link)
		if tpl != "" {
			return tpl, link
		}
	}
	return "", ""
}

// templatizeCategory 把真实分类链接里的数字 ID / 页码替换成 {cateId}/{catePg}。
func templatizeCategory(link string) string {
	if !strings.Contains(link, "/") {
		return ""
	}
	// /list/2-1.html → /list/{cateId}-{catePg}.html；/list/2.html → /list/{cateId}.html
	if out := dashListPattern.ReplaceAllString(link, "/list/{cateId}-{catePg}.html"); out != link {
		return out
	}
	if out := plainListPattern.ReplaceAllString(link, "/list/{cateId}.html"); out != link {
		return out
	}
	// /vodtype/3.html → /vodtype/{cateId}.html；/vodshow/3-… → /vodshow/{cateId}-…
	if out := vodPathPattern.ReplaceAllString(link, "${1}/{cateId}"); out != link {
		return out
	}
	// 通用：把路径里的分类数字段当 cateId
	if out := genericIDPattern.ReplaceAllString(link, "/${1}/{cateId}"); out != link {
		return out
	}
	return link
}

var (
	dashListPattern  = regexp.MustCompile(`(?i)/list/\d+-\d+\.html`)
	plainListPattern = regexp.MustCompile(`(?i)/list/\d+\.html`)
	vodPathPattern   = regexp.MustCompile(`(?i)(/vod(?:type|show|detail|play))/\d+`)
	genericIDPattern = regexp.MustCompile(`(?i)/(type|show|category|fenlei|vs|vshow|screen)/\d+`)
)

// templateBlock 生成「模板与简写」指纹块：识别皮肤、命中家族、可省略字段清单。
// paging 是 Probe 阶段「分页实测」样本的结论文本；category 是「分类检测」样本的结论文本；
// 实测通过时用它覆盖静态推断。
func templateBlock(home, catalog, detail, origin, paging, category string) string {
	catView := parseCategoryFinding(category)
	skin, skinCount := detectSkin(catalog, detail, home)
	tpl, example := guessCategoryTemplate(home, catalog)
	// 分页实测优先：Probe 真的拼了第 2 页抓回来比对过，结论比正则推断可靠。
	// 实测模板里页码已是 {catePg}，但分类 ID 还是真实数字——还原成 {cateId}，
	// 让 AI 拿去只改 {cateId} 就能匹配 分类 里的每个 ID。
	pagingTemplate, pagingConfirmed := parsePagingFinding(paging)
	categoryTplUsed := false
	if pagingConfirmed && pagingTemplate != "" {
		guessed := tpl
		if catView.Template != "" && (catView.Status == "通过" || catView.Status == "部分通过") {
			// 分类实测已确定 {cateId} 的真实位置——用它做对齐骨架，比静态推断可靠
			guessed = catView.Template
			categoryTplUsed = true
		}
		if guessed != "" && !strings.HasPrefix(guessed, "http") && origin != "" {
			guessed = origin + guessed
		}
		tpl = normalizePagingTemplate(guessed, pagingTemplate)
		example = "分页实测（拼第2页抓取+内容比对通过）还原的模板"
	} else if catView.Template != "" && (catView.Status == "通过" || catView.Status == "部分通过") {
		// 分页没实测、但分类 id 位置实测过：以实测 {cateId} 骨架打底，
		// 分页段用推断形态补齐（最终以 paging 验证为准）。
		g := tpl
		if g != "" && !strings.HasPrefix(g, "http") && origin != "" {
			g = origin + g
		}
		if merged := mergeCategoryPagingTemplates(catView.Template, g); merged != "" {
			tpl = merged
			example = "分类实测 {cateId} 骨架 + 推断分页段（{catePg} 未实测）"
		} else {
			tpl = catView.Template
			example = "分类实测（导航分类真实抓取验证）的 {cateId} 模板"
		}
		categoryTplUsed = true
	}
	var lines []string
	lines = append(lines, "页面皮肤探测："+func() string {
		if skin == "" {
			return "未识别到 myui/stui/hl/module 标准皮肤（可能是自定义模板）——这类站【不要简写】，所有 数组/播放数组/标题/链接 必须按样本实测逐字写。"
		}
		return fmt.Sprintf("%s（样本出现 %d 次）", skin, skinCount)
	}())
	if paging != "" {
		lines = append(lines, paging)
	}
	if category != "" {
		lines = append(lines, category)
	}
	// 简写铁律：省掉 主页url 时，分类url 必须是含域名的绝对地址；{catePg} 分页占位必须写。
	if tpl != "" && !strings.HasPrefix(tpl, "http") && origin != "" {
		lines = append(lines, fmt.Sprintf("若简写省略 主页url，分类url 必须写全绝对地址：\"%s%s\"（相对路径 jar 无法定位站点，会直接识别失败）。", origin, tpl))
	}
	// 缺 {catePg} 的警示：静态推断出来才需要提醒补；实测通过说明形态已被验证过。
	if tpl != "" && !strings.Contains(tpl, "{catePg}") && !pagingConfirmed {
		lines = append(lines, "⚠ 推断形态缺少分页占位 {catePg}——【分类url 必须补上 {catePg}】，否则验证第 2 页与第 1 页相同、paging 步骤必挂。{catePg} 的具体形态（/2/、-2.html、?pg=2…）要靠真实拼接测试决定，不能只凭长相推断：请对照样本里的下一页链接，并用 paging 验证确认翻页内容确实变化。")
	}
	// 缺 {cateId} 的警示：多分类站没有 {cateId} 位置时，切分类永远打开同一页。
	if tpl != "" && !strings.Contains(tpl, "{cateId}") && strings.Contains(category, "已实测分类串") {
		lines = append(lines, "⚠ 推断形态缺少分类占位 {cateId}——站点有多个分类时【分类url 必须含 {cateId}】（位置以「分类检测」标出的 {cateId} 实测段为准），与 {catePg} 同时存在；否则切分类永远打开同一个页面，catalog 串档。")
	}
	if tpl == "" {
		lines = append(lines, "分类链接形态：首页未识别到标准分类链接，无法套用内置模板——按样本全字段手写。")
		return strings.Join(lines, "\n")
	}
	if pagingConfirmed && pagingTemplate != "" {
		suffix := "——这是真实拼接+抓取+两页比对确认过的模板，页码段逐字照抄，不要再按静态推断改形态。"
		if strings.Contains(tpl, "{cateId}") {
			suffix = "——这是真实拼接+抓取+两页比对确认过的模板（分类ID 段已还原为 {cateId}），逐字照抄，不要再按静态推断改形态。"
		} else {
			suffix = "——页码形态已经真实拼接+抓取+两页比对确认；⚠ 但该模板里【没有 {cateId}】，多分类站必须按「分类检测」实测位置补上 {cateId}（补进对应段后 {cateId} 与 {catePg} 并存），否则切分类永远打开同一页。"
		}
		if categoryTplUsed {
			suffix = "——页码形态经真实拼接+抓取+两页比对确认，{cateId} 位置来自分类实测，逐字照抄，不要再按静态推断改形态。"
		}
		lines = append(lines, fmt.Sprintf("分类url 分页形态【已实测】：%q%s", collapseSpace(tpl), suffix))
		// 「分类检测」样本里也带着一条只含 {cateId} 的模板（用于说明 {cateId} 落在哪个位置）。
		// 当分页已实测出「{cateId}+{catePg}」组合模板时，必须明确裁决最终以组合模板为准，
		// 否则 AI 会照抄分类检测里那条缺 {catePg} 的模板当分类url。
		if strings.Contains(tpl, "{cateId}") && strings.Contains(tpl, "{catePg}") && strings.Contains(category, "实测分类URL模板") {
			lines = append(lines, "⚠ 分类url 最终形态【以「分页实测」的组合模板为准】（{cateId} 与 {catePg} 并存，两个占位位置都经真实抓取+内容比对确认）；「分类检测」里那条模板只是 {cateId} 的位置示意，【不是】最终分类url——不要照抄它当分类url，也不要把两个占位拆开重写。")
		}
	} else if categoryTplUsed {
		lines = append(lines, fmt.Sprintf("分类url 形态：%q——{cateId} 位置来自分类实测（导航分类真实抓取验证，逐字保留、不要挪动）；页码段 %s（未实测，形态以 paging 验证为准，若验证失败按样本下一页链接换形态重试）。",
			collapseSpace(tpl), func() string {
				if strings.Contains(tpl, "{catePg}") {
					return "是按推断补齐的"
				}
				return "缺少 {catePg}、必须补上"
			}()))
	} else {
		lines = append(lines, fmt.Sprintf("分类url 推断形态（未实测，仅供参考）：%q（源自真实链接 %s）", collapseSpace(tpl), example))
	}
	familyName, merged := xbpq.MatchTemplate(tpl)
	if merged == nil {
		lines = append(lines, "该形态未命中内置模板家族——按样本全字段手写。")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "命中内置模板家族："+familyName)
	// 皮肤与家族默认链是否吻合：不吻合时简写会踩空，须提示。
	if skin != "" {
		ok := map[string][]string{
			"myui":   {"MacCMS", "MacCMS接口(JSON)", "路径式泛型", "通用高频"},
			"stui":   {"MacCMS", "MacCMS接口(JSON)", "路径式泛型", "通用高频"},
			"hl(海蓝)": {"MacCMS", "路径式泛型", "通用高频"},
		}[skin]
		compatible := ok == nil
		for _, name := range ok {
			if strings.Contains(familyName, name) {
				compatible = true
				break
			}
		}
		if !compatible {
			lines = append(lines, fmt.Sprintf("⚠ 皮肤是 %s 但命中模板链以其它皮肤为主，模板默认可能截不到——简写后务必逐步验证，失败字段按样本补写。", skin))
		}
	}
	skippable := xbpq.TemplateFieldNames(merged)
	lines = append(lines, "这些字段引擎会用模板兜底、【可以省略不写】："+strings.Join(skippable, "、"))
	lines = append(lines, "简写规则仍【必须写】：分类、分类url（含域名的绝对地址、必含 {cateId} 与 {catePg}）、搜索url（若模板没给）、以及样本里与皮肤不符、模板截不到的字段。")
	lines = append(lines, "策略：能对上皮肤的字段直接省略靠模板；模板指纹没覆盖、或与样本 HTML 不一致的字段，回到样本逐字实测再写，不要盲信模板默认值。")
	return strings.Join(lines, "\n")
}

// mergeCategoryPagingTemplates 把"实测出的 {cateId} 骨架"（来自分类检测，页码段未知）
// 与"推断的完整形态"（来自静态样本，含 {catePg} 段）拼起来：以实测骨架的
// {cateId} 为头，接上推断形态 {cateId} 之后的整段尾巴（含筛选/页码占位）。
// 拼接前提：推断尾巴必须真的带 {catePg}，否则不拼（回退纯骨架并提示补页码）。
// 重叠合并：骨架 {cateId} 之后可能已有 ".html" 之类后缀，而推断尾巴结尾同样是它
// （如 /list/{cateId}.html + -{catePg}.html），直接拼会出现 .html.html——
// 取"推断尾巴后缀 = 骨架后缀前缀"的最大重叠去重。
func mergeCategoryPagingTemplates(categoryTpl, guessed string) string {
	if categoryTpl == "" || guessed == "" {
		return ""
	}
	if !strings.Contains(categoryTpl, "{cateId}") || strings.Contains(categoryTpl, "{catePg}") {
		return ""
	}
	idAt := strings.Index(guessed, "{cateId}")
	if idAt < 0 {
		return ""
	}
	catAt := strings.Index(categoryTpl, "{cateId}")
	// 头必须逐字对齐（协议+域名+{cateId} 之前的路径），否则两种形态互相矛盾，
	// 不硬拼——回退纯骨架并提示补页码。
	if categoryTpl[:catAt] != guessed[:idAt] {
		return ""
	}
	tail := guessed[idAt+len("{cateId}"):]
	if !strings.Contains(tail, "{catePg}") {
		return ""
	}
	skeletonTail := categoryTpl[strings.Index(categoryTpl, "{cateId}")+len("{cateId}"):]
	overlap := 0
	maxK := len(tail)
	if len(skeletonTail) < maxK {
		maxK = len(skeletonTail)
	}
	for k := maxK; k > 0; k-- {
		if tail[len(tail)-k:] == skeletonTail[:k] {
			overlap = k
			break
		}
	}
	return strings.Replace(categoryTpl, "{cateId}", "{cateId}"+tail+skeletonTail[overlap:], 1)
}

func occurrences(body, token string) int { return strings.Count(body, token) }

// pagingTemplatePattern 从「分页实测」结论文本里提取实测通过的 分类url 模板。
var pagingTemplatePattern = regexp.MustCompile(`实测分类url模板：\s*"([^"]+)"`)

// parsePagingFinding 解析分页实测样本：返回（实测模板, 是否确认通过）。
func parsePagingFinding(paging string) (template string, confirmed bool) {
	if paging == "" {
		return "", false
	}
	confirmed = strings.Contains(paging, "结论：通过")
	if match := pagingTemplatePattern.FindStringSubmatch(paging); len(match) > 1 {
		template = match[1]
	}
	return template, confirmed
}

// normalizePagingTemplate 把实测通过的模板（页码段已是 {catePg}，分类 ID 仍是真实数字）
// 还原成可复用形态：以推断形态（含 {cateId}）作对齐参考，把实测串在 {cateId} 位置的
// 数字段替换回 {cateId}。对齐失败时退回"紧邻 {catePg} 的纯数字路径段即 cateId"的
// 规则；仍拿不准就原样返回——宁可不还原，也不产生错误模板。
func normalizePagingTemplate(guessed, measured string) string {
	if measured == "" {
		return measured
	}
	// 实测模板若已同时含 {cateId} 与 {catePg}（锚定分类后的一次性实测），直接采用，
	// 不再用骨架还原——避免把已经正确的双占位模板改坏。
	if strings.Contains(measured, "{cateId}") && strings.Contains(measured, "{catePg}") {
		return measured
	}
	if guessed != "" && strings.Contains(guessed, "{cateId}") {
		parts := strings.SplitN(guessed, "{cateId}", 2)
		head, tail := parts[0], parts[1]
		if strings.HasPrefix(measured, head) && strings.HasSuffix(measured, tail) &&
			len(measured) >= len(head)+len(tail) {
			middle := measured[len(head) : len(measured)-len(tail)]
			if middle != "" && len(middle) <= 30 && looksLikeID(middle) {
				return head + "{cateId}" + tail
			}
		}
	}
	// 备选：{catePg} 前面紧邻的纯数字路径段大概率就是 {cateId}。
	return pagingBeforePgPattern.ReplaceAllString(measured, "/{cateId}$2")
}

var idSegmentPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z_-]{0,29}$`)

func looksLikeID(s string) bool { return idSegmentPattern.MatchString(s) }

// ---- 分类实测样本解析 ----

var (
	categoryConfirmedPattern = regexp.MustCompile(`已实测分类串：\s*"([^"]+)"`)
	categoryFailedPattern    = regexp.MustCompile(`实测失败分类：(.+)（抓取失败或页面无内容`)
	categoryURLTplPattern    = regexp.MustCompile(`实测分类URL模板：\s*"([^"]+)"`)
)

// categoryFindingView 是「分类检测」样本的解析结果。
type categoryFindingView struct {
	Status      string // 通过 / 部分通过 / 未通过 / 跳过 / 无样本
	Confirmed   string // 已实测分类串（"电影$1#电视剧$2"），可能为空
	Failed      string // 实测失败分类列表原文
	Template    string // 实测分类URL模板（含 {cateId}）
	IDPosition  string // {cateId} 位置描述行原文
	SuspectSame bool   // 不同 ID 抓回页面几乎一样
}

// parseCategoryFinding 解析「分类检测」样本；无样本时返回 Status=""。
func parseCategoryFinding(category string) categoryFindingView {
	if strings.TrimSpace(category) == "" {
		return categoryFindingView{}
	}
	view := categoryFindingView{}
	switch {
	case strings.Contains(category, "结论：通过"):
		view.Status = "通过"
	case strings.Contains(category, "结论：部分通过"):
		view.Status = "部分通过"
	case strings.Contains(category, "结论：未通过"):
		view.Status = "未通过"
	case strings.Contains(category, "结论：跳过"):
		view.Status = "跳过"
	}
	if match := categoryConfirmedPattern.FindStringSubmatch(category); len(match) > 1 {
		view.Confirmed = match[1]
	}
	if match := categoryFailedPattern.FindStringSubmatch(category); len(match) > 1 {
		view.Failed = strings.TrimSpace(match[1])
	}
	if match := categoryURLTplPattern.FindStringSubmatch(category); len(match) > 1 {
		view.Template = match[1]
	}
	if idx := strings.Index(category, "分类url 的 {cateId} 位置（实测）："); idx >= 0 {
		line := category[idx:]
		if end := strings.IndexByte(line, '\n'); end >= 0 {
			line = line[:end]
		}
		view.IDPosition = line
	}
	view.SuspectSame = strings.Contains(category, "不同分类 ID 抓回的页面内容几乎一样")
	return view
}

// confirmedCategoryIDs 从已实测分类串里提取 ID 集合（电影$1#电视剧$2 → {1,2}）。
func confirmedCategoryIDs(confirmed string) map[string]bool {
	out := map[string]bool{}
	for _, pair := range strings.Split(confirmed, "#") {
		if dollar := strings.LastIndex(pair, "$"); dollar >= 0 {
			if id := pair[dollar+1:]; id != "" {
				out[id] = true
			}
		}
	}
	return out
}

// pagingBeforePgPattern 实测模板里紧邻 {catePg} 前的数字路径段（可能是分类 ID）：
// 覆盖 /1-{catePg}、/1/{catePg}、/1_{catePg} 三种衔接形态。RE2 无向前断言，
// 用捕获组把 {catePg} 及其前置分隔符一起圈进来再回填。
var pagingBeforePgPattern = regexp.MustCompile(`/(\d{1,6})([-_]?/?\{catePg\})`)

func clipToken(s string, limit int) string {
	s = collapseSpace(s)
	runes := []rune(s)
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ---- 列表页 ----

var divOpenPattern = regexp.MustCompile(`<div[^>]{0,160}>`)
var listOpenPattern = regexp.MustCompile(`<(?:ul|ol)[^>]{0,160}>`)

// routeTitlePattern 线路标题文字：播放线路1 / 播放源2 / 播放来源3 / 线路4 等变体。
var routeTitlePattern = regexp.MustCompile(`(?:播放线路|播放来源|播放源|线路)\s*\d+`)

func catalogBlock(body string) string {
	if body == "" {
		return ""
	}
	var lines []string
	// 条目边界：挑出现次数足够的重复标签
	itemStart, itemEnd := "", ""
	for _, pair := range [][2]string{{"<li", "</li>"}, {"<dd", "</dd>"}, {"<a", "</a>"}} {
		if occurrences(body, pair[0]) >= 6 {
			itemStart, itemEnd = pair[0], pair[1]
			break
		}
	}
	if itemStart == "" {
		return ""
	}
	lines = append(lines, fmt.Sprintf("条目边界锚点：%q（%s 全文出现 %d 次）→ 数组建议 \"%s…%s\" 形式，框住一个完整条目即可，href/title 留给字段截取。",
		itemStart+">", itemStart, occurrences(body, itemStart), itemStart, itemEnd))
	// 从所有条目片段里挑"最像内容条目"的一个：含详情页链接 > 含标题属性 > 最长。
	// 直接取第一个会命中导航栏的 <li>，导致字段建议全部失真。
	if item := bestItem(body, itemStart, itemEnd); item != "" {
		lines = append(lines, "代表条目原文（照抄锚点以此为准）：\n  "+clipToken(item, 400))
		if at := occurrences(item, `href="`); at > 0 {
			lines = append(lines, fmt.Sprintf("  → 链接 建议 \"href=\\\"&&\\\"\"（该条 %d 处；全文 %d 处）", at, occurrences(body, `href="`)))
		}
		if strings.Contains(item, `title="`) {
			lines = append(lines, fmt.Sprintf("  → 标题 建议 \"title=\\\"&&\\\"\"（全文 %d 处）", occurrences(body, `title="`)))
		}
		if strings.Contains(item, `data-original="`) {
			lines = append(lines, fmt.Sprintf("  → 列表图片 建议 \"data-original=\\\"&&\\\"\"（懒加载；全文 %d 处）", occurrences(body, `data-original="`)))
		} else if strings.Contains(item, `src="`) {
			lines = append(lines, fmt.Sprintf("  → 列表图片 建议 \"src=\\\"&&\\\"\"（全文 %d 处）", occurrences(body, `src="`)))
		}
	}
	// 实测：把候选 数组+链接 组合喂给真实截取引擎，只有截出"可跳转链接"的写法才推荐。
	// 这是防"锚点存在但截出坏值"的关键——AI 常把 链接 写成 href="/vod/&&.html"
	// 这类把路径前缀吃进锚点的形态，截出只剩裸 ID（如 55560），拼出的链接全坏。
	// 测量范围必须限定在 二次截取 容器内（页头/页脚同样满是 li，全页测量会把导航当"可用"）。
	if item := bestItem(body, itemStart, itemEnd); item != "" {
		scope := body
		if top := containerCandidates(body, strings.TrimPrefix(itemStart, "<")); len(top) > 0 {
			if span := containerSpan(body, top[0].tag); span != "" {
				scope = span
			}
		}
		// 代表条目的详情链接路径段（如 /vod/55560.html → "vod"），用于识别"截出来的是导航而非条目"
		wantSegment := ""
		if hm := detailHintPattern.FindStringSubmatch(item); hm != nil && len(hm) > 1 {
			wantSegment = hm[1]
		}
		arrays := []string{itemStart + "&&" + itemEnd}
		if gt := strings.Index(item, ">"); gt > 0 && strings.HasPrefix(item, itemStart) {
			if q := strings.Index(item[:gt], `"`); q >= 0 {
				arrays = append([]string{item[:gt] + "&&" + itemEnd, item[:q+1] + "&&" + itemEnd}, arrays...)
			}
		}
		lines = append(lines, "数组+链接 实测（在二次截取容器范围内按真实页面各截一遍，✓ 才能用）：")
		anyOK := false
		for _, arr := range arrays {
			entries := xbpq.List(scope, arr)
			if len(entries) < 3 {
				continue
			}
			for _, link := range []string{`href="&&"`, `href="/vod/&&"`, `href="/play/&&"`} {
				good, sample := 0, ""
				onTopic := 0
				for _, entry := range entries {
					value := strings.TrimSpace(xbpq.CutOnce(entry, link))
					if value == "" {
						continue
					}
					// 可跳转 = http 绝对链接，或以 / 开头的站内路径；裸 ID（无 / 无 http）说明锚点吃进了前缀
					if strings.HasPrefix(value, "http") || (strings.HasPrefix(value, "/") && len(value) > 1) {
						good++
						if wantSegment == "" || strings.Contains(value, "/"+wantSegment+"/") {
							onTopic++
							if sample == "" {
								sample = value
							}
						}
					}
				}
				// 双重门槛：多数可跳转 且 多数指向代表条目详情路径（否则是导航混入或前缀吃坏）
				if good > 0 && good*10 >= len(entries)*8 && (wantSegment == "" || onTopic*10 >= good*8) {
					anyOK = true
					lines = append(lines, fmt.Sprintf("  ✓ 数组=%q 配 链接=%q —— %d/%d 条截出指向 /%s/ 的可跳转链接，示例截取值：%s",
						clipToken(arr, 44), clipToken(link, 26), onTopic, len(entries), wantSegment, clipToken(sample, 70)))
					break
				}
				if link == `href="/vod/&&"` && good == 0 {
					lines = append(lines, fmt.Sprintf("  ✗ 链接=%q 是典型错误写法——把路径前缀吃进锚点后只截出裸 ID，拼不出链接", clipToken(link, 26)))
				}
				if link == `href="&&"` && good > 0 && wantSegment != "" && onTopic*10 < good*8 {
					lines = append(lines, fmt.Sprintf("  ✗ 数组=%q 配 链接=%q —— 截出的链接多数不指向 /%s/ 详情页（%d/%d），说明数组把导航也框进来了，换更贴条目的边界",
						clipToken(arr, 44), clipToken(link, 26), wantSegment, onTopic, good))
				}
			}
			if anyOK {
				break
			}
		}
		if !anyOK {
			lines = append(lines, "  链接 请逐字用 \"href=\\\"&&\\\"\"，不要把 /vod/ 之类路径前缀写进锚点")
		}
	}
	// 容器候选：用 DOM 子树统计每个 div 真实包含多少个条目，最内层且条目最多的才是列表区。
	// （字符串窗口近似会把导航/页脚 div 也算进来，误导 AI 的二次截取选择。）
	if top := containerCandidates(body, strings.TrimPrefix(itemStart, "<")); len(top) > 0 {
		lines = append(lines, "列表容器候选（二次截取用；按容器子树内真实条目数排序，越靠前越像主列表区）：")
		for _, one := range top {
			lines = append(lines, fmt.Sprintf("  %s —— 内含 %d 个条目（该开始标签全文出现 %d 次）", clipToken(one.tag, 88), one.entries, one.count))
		}
		lines = append(lines, "注意：二次截取 选内含条目数最多的那个容器；起始锚点用「开始标签去掉尾部 > 的前缀」，如 <div class=\"xxx\"&&（同行可能带 style= 等附加属性）。")
	}
	return strings.Join(lines, "\n")
}

// cand 是一个候选容器 div 开始标签及其统计。
type cand struct {
	tag     string
	count   int
	entries int
	depth   int
}

// containerCandidates 解析 DOM，找"子树条目多、但开始锚点在全文出现次数少"的 div——
// 这正是合格的 二次截取 容器（装着列表、又不会在页头页脚重复出现）。
// 锚点用「标签名+class（或 id）+去尾 >」前缀形态，对属性顺序/附加属性都稳。
func containerCandidates(body, itemTag string) []cand {
	if itemTag == "" {
		return nil
	}
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}
	var results []cand
	var walk func(node *html.Node, depth int) int
	walk = func(node *html.Node, depth int) int {
		total := 0
		if node.Type == html.ElementNode && node.Data == itemTag {
			total = 1
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			total += walk(child, depth+1)
		}
		if node.Type == html.ElementNode && node.Data == "div" {
			anchor := divAnchor(node)
			if anchor != "" {
				occurrences := strings.Count(body, anchor)
				if occurrences >= 1 && occurrences <= 4 {
					results = append(results, cand{tag: anchor, count: occurrences, entries: total, depth: depth})
				}
			}
		}
		return total
	}
	walk(document, 0)
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].entries != results[j].entries {
			return results[i].entries > results[j].entries
		}
		return results[i].depth > results[j].depth
	})
	var top []cand
	for _, one := range results {
		if one.entries < 3 {
			continue
		}
		// 同锚点只留一条（DOM 里可能多个同构 div）
		duplicate := false
		for _, kept := range top {
			if kept.tag == one.tag {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		top = append(top, one)
		if len(top) >= 3 {
			break
		}
	}
	return top
}

// containerSpan 在原文里定位容器：从前缀锚点（如 <div class="xxx"）找到开始标签结尾，
// 再从那里按 <div/</div> 深度配平找到闭合，返回容器子树原文。找不到返回 ""。
func containerSpan(body, anchor string) string {
	at := strings.Index(body, anchor)
	if at < 0 {
		return ""
	}
	openEnd := strings.Index(body[at:], ">")
	if openEnd < 0 {
		return ""
	}
	start := at + openEnd + 1
	depth := 1
	for cursor := start; cursor < len(body); {
		nextOpen := strings.Index(body[cursor:], "<div")
		nextClose := strings.Index(body[cursor:], "</div>")
		if nextClose < 0 {
			return body[start:]
		}
		if nextOpen >= 0 && nextOpen < nextClose {
			depth++
			cursor += nextOpen + 4
			continue
		}
		depth--
		if depth == 0 {
			return body[start : cursor+nextClose]
		}
		cursor += nextClose + 6
	}
	return body[start:]
}

// divAnchor 由 DOM 节点属性构造稳定的截取前缀：<div class="xxx" 或 <div id="xxx"。
// 没有 class/id 时退回渲染完整开始标签再去尾 >（若属性原文与渲染不一致会被出现次数过滤掉）。
func divAnchor(node *html.Node) string {
	class, hasClass := attrOf(node, "class")
	id, hasID := attrOf(node, "id")
	switch {
	case hasClass && strings.TrimSpace(class) != "":
		return "<div class=\"" + strings.TrimSpace(class) + "\""
	case hasID && strings.TrimSpace(id) != "":
		return "<div id=\"" + strings.TrimSpace(id) + "\""
	default:
		return ""
	}
}

func attrOf(node *html.Node, key string) (string, bool) {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val, true
		}
	}
	return "", false
}

// bestItem 在所有条目片段中挑一个最像"内容条目"的：
// 含 /detail/ 或 /vod/ 链接加分最多，其次含图片属性、title，再次长度。避免选中导航 <li>。
func bestItem(body, itemStart, itemEnd string) string {
	var best, runnerUp string
	bestScore := -1
	for _, loc := range findAllRanges(body, itemStart) {
		rest := body[loc[0]:]
		offset := strings.Index(rest, itemEnd)
		if offset < 0 {
			continue
		}
		segment := rest[:offset+len(itemEnd)]
		score := 0
		if detailHintPattern.MatchString(segment) {
			score += 8
		}
		if strings.Contains(segment, `title="`) {
			score += 2
		}
		if strings.Contains(segment, `data-original="`) || strings.Contains(segment, "<img") {
			score += 2
		}
		if len(segment) > 60 && len(segment) < 800 {
			score += 1
		}
		if score > bestScore {
			bestScore, runnerUp, best = score, best, segment
		}
	}
	if bestScore <= 0 && best == "" {
		return runnerUp
	}
	return best
}

var detailHintPattern = regexp.MustCompile(`(?i)href="[^"]*?/(detail|vod|movie|drama|play)/`)

// findAllRanges 返回 token 每次出现的起止索引。
func findAllRanges(body, token string) [][2]int {
	var out [][2]int
	for start := 0; ; {
		index := strings.Index(body[start:], token)
		if index < 0 {
			break
		}
		out = append(out, [2]int{start + index, start + index + len(token)})
		start += index + len(token)
		if len(out) >= 120 {
			break
		}
	}
	return out
}

// ---- 详情页 ----

// playHrefPattern 分集链接识别：路径含 /play/、/vodplay/、/playhtml/、/dplay/，
// 或播放脚本 play.php（vodplay.php 内含 play.php?，一并命中）。
// 早期只认 /play/ 会漏掉 MacCMS 的 /vodplay/1-1-1.html 连写形态，导致这类站
// 整个详情页分集识别为空、多线路无从判起。
var playHrefPattern = regexp.MustCompile(`href="[^"]*?(?:/play/|/vodplay/|/playhtml/|/dplay/|play\.php\?)[^"]*"`)

// hlTabPattern hl(海蓝)皮肤线路按钮：class 含 hl-tabs-btn，一排按钮对应多个分集面板。
var hlTabPattern = regexp.MustCompile(`(?i)<a[^>]*class="[^"]*hl-tabs-btn[^"]*"[^>]*>`)

var hlTabInnerPattern = regexp.MustCompile(`(?is)<a[^>]*hl-tabs-btn[^>]*>(.*?)</a>`)
var tagStripPattern = regexp.MustCompile(`<[^>]+>`)

// dropdownPattern myui 新版下拉多资源：data-dropdown-value 每个值就是一条线路名。
var dropdownPattern = regexp.MustCompile(`data-dropdown-value="([^"]{1,40})"`)

// stripTagsLine 去掉 HTML 标签取按钮文字（线路名）。
func stripTagsLine(s string) string {
	s = tagStripPattern.ReplaceAllString(s, "")
	return collapseSpace(strings.TrimSpace(s))
}

// routeEvidence 详情页样本的多线路证据。
type routeEvidence struct {
	titles          int      // 「播放线路/播放源/线路 N」文字标题数
	containers      int      // 分集列表容器独立截出的段数（每段含分集链接）
	ulGroups        int      // DOM 实测：按分集链接最近列表父节点分组的线路数（不受 class 逐线路不同影响）
	ulGroupDetail   string   // 分组明细（如 "3 组 [2/2/2]"），供指纹回显
	containerAnchor string   // DOM 实测给出的 线路数组 建议锚点（父节点开始标签前缀）
	hlTabs          int      // hl 皮肤线路按钮数
	dropdowns       int      // myui data-dropdown-value 数
	dropNames       []string // 下拉资源名（示例用）
	tabNames        []string // hl 按钮文字（示例用）
}

func (r routeEvidence) max() int {
	m := r.titles
	for _, v := range []int{r.containers, r.ulGroups, r.hlTabs, r.dropdowns} {
		if v > m {
			m = v
		}
	}
	return m
}

// max 任一证据 ≥2 即认定多线路站。
func (r routeEvidence) multi() bool { return r.max() >= 2 }

// routeEvidenceOf 计算详情页样本的全部多线路证据。容器段数复用 voteEpisodeContainer。
// ulGroups 是 DOM 实测证据：按分集链接的最近列表父节点逐页分组统计线路数，
// 不受「各线路容器 class 不一致、字符串截取只认投票众数」的限制——很多站
// 线路容器 class 带逐线路后缀或嵌套结构不同，voteEpisodeContainer 只截得出 1 段，
// 但 DOM 上分明有 2+ 个列表各挂一串分集链接，这类漏判由 ulGroups 兜住。
func routeEvidenceOf(body string) routeEvidence {
	if body == "" {
		return routeEvidence{}
	}
	ev := routeEvidence{
		titles:    len(routeTitlePattern.FindAllString(body, -1)),
		hlTabs:    len(hlTabPattern.FindAllString(body, -1)),
		dropdowns: len(dropdownPattern.FindAllStringSubmatch(body, -1)),
	}
	for _, m := range hlTabInnerPattern.FindAllStringSubmatch(body, 8) {
		if name := stripTagsLine(m[1]); name != "" && !containsFold(ev.tabNames, name) {
			ev.tabNames = append(ev.tabNames, name)
		}
	}
	for _, m := range dropdownPattern.FindAllStringSubmatch(body, 8) {
		if name := strings.TrimSpace(m[1]); name != "" && !containsFold(ev.dropNames, name) {
			ev.dropNames = append(ev.dropNames, name)
		}
	}
	if _, _, prefix, endTag, _, _ := voteEpisodeContainer(body); prefix != "" && endTag != "" {
		for _, segment := range xbpq.List(body, prefix+"&&"+endTag) {
			if playHrefPattern.MatchString(segment) {
				ev.containers++
			}
		}
	}
	if groups, anchor := episodeRouteGroups(body); len(groups) > 0 {
		ev.ulGroups = len(groups)
		counts := make([]string, len(groups))
		for index, count := range groups {
			counts[index] = strconv.Itoa(count)
		}
		ev.ulGroupDetail = fmt.Sprintf("%d 组 [%s]", len(groups), strings.Join(counts, "/"))
		// 锚点建议只在比字符串截取更强、且覆盖全部线路组时给出
		if len(groups) > ev.containers {
			ev.containerAnchor = anchor
		}
	}
	return ev
}

// episodeRouteGroups DOM 实测：解析详情页，把每条 /play/ 分集链接归到它
// 「最近的列表类祖先」（ul/ol 优先，其次 dd、带 class 的 div 容器），
// 按祖先节点去重分组，返回（各组分集数——已剔除 <2 集的噪声组、建议的 线路数组 锚点）。
// 这是"分析具体页面再定线路"的核心：分组基于真实 DOM 结构而非字符串锚点众数，
// 每个线路容器 class 不同、或外层 div 与内层 ul 嵌套错位时也能数对线路。
func episodeRouteGroups(body string) ([]int, string) {
	if !playHrefPattern.MatchString(body) {
		return nil, ""
	}
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil, ""
	}
	var order []*html.Node
	counts := map[*html.Node]int{}
	anchorOf := map[*html.Node]string{}
	var walk func(node *html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" {
			for _, attr := range node.Attr {
				if strings.EqualFold(attr.Key, "href") && playHrefPattern.MatchString(`href="`+attr.Val+`"`) {
					group := episodeRouteAncestor(node)
					if group != nil {
						if counts[group] == 0 {
							order = append(order, group)
							anchorOf[group] = containerAnchorOf(group)
						}
						counts[group]++
					}
					break
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	var groups []int
	anchorCover := map[string]int{} // 锚点 → 使用该锚点的线路组数
	for _, node := range order {
		if counts[node] < 2 {
			continue // 单集容器多为推荐位/热播榜混入的分集链接，不算线路
		}
		groups = append(groups, counts[node])
		if anchor := anchorOf[node]; anchor != "" {
			anchorCover[anchor]++
		}
	}
	// 线路数组 锚点建议只在"该锚点覆盖全部线路组"时给出（class 逐组不同的站
	// 锚点各不相同 → 不给锚点，回退文字形态描述，避免给出截不全的写法）。
	if len(groups) >= 2 {
		bestAnchor, bestCover := "", 0
		for anchor, cover := range anchorCover {
			if cover > bestCover || (cover == bestCover && anchor < bestAnchor) {
				bestAnchor, bestCover = anchor, cover
			}
		}
		if bestCover == len(groups) {
			return groups, bestAnchor
		}
	}
	return groups, ""
}

// episodeRouteAncestor 找分集链接所属的线路容器：沿父链向上，
// 第一个 ul/ol 即线路容器（分集列表的天然容器）；没有列表层时退到 dd
// （MacCMS 旧模板每线路一个 dd）或带 class 的最小 div 包装层。
// 到 body/html 仍未命中返回 nil（裸链接不参与分组）。
func episodeRouteAncestor(link *html.Node) *html.Node {
	firstDiv := (*html.Node)(nil)
	for node := link.Parent; node != nil; node = node.Parent {
		if node.Type != html.ElementNode {
			continue
		}
		switch node.Data {
		case "ul", "ol":
			return node
		case "dd":
			return node
		case "div":
			if firstDiv == nil {
				if _, hasClass := attrOf(node, "class"); hasClass {
					firstDiv = node
				}
			}
		case "body", "html":
			return firstDiv
		}
	}
	return firstDiv
}

// containerAnchorOf 由线路容器节点构造可直接写进 线路数组 的开始锚点前缀。
// 复用 suggestPrefix 语义：<ul class="stui-content__playlist clearfix"（去尾 >）。
func containerAnchorOf(node *html.Node) string {
	var builder strings.Builder
	builder.WriteString("<")
	builder.WriteString(node.Data)
	if class, ok := attrOf(node, "class"); ok && strings.TrimSpace(class) != "" {
		builder.WriteString(` class="` + strings.TrimSpace(class) + `"`)
	} else if id, ok := attrOf(node, "id"); ok && strings.TrimSpace(id) != "" {
		builder.WriteString(` id="` + strings.TrimSpace(id) + `"`)
	} else {
		return ""
	}
	return builder.String()
}

func containsFold(list []string, want string) bool {
	for _, item := range list {
		if strings.EqualFold(item, want) {
			return true
		}
	}
	return false
}

// routeFormHint 按最强证据返回 线路数组 的推荐写法（一句话，供自检报错引用）。
func routeFormHint(ev routeEvidence) string {
	switch {
	case ev.hlTabs >= 2:
		return "hl 皮肤：线路数组 锚每个 hl-tabs-btn 按钮，播放数组 锚分集面板容器——两者不同锚点"
	case ev.dropdowns >= 2:
		return "myui dropdown：线路标题=\"data-dropdown-value=\\\"&&\\\"\"，线路数组 锚每条 dropdown-menu 的 <li>"
	case ev.containerAnchor != "":
		return "DOM 实测容器形态：线路数组 与 播放数组 用同一锚点 \"" + ev.containerAnchor + "&&</" + firstTagOfAnchor(ev.containerAnchor) + ">\"（详情页实测该容器共截出 " + ev.ulGroupDetail + "）"
	case ev.containers >= 2 || ev.ulGroups >= 2:
		return "并列容器：线路数组 与 播放数组 用同一列表容器锚点，引擎按容器段切分（详情页 DOM 实测 " + ev.ulGroupDetail + "）"
	default:
		return "文字标题形态：线路数组 锚每条线路容器（与 播放数组 同锚点），线路标题 截「播放源/播放线路 N」标题行"
	}
}

// firstTagOfAnchor 取容器锚点 "<ul class=\"…\"" 里的标签名（ul/ol/div/dd）。
func firstTagOfAnchor(anchor string) string {
	trimmed := strings.TrimPrefix(anchor, "<")
	end := strings.IndexAny(trimmed, " >")
	if end <= 0 {
		return ""
	}
	return trimmed[:end]
}

// voteEpisodeContainer 找分集列表容器：对每条分集链接取"其前最近的 ul/ol（优先）或 div"
// 开始标签投票取众数。不能取标签首次出现位置——通用 class 会把页头误当容器。
// 返回（分集链接位置表、容器开始标签、建议前缀锚、闭合标签、票数、是否 ul/ol）。
func voteEpisodeContainer(body string) (matches [][]int, container, prefix, endTag string, votes int, fromUL bool) {
	matches = playHrefPattern.FindAllStringIndex(body, 50)
	if len(matches) == 0 {
		return nil, "", "", "", 0, false
	}
	ulVotes := map[string]int{}
	var ulOrder []string
	ulPositions := listOpenPattern.FindAllStringIndex(body, -1)
	ulCursor := 0
	for _, playLoc := range matches {
		for ulCursor < len(ulPositions) && ulPositions[ulCursor][0] < playLoc[0] {
			ulCursor++
		}
		if ulCursor > 0 {
			tag := body[ulPositions[ulCursor-1][0]:ulPositions[ulCursor-1][1]]
			if len(tag) <= 140 {
				if _, seen := ulVotes[tag]; !seen {
					ulOrder = append(ulOrder, tag)
				}
				ulVotes[tag]++
			}
		}
	}
	divVotes := map[string]int{}
	var divOrder []string
	positions := divOpenPattern.FindAllStringIndex(body, -1)
	diveAt := 0
	for _, playLoc := range matches {
		for diveAt < len(positions) && positions[diveAt][0] < playLoc[0] {
			diveAt++
		}
		if diveAt > 0 {
			tag := body[positions[diveAt-1][0]:positions[diveAt-1][1]]
			if len(tag) <= 120 {
				if _, seen := divVotes[tag]; !seen {
					divOrder = append(divOrder, tag)
				}
				divVotes[tag]++
			}
		}
	}
	// 平票取文档序最靠前者（map 迭代顺序随机，多线路页必须确定性输出）
	best := 0
	for _, tag := range ulOrder {
		if ulVotes[tag] > best {
			container, best, fromUL = tag, ulVotes[tag], true
		}
	}
	if container == "" {
		best = 0
		for _, tag := range divOrder {
			if divVotes[tag] > best {
				container, best = tag, divVotes[tag]
			}
		}
	}
	if container == "" {
		return matches, "", "", "", 0, false
	}
	endTag = "</div>"
	if fromUL {
		if strings.HasPrefix(container, "<ol") {
			endTag = "</ol>"
		} else {
			endTag = "</ul>"
		}
	}
	prefix = suggestPrefix(container)
	return matches, container, prefix, endTag, best, fromUL
}

func detailBlock(body string) string {
	if body == "" {
		return ""
	}
	matches, container, prefix, endTag, votes, fromUL := voteEpisodeContainer(body)
	if len(matches) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("分集链接形态 href=\"…/play/…\" 共 %d 处，示例：%s", len(matches), clipToken(body[matches[0][0]:matches[0][1]], 90)))
	ev := routeEvidenceOf(body)
	if container != "" {
		lines = append(lines, fmt.Sprintf("分集列表容器真实开始标签：%s （%d 条分集链接投票；该前缀全文出现 %d 次）", clipToken(container, 120), votes, occurrences(body, prefix)))
		if prefix != strings.TrimSuffix(container, ">") {
			lines = append(lines, fmt.Sprintf("  （用 %q 前缀匹配更稳：同类行的 style= 等附加属性可能逐行不同）", prefix))
		}
		if fromUL {
			lines = append(lines, fmt.Sprintf("  → 播放数组 建议 \"%s&&%s\"——用分集列表的内层容器（ul/ol），结束符 %s 与其配对，不要把外层 div 当容器（div&&</div> 会截到嵌套错位的内容，分集拆不出来）。", escapeGo(prefix), endTag, endTag))
		} else {
			lines = append(lines, fmt.Sprintf("  → 播放数组 建议 \"%s&&</div>\"——起始锚点必须去掉尾部的 >，因为真实标签往往带 style= 等额外属性，带 > 会匹配不上。", escapeGo(prefix)))
		}
		// 多线路判定：四路证据——①「播放线路/播放源/线路 N」文字标题；②分集容器独立截段数
		// （每段含分集链接才算一条线路）；③hl 皮肤线路按钮数；④myui data-dropdown-value 数。
		// 很多站没有"播放线路"字样但线路容器并排多个，只数文字会漏判。
		if ev.multi() {
			lines = append(lines, fmt.Sprintf("  检测到多线路站（最强证据 %d 条；文字标题 %d、分集容器独立截出 %d 段、DOM 实测分集列表分组 %s、hl 线路按钮 %d、dropdown 资源 %d），【必须写 线路数组】。",
				ev.max(), ev.titles, ev.containers, ev.ulGroupDetail, ev.hlTabs, ev.dropdowns))
			switch {
			case ev.hlTabs >= 2:
				lines = append(lines, fmt.Sprintf("  → hl(海蓝)皮肤：线路按钮与分集面板分离，线路数组 与 播放数组 【不是】同锚点——线路数组=\"class=\\\"hl-tabs-btn hl-slide-swiper\\\"&&</a>\"，线路标题=\">&&</a>\"（按钮文字是\\\"线路1\\\"这类占位时加 [替换:线路1>>资源名]，需要指定顺序再加 [排序:资源B>资源A]）；播放数组 用分集面板容器（data-value 指向的 id，如 \"id=\\\"hl-plays-list\\\"&&</div>\"）。按钮文字实测：%s。",
					clipToken(strings.Join(ev.tabNames, "、"), 120)))
			case ev.dropdowns >= 2:
				lines = append(lines, fmt.Sprintf("  → myui dropdown 形态：线路标题=\"data-dropdown-value=\\\"&&\\\"\"（资源名实测：%s）；线路数组 锚每条 dropdown-menu 的 <li> 容器（对照详情页原文逐字写）。",
					clipToken(strings.Join(ev.dropNames, "、"), 120)))
			case ev.containerAnchor != "":
				lines = append(lines, fmt.Sprintf("  → DOM 实测并列容器（%s）：线路数组 与 播放数组 用同一个列表容器锚点 \"%s&&</%s>\" 即可，引擎按容器段切分线路——这是逐页解析真实 DOM 得出的分组，即使各线路容器附加属性不同也适用；每条线路有容器标题行（如 <h3 class=\\\"title\\\">播放源…、data-dropdown-value）时再写 线路标题 截它。",
					ev.ulGroupDetail, escapeGo(ev.containerAnchor), firstTagOfAnchor(ev.containerAnchor)))
			case ev.containers >= 2 || ev.ulGroups >= 2:
				lines = append(lines, fmt.Sprintf("  → 并列容器形态：线路数组 与 播放数组 用同一个列表容器锚点（%s&&%s）即可，引擎按容器段切分线路；每条线路有容器标题行（如 <h3 class=\"title\">播放源…、data-dropdown-value）时再写 线路标题 截它。DOM 实测分组 %s——若按该锚点只截得出 1 段而分组显示 ≥2 组，说明各线路容器 class 不同，要对照详情页原文给每条线路容器找共同前缀。",
					escapeGo(prefix), endTag, ev.ulGroupDetail))
			default:
				lines = append(lines, "  → 文字标题形态：线路标题 截「播放线路 N」标题行；线路数组 按官方样例用每条线路的容器锚点（与 播放数组 同锚点即可）。")
			}
		} else if occurrences(body, prefix) >= 2 {
			lines = append(lines, fmt.Sprintf("  该容器出现 %d 次但独立截取只得到 %d 条有效线路：若确认只有一条线路，就不要写 线路数组（写了会把每一行拆成重复线路）。", occurrences(body, prefix), ev.containers))
		}
	}
	if at := occurrences(body, "<li"); votes > 0 && at >= votes {
		lines = append(lines, fmt.Sprintf("  → 播放列表 可用 \"<li\"（注意是不带 > 的前缀：条目可能写成 <li ><a…，全文出现 %d 次）", at))
	}
	if at := occurrences(body, "</a>"); at >= len(matches) {
		lines = append(lines, fmt.Sprintf("  → 播放标题 可用 \">&&</a>\"；播放链接 可用 \"href=\\\"&&\\\"\"（</a> 全文出现 %d 处，以分集条目原文为准）", at))
	}
	// 分集条目原文：取投票容器之后第一个含 /play/ 链接的条目，避免拿到页面其它 li（如留言板）
	if container != "" {
		if start := strings.Index(body, container); start >= 0 {
			rest := body[start:]
			if linkAt := playHrefPattern.FindStringIndex(rest); linkAt != nil {
				liStart := strings.LastIndex(rest[:linkAt[0]], "<li")
				if liStart >= 0 {
					if liEnd := strings.Index(rest[liStart:], "</li>"); liEnd > 0 {
						lines = append(lines, "第一个分集条目原文：\n  "+clipToken(rest[liStart:liStart+liEnd+5], 260))
					}
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}

func escapeGo(s string) string { return strings.ReplaceAll(s, `"`, `\"`) }

// suggestPrefix 从开始标签里取最稳的匹配前缀：<div class="row" style="..."> → <div class="row"。
// 保留标签名与 class（列表行的语义身份），丢掉 style= 之类逐行可能不同的附加属性。
func suggestPrefix(tag string) string {
	if len(tag) > 300 {
		return strings.TrimSuffix(tag, ">")
	}
	var builder strings.Builder
	builder.WriteString("<")
	nameEnd := 1
	for nameEnd < len(tag) && tag[nameEnd] != ' ' && tag[nameEnd] != '>' {
		nameEnd++
	}
	builder.WriteString(tag[1:nameEnd])
	rest := tag[nameEnd:]
	// 找第一个 class 属性
	for {
		at := strings.Index(rest, `class="`)
		if at < 0 {
			break
		}
		end := strings.Index(rest[at+7:], `"`)
		if end < 0 {
			break
		}
		builder.WriteString(" class=\"" + rest[at+7:at+7+end] + "\"")
		break
	}
	if idAt := strings.Index(tag, `id="`); idAt >= 0 && !strings.Contains(builder.String(), "class") {
		end := strings.Index(tag[idAt+4:], `"`)
		if end >= 0 {
			builder.WriteString(" id=\"" + tag[idAt+4:idAt+4+end] + "\"")
		}
	}
	prefix := builder.String()
	if len(prefix) < 5 {
		return strings.TrimSuffix(tag, ">")
	}
	return prefix
}

// ---- 播放页 ----

var mediaURLPattern = regexp.MustCompile(`https?://[^"'\s\\]+?\.(?:m3u8|mp4)[^"'\s\\]*`)

var jumpCandidates = []string{
	`url: '&&'`,
	`url:"&&"`,
	`"url":"&&"`,
	`player_aaaa=&&;`,
	`now="&&"`,
	`url=&&&`,
	`src:"&&"`,
}

func playBlock(body string) string {
	if body == "" {
		return ""
	}
	var lines []string
	media := mediaURLPattern.FindString(body)
	if media == "" {
		// MacCMS 系 player_aaaa 的 url 常写成 https:\/\/…\/index.m3u8（\/ 转义），
		// 反斜杠转正斜杠再找一遍，并在反转义后的副本上做后续定位（原串里 Index 会找不到 media）
		unescaped := strings.ReplaceAll(body, `\/`, `/`)
		media = mediaURLPattern.FindString(unescaped)
		if media != "" {
			body = unescaped
		}
	}
	if media == "" {
		lines = append(lines, "样本里没找到明文 m3u8/mp4 直链：可能是加密/iframe 二级页，跳转播放链接 需要先解 script 里的变量。")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "页面里的真实直链示例："+clipToken(media, 140))
	// MacCMS player_aaaa 页有专门的坑：页面上还有 var maccms={"url":"www.xxx",...}，
	// "url":"&&" 锚点会先命中 maccms 那条截出站点域名。引擎内置 player_aaaa 解析兜底，
	// 这种站推荐【不写 跳转播放链接】。
	if strings.Contains(body, "player_aaaa") {
		lines = append(lines, "  检测到 MacCMS player_aaaa 配置对象：跳转播放链接 建议【整个字段省略不写】——引擎会自动从 player_aaaa 解出 url（实测比手写锚点稳；手写 \"url\":\"&&\" 常先命中同页 var maccms 的 \"url\":\"站点域名\" 截出坏值）。")
		return strings.Join(lines, "\n")
	}
	worked := false
	for _, candidate := range jumpCandidates {
		value := strings.TrimSpace(strings.ReplaceAll(xbpq.CutOnce(body, candidate), `\/`, `/`))
		if value != "" && strings.HasPrefix(value, "http") {
			lines = append(lines, fmt.Sprintf("  → 跳转播放链接 实测可用：%q（截出 %s）", candidate, clipToken(value, 120)))
			worked = true
		}
	}
	if !worked {
		at := strings.Index(body, media)
		start := at - 100
		if start < 0 {
			start = 0
		}
		lines = append(lines, "  直链前文（据此自定锚点，锚点必须逐字复制）：\n  "+clipToken(body[start:at], 200))
	}
	return strings.Join(lines, "\n")
}

// ---- 首页 ----

var navPattern = regexp.MustCompile(`(?i)<a[^>]+href="[^"]*?/(?:type|list|vodtype|show|fenlei)/([0-9a-zA-Z-]+)/?[^"]*"[^>]*>([^<>]{1,12})</a>`)
var formPattern = regexp.MustCompile(`(?i)<form[^>]+action="([^"]*(?:search|so|wd)[^"]*)"[^>]*>`)

func homeBlock(body, category string) string {
	if body == "" {
		return ""
	}
	catView := parseCategoryFinding(category)
	var lines []string
	seen := map[string]bool{}
	var pairs []string
	for _, match := range navPattern.FindAllStringSubmatch(body, 80) {
		id, name := match[1], strings.TrimSpace(match[2])
		// MacCMS 搜索页占位链接形态是 /list/2-----------.html（连字符补位），ID 要去掉尾部连字符
		id = strings.Trim(id, "-")
		// 分类导航 ID 是纯段（2 / 13）；仍含 - 的是分页链接（2-2），不作分类候选
		if id == "" || strings.Contains(id, "-") {
			continue
		}
		if name == "" || seen[id] || strings.Contains(name, "-") || len([]rune(name)) > 8 {
			continue
		}
		seen[id] = true
		pairs = append(pairs, name+"$"+id)
		if len(pairs) >= 8 {
			break
		}
	}
	if len(pairs) > 0 {
		if catView.Confirmed != "" && (catView.Status == "通过" || catView.Status == "部分通过") {
			lines = append(lines, "分类字段【已实测】：使用上方「分类检测」的已实测分类串（真实抓取验证过 ID 有效），此处导航静态候选不再作为依据。")
		} else {
			lines = append(lines, "导航分类候选（名称$ID，未经实测，仅供参考）：\n  "+strings.Join(pairs, "#"))
			lines = append(lines, "  ⚠ ID 是否有效取决于站点是否真按该段取分类——「分类检测」样本有实测结论时以它为准。")
		}
		if pageMatch := regexp.MustCompile(`(?i)/((?:type|list|vodtype)/[0-9a-zA-Z-]+/)2/`).FindStringSubmatch(body); pageMatch != nil {
			lines = append(lines, fmt.Sprintf("  → 分页形态：/%s{catePg}/ —— 分类url 建议含 {cateId} 与 {catePg} 两段，如 …/type/{cateId}/{catePg}/…", pageMatch[1]))
		} else if dashMatch := regexp.MustCompile(`(?i)/(list|vodlist|type)/(\d+)-2\.html`).FindStringSubmatch(body); dashMatch != nil {
			lines = append(lines, fmt.Sprintf("  → 分页形态：/%s/{cateId}-2.html —— 分类url 建议 \"/%s/{cateId}-{catePg}.html\"", dashMatch[1], dashMatch[1]))
		} else {
			lines = append(lines, "  → 未在首页见到第 2 页链接：分类url 的 {catePg} 形态请再对照分类页样本确认。")
		}
	}
	if form := formPattern.FindStringSubmatch(body); form != nil {
		lines = append(lines, fmt.Sprintf("  → 搜索表单 action=%q：若含 ?wd= 直接替换成 {wd}；若是 /search/xxx/ 路径形态则拼 …/{wd}/…，务必与 action 原文逐字一致。", form[1]))
	}
	return strings.Join(lines, "\n")
}

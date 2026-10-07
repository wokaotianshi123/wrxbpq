// Package fingerprint 从页面样本中解析出经过核实的截取锚点（站点指纹），
// 并对 AI 产出的规则做格式自检。目的：把"AI 猜锚点、抄错 HTML、写错截取格式"
// 这三类高频错误分别压到"照抄指纹"和"服务端逐字校验"上。
package fingerprint

import (
	"fmt"
	"regexp"
	"sort"
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

	var out []string
	out = append(out, "【站点指纹】以下锚点由服务端从真实页面样本里解析，并统计过出现次数。")
	out = append(out, "规则里的 pattern 一律照抄指纹中的锚点原文（逐字符，含空格与引号），禁止凭印象改写；指纹没覆盖的字段再回到样本原文里逐字复制。")
	if block := homeBlock(home); block != "" {
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

func occurrences(body, token string) int { return strings.Count(body, token) }

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
var routeTitlePattern = regexp.MustCompile(`播放线路\s*\d+`)

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

var playHrefPattern = regexp.MustCompile(`href="[^"]*?/play/[^"]+"`)

func detailBlock(body string) string {
	if body == "" {
		return ""
	}
	matches := playHrefPattern.FindAllStringIndex(body, 50)
	if len(matches) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("分集链接形态 href=\"…/play/…\" 共 %d 处，示例：%s", len(matches), clipToken(body[matches[0][0]:matches[0][1]], 90)))
	// 分集容器：对每条分集链接找"其前最近的 div 开始标签"，再投票取众数。
	// 不能取标签首次出现位置——重复标签（如通用 class）会把页头 div 误当容器。
	// 分集容器：优先"每条分集链接之前最近的 ul/ol"——分集列表的内层容器，
	// 用 ul&&</ul> 截取结束符干净；退而求其次才用 div 投票（ffv1 类无 ul 结构的站）。
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
	container, best, fromUL := "", 0, false
	for _, tag := range ulOrder {
		if ulVotes[tag] > best {
			container, best, fromUL = tag, ulVotes[tag], true
		}
	}
	if container == "" {
		for _, tag := range divOrder {
			if divVotes[tag] > best {
				container, best = tag, divVotes[tag]
			}
		}
	}
	endTag := "</div>"
	if fromUL {
		if strings.HasPrefix(container, "<ol") {
			endTag = "</ol>"
		} else {
			endTag = "</ul>"
		}
	}
	if container != "" {
		prefix := suggestPrefix(container)
		lines = append(lines, fmt.Sprintf("分集列表容器真实开始标签：%s （%d 条分集链接投票；该前缀全文出现 %d 次）", clipToken(container, 120), best, occurrences(body, prefix)))
		if prefix != strings.TrimSuffix(container, ">") {
			lines = append(lines, fmt.Sprintf("  （用 %q 前缀匹配更稳：同类行的 style= 等附加属性可能逐行不同）", prefix))
		}
		if fromUL {
			lines = append(lines, fmt.Sprintf("  → 播放数组 建议 \"%s&&%s\"——用分集列表的内层容器（ul/ol），结束符 %s 与其配对，不要把外层 div 当容器（div&&</div> 会截到嵌套错位的内容，分集拆不出来）。", escapeGo(prefix), endTag, endTag))
		} else {
			lines = append(lines, fmt.Sprintf("  → 播放数组 建议 \"%s&&</div>\"——起始锚点必须去掉尾部的 >，因为真实标签往往带 style= 等额外属性，带 > 会匹配不上。", escapeGo(prefix)))
		}
		// 多线路判定要数「播放线路 N」标题而不是容器出现次数：容器次数受样本裁剪影响，标题才是站点行为的直接证据
		routes := routeTitlePattern.FindAllString(body, -1)
		if len(routes) >= 2 {
			lines = append(lines, fmt.Sprintf("  检测到 %d 个「播放线路」标题：这是多线路站。线路数组 与 播放数组 使用同一个列表容器锚点（%s&&%s）是正确写法——每个容器就是一条线路。", len(routes), escapeGo(prefix), endTag))
		} else if occurrences(body, prefix) >= 2 && len(routes) == 0 {
			lines = append(lines, fmt.Sprintf("  该容器出现 %d 次但未检测到「播放线路」标题：若确认只有一条线路，就不要写 线路数组（写了会把每一行拆成重复线路）。", occurrences(body, prefix)))
		}
	}
	if at := occurrences(body, "<li"); best > 0 && at >= best {
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

func homeBlock(body string) string {
	if body == "" {
		return ""
	}
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
		lines = append(lines, "导航分类候选（名称$ID，顺序照抄）：\n  "+strings.Join(pairs, "#"))
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

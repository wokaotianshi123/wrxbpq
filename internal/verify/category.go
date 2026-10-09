// 分类实测：分类字段（名称$ID）里的 ID 与 分类url 的 {cateId} 段，过去只靠正则从
// 首页导航里"扒数字"直接叫 AI 照抄，从未验证这个 ID 拼出来的页面到底有没有内容。
// 本文件与分页实测同理做真实验证：
// ①从首页导航提取"短锚文本 + 路径链接"候选；②交叉 diff 真实分类 URL，找出
// 真正随分类变化的 id 段（路径段或 query 参数），而不是假设"数字就是 id"；
// ③每个候选分类真实抓取，页面能提出 ≥3 个条目链接才算该 ID 有效；
// ④两两比对各分类页的条目集合——若不同 ID 抓回的页面几乎一样，判定 id 没生效并警告。
// 结论写进「分类检测」样本，指纹与自检以实测为准。
package verify

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wrxbpq/wrxbpq/internal/xbpq"
)

// categoryNoiseWords 导航里常见的非分类锚文本。
var categoryNoisePattern = regexp.MustCompile(`首页|更多|下一?页|上一条|下一条|搜索|登录|注册|排行|榜单|专题|公告|留言|帮助|下载|直播|顶`)

var anchorInnerPattern = regexp.MustCompile(`(?is)<a\b[^>]*>(.*)</a>`)
var stripTagPattern = regexp.MustCompile(`<[^>]*>`)

// categoryPair 一条候选分类（导航锚文本 + 真实链接）。
type categoryPair struct {
	Name string
	URL  string
	ID   string // 从真实 URL diff 出的分类 id 段（推导失败时为空）
}

// CategoryFinding 是分类实测的结论。
type CategoryFinding struct {
	Confirmed []categoryPair // 抓取验证通过的分类
	Failed    []categoryPair // 抓取失败或无内容的分类
	Template  string         // 交叉 diff 出的含 {cateId} 的分类 URL 模板
	Note      string         // 面向 AI 的结论文本（进指纹样本，格式可被解析）
}

// categoryCandidates 从首页提取分类候选：锚文本 1~6 字、href 是站内路径、
// 排除常见导航噪音。宁滥勿缺——真正的把关在"抓取验证"一步。
func categoryCandidates(siteURL, homeBody string) []categoryPair {
	seen := map[string]bool{}
	names := map[string]bool{}
	var out []categoryPair
	for _, tag := range aTagClosePattern.FindAllString(homeBody, 4000) {
		hrefMatch := hrefExtractPattern.FindStringSubmatch(tag)
		inner := anchorInnerPattern.FindStringSubmatch(tag)
		if hrefMatch == nil || inner == nil {
			continue
		}
		raw := strings.TrimSpace(hrefMatch[1])
		lower := strings.ToLower(raw)
		if raw == "" || raw == "#" || strings.HasPrefix(lower, "javascript") ||
			strings.HasPrefix(lower, "mailto") {
			continue
		}
		name := collapseText(stripTagPattern.ReplaceAllString(inner[1], ""))
		if name == "" || len([]rune(name)) > 6 || categoryNoisePattern.MatchString(name) {
			continue
		}
		if !strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "http") && !strings.Contains(raw, "/") {
			continue // 相对裸串（?page=…）先不收，形态太杂
		}
		absolute := xbpq.Absolute(siteURL+"/", raw)
		parsed, err := url.Parse(absolute)
		if err != nil || !strings.EqualFold(parsed.Host, mustHost(siteURL)) {
			continue
		}
		key := parsed.Path + "?" + parsed.RawQuery
		if seen[key] || names[name] {
			continue
		}
		seen[key] = true
		names[name] = true
		out = append(out, categoryPair{Name: name, URL: absolute})
		if len(out) >= 10 {
			break
		}
	}
	return out
}

func collapseText(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), "")
}

func mustHost(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return parsed.Host
}

// idPosition 记录分类 id 在 URL 里的位置（路径段或 query 参数）。
type idPosition struct {
	pathIndex   int    // 差异所在的 path 段下标；-1 表示走 query
	prefix      int    // 段内 id 之前的原样文本长度
	tokenLen    int    // 真实 URL 里 id 段的长度（占位连字符已排除）
	queryKey    string // query 形态的参数名
	exampleID   string // 第一个样本的 id 值（写进结论帮 AI 理解）
	examplePath string // 样本一的真实 URL
}

// pathIDToken 在两个差异路径段里找"唯一不同的字母数字串"：把段拆成
// token（[0-9A-Za-z_-]+）与分隔串交替序列，要求两侧 token 数相同、分隔串
// 逐字相同、且恰好只有一个 token 不同——两侧该 token 都长得像 id 才认可。
// 这样 /vodtype/1.html 与 /vodtype/2.html（token=1/2，尾巴 .html）、
// /type/1/ 与 /type/2/（整段即 id）、/list/2-----------.html 与
// /list/3-----------.html（占位连字符并入 token）都能命中；
// /movie1.html 对 /tv2.html（前缀 movie≠tv，分隔串不同）会被正确拒绝。
func pathIDToken(segA, segB string) (index, length int, ok bool) {
	tokensA, sepsA := splitSegmentTokens(segA)
	tokensB, sepsB := splitSegmentTokens(segB)
	if len(tokensA) != len(tokensB) || len(sepsA) != len(sepsB) {
		return 0, 0, false
	}
	for i := range sepsA {
		if sepsA[i] != sepsB[i] {
			return 0, 0, false
		}
	}
	diff := -1
	for i := range tokensA {
		if tokensA[i] != tokensB[i] {
			if diff >= 0 {
				return 0, 0, false // 不止一处 token 差异
			}
			diff = i
		}
	}
	if diff < 0 || !looksLikeIDToken(tokensA[diff]) || !looksLikeIDToken(tokensB[diff]) {
		return 0, 0, false
	}
	start := 0
	for i := 0; i < diff; i++ {
		start += len(sepsA[i]) + len(tokensA[i])
	}
	start += len(sepsA[diff])
	ta, tb := tokensA[diff], tokensB[diff]
	// 纯字母 slug id（无数字，如 movie/tv/cartoon）：整个 token 就是 id。
	if !hasDigit(ta) && !hasDigit(tb) {
		return start, len(ta), true
	}
	// 含数字：id 必须是去掉公共前后缀后【纯数字】的一段（MacCMS 2----------- 取前导 2、
	// movie1.html 对 tv2.html 因 movie≠tv 中段非纯数字被拒——那是两个不同模板而非分类 id）。
	p := commonPrefixLen(ta, tb)
	s := commonSuffixLen(ta[p:], tb[p:])
	mid := ta[p : len(ta)-s]
	if !onlyDigits(mid) || mid == "" {
		return 0, 0, false
	}
	return start + p, len(mid), true
}

func hasDigit(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return true
		}
	}
	return false
}

// splitSegmentTokens 把字符串拆成 token 序列与分隔串序列：
// seg == seps[0] + tokens[0] + seps[1] + tokens[1] + … + seps[len(tokens)]
func splitSegmentTokens(seg string) (tokens, seps []string) {
	var current strings.Builder
	inToken := false
	sep := strings.Builder{}
	flushSep := func() {
		seps = append(seps, sep.String())
		sep.Reset()
	}
	for i := 0; i < len(seg); i++ {
		r := seg[i]
		isToken := r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_'
		if isToken {
			if !inToken {
				flushSep()
				inToken = true
			}
			current.WriteByte(r)
			continue
		}
		if inToken {
			tokens = append(tokens, current.String())
			current.Reset()
			inToken = false
		}
		sep.WriteByte(r)
	}
	if inToken {
		tokens = append(tokens, current.String())
	}
	flushSep()
	return tokens, seps
}

// deriveIDPosition 交叉 diff 两个同 host 分类 URL，找出唯一变化段。
// 变化必须局限在一个位置（路径一段或 query 一个参数），且两侧都像 id（数字/slug）。
func deriveIDPosition(a, b string) (idPosition, bool) {
	ua, uaErr := url.Parse(a)
	ub, ubErr := url.Parse(b)
	if uaErr != nil || ubErr != nil {
		return idPosition{}, false
	}
	segA := strings.Split(ua.Path, "/")
	segB := strings.Split(ub.Path, "/")
	pathDiff := -1
	if len(segA) == len(segB) {
		multiple := false
		for i := range segA {
			if segA[i] != segB[i] {
				if pathDiff >= 0 {
					multiple = true
					break // 不止一段差异，形态对不齐
				}
				pathDiff = i
			}
		}
		if !multiple && pathDiff >= 0 {
			if idx, ln, ok := pathIDToken(segA[pathDiff], segB[pathDiff]); ok {
				return idPosition{pathIndex: pathDiff, prefix: idx, tokenLen: ln, exampleID: segA[pathDiff][idx : idx+ln], examplePath: a}, true
			}
		}
	}
	// query 形态：恰好一个参数值不同且都像 id。
	var keys []string
	for key := range ua.Query() {
		if ua.Query().Get(key) != ub.Query().Get(key) && ua.Query().Get(key) != "" && ub.Query().Get(key) != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) == 1 {
		midA, midB := ua.Query().Get(keys[0]), ub.Query().Get(keys[0])
		if looksLikeIDToken(midA) && looksLikeIDToken(midB) {
			return idPosition{pathIndex: -1, queryKey: keys[0], exampleID: midA, examplePath: a}, true
		}
	}
	return idPosition{}, false
}

func looksLikeIDToken(s string) bool {
	if s == "" || len(s) > 30 || strings.Contains(s, "{") || strings.Contains(s, "%") {
		return false
	}
	if onlyDigits(s) && len(s) <= 8 {
		return true
	}
	// slug：字母数字、可含 -_，但不含点/斜杠/问号这类结构字符
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func onlyDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// extractID 按已知 id 位置从具体分类 URL 里取出 id 值。
func (p idPosition) extractID(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if p.pathIndex >= 0 {
		segments := strings.Split(parsed.Path, "/")
		if p.pathIndex >= len(segments) {
			return ""
		}
		segment := segments[p.pathIndex]
		if len(segment) < p.prefix+p.tokenLen {
			return ""
		}
		return segment[p.prefix : p.prefix+p.tokenLen]
	}
	return parsed.Query().Get(p.queryKey)
}

// buildTemplate 生成把 id 段换成 {cateId} 的分类 URL 模板。
// 注意不能用 parsed.String()——它会把 {cateId} 的花括号转义成 %7B%7D。
func (p idPosition) buildTemplate() string {
	parsed, err := url.Parse(p.examplePath)
	if err != nil {
		return ""
	}
	if p.pathIndex >= 0 {
		segments := strings.Split(parsed.Path, "/")
		if p.pathIndex >= len(segments) {
			return ""
		}
		segment := segments[p.pathIndex]
		if len(segment) < p.prefix+p.tokenLen {
			return ""
		}
		segments[p.pathIndex] = segment[:p.prefix] + "{cateId}" + segment[p.prefix+p.tokenLen:]
		out := parsed.Scheme + "://" + parsed.Host + strings.Join(segments, "/")
		if parsed.RawQuery != "" {
			out += "?" + parsed.RawQuery
		}
		return out
	}
	replaced := strings.Replace(parsed.RawQuery, p.queryKey+"="+url.QueryEscape(p.exampleID), p.queryKey+"={cateId}", 1)
	if replaced == parsed.RawQuery {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host + parsed.Path + "?" + replaced
}

// probeCategories 分类实测主流程：提取候选 → diff 出 id 位置 → 并发真实抓取 → 比对。
func probeCategories(ctx context.Context, fetcher *xbpq.Fetcher, siteURL, homeBody string) CategoryFinding {
	pairs := categoryCandidates(siteURL, homeBody)
	if len(pairs) < 2 {
		return CategoryFinding{Note: fmt.Sprintf("分类检测 结论：跳过（首页导航里只提出 %d 个分类候选）。分类请按导航逐字抄并靠 catalog 验证把关。", len(pairs))}
	}
	// 用第一对能对齐的 URL 确定 id 位置。
	var position idPosition
	positionFound := false
	for i := 0; i+1 < len(pairs) && !positionFound; i++ {
		position, positionFound = deriveIDPosition(pairs[i].URL, pairs[i+1].URL)
	}
	if !positionFound {
		return CategoryFinding{Note: "分类检测 结论：跳过（分类链接之间不止一处差异，无法确定 {cateId} 段）。分类按样本导航逐字抄，靠 catalog 与 paging 验证把关。"}
	}
	for index := range pairs {
		pairs[index].ID = position.extractID(pairs[index].URL)
	}

	// 并发抓取验证（最多 6 条，单条 12s，整体 45s）。
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	limit := len(pairs)
	if limit > 6 {
		limit = 6
	}
	type fetchOutcome struct {
		pair    categoryPair
		entries map[string]bool
		err     string
	}
	outcomes := make([]fetchOutcome, limit)
	var waiters sync.WaitGroup
	for index := 0; index < limit; index++ {
		waiters.Add(1)
		go func(index int) {
			defer waiters.Done()
			singleCtx, singleCancel := context.WithTimeout(ctx, 12*time.Second)
			defer singleCancel()
			body, err := fetcher.Get(singleCtx, pairs[index].URL, siteURL+"/")
			switch {
			case err != nil:
				outcomes[index] = fetchOutcome{pair: pairs[index], err: err.Error()}
			case strings.TrimSpace(body) == "":
				outcomes[index] = fetchOutcome{pair: pairs[index], err: "空响应"}
			default:
				outcomes[index] = fetchOutcome{pair: pairs[index], entries: entrySet(body)}
			}
		}(index)
	}
	waiters.Wait()

	finding := CategoryFinding{}
	for _, outcome := range outcomes {
		if outcome.err != "" || len(outcome.entries) < 3 || outcome.pair.ID == "" {
			finding.Failed = append(finding.Failed, outcome.pair)
			continue
		}
		finding.Confirmed = append(finding.Confirmed, outcome.pair)
	}
	// id 生效性：不同分类的条目集合若几乎相同，说明 id 没被站点理会。
	// entrySets 与 Confirmed 下标对齐（按 outcomes 顺序重建）。
	var entrySets []map[string]bool
	for _, outcome := range outcomes {
		if outcome.err == "" && len(outcome.entries) >= 3 && outcome.pair.ID != "" {
			entrySets = append(entrySets, outcome.entries)
		}
	}
	suspectSame := false
	for i := 0; i < len(entrySets); i++ {
		for j := i + 1; j < len(entrySets); j++ {
			if finding.Confirmed[i].ID == finding.Confirmed[j].ID {
				continue
			}
			if overlap(entrySets[i], entrySets[j]) >= 95 {
				suspectSame = true
			}
		}
	}

	finding.Template = position.buildTemplate()
	finding.Note = renderCategoryNote(finding, position, suspectSame)
	return finding
}

func renderCategoryNote(finding CategoryFinding, position idPosition, suspectSame bool) string {
	var builder strings.Builder
	builder.WriteString("分类检测 ")
	switch {
	case len(finding.Confirmed) == 0:
		builder.WriteString("结论：未通过（候选分类页抓取全部失败/无内容）。\n")
	case len(finding.Failed) == 0 && !suspectSame:
		builder.WriteString("结论：通过。\n")
	default:
		builder.WriteString("结论：部分通过。\n")
	}
	if len(finding.Confirmed) > 0 {
		var parts []string
		for _, pair := range finding.Confirmed {
			parts = append(parts, pair.Name+"$"+pair.ID)
		}
		builder.WriteString("已实测分类串：\"" + strings.Join(parts, "#") + "\"（每条都真实抓取过且页面可提出 ≥3 个条目，分类字段从这里逐字取）\n")
	}
	if len(finding.Failed) > 0 {
		var parts []string
		for _, pair := range finding.Failed {
			id := pair.ID
			if id == "" {
				id = "?"
			}
			parts = append(parts, pair.Name+"("+id+")")
		}
		builder.WriteString("实测失败分类：" + strings.Join(parts, "、") + "（抓取失败或页面无内容——不要放进分类字段）\n")
	}
	if position.pathIndex >= 0 {
		builder.WriteString(fmt.Sprintf("分类url 的 {cateId} 位置（实测）：路径段 %q 中 %q 一段（如 %s）\n",
			sampleSegment(position), position.exampleID, position.examplePath))
	} else {
		builder.WriteString(fmt.Sprintf("分类url 的 {cateId} 位置（实测）：query 参数 %s（如 %s）\n", position.queryKey, position.examplePath))
	}
	if len(finding.Confirmed) > 0 && finding.Template != "" {
		builder.WriteString("实测分类URL模板：\"" + finding.Template + "\"\n")
	}
	if suspectSame {
		builder.WriteString("⚠ 不同分类 ID 抓回的页面内容几乎一样：{cateId} 可能没生效（形态猜错或站点忽略该参数）——用 paging/catalog 验证复核。\n")
	}
	if len(finding.Confirmed) < 6 && len(finding.Failed) == 0 {
		builder.WriteString("（本次最多实测 6 个候选；导航里其余分类未实测，形态相同时可按上面的 id 位置外推。）\n")
	}
	return strings.TrimRight(builder.String(), "\n")
}

func sampleSegment(p idPosition) string {
	parsed, err := url.Parse(p.examplePath)
	if err != nil || p.pathIndex < 0 {
		return ""
	}
	segments := strings.Split(parsed.Path, "/")
	if p.pathIndex >= len(segments) {
		return ""
	}
	return segments[p.pathIndex]
}

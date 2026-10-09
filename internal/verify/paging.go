// 分页形态实测：分类url 里 {catePg} 到底写成 /{catePg}/、-{catePg}.html 还是 ?pg=，
// 静态正则只能猜个"建议形态"。本文件在 Probe 阶段做真实验证：
// ①从分类页找「下一页/页码」真实链接；②与当前分类页 URL 做前后缀 diff，
// 差异段必须是 +1 的数字（页码的强证据）；③把差异段替换成 {catePg} 生成模板，
// 直接抓取拼出的第 2 页；④与第 1 页条目集合比对——内容确实翻页了才算实测通过。
// 结论写进「分页实测」样本，指纹与 AI 以此为准，不再拿猜测当判断。
package verify

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wrxbpq/wrxbpq/internal/xbpq"
)

// PagingFinding 是一次分页实测的结论。
type PagingFinding struct {
	Confirmed bool   // 实测拼接并抓取成功、内容确认翻页
	Template  string // 实测通过的 分类url 模板（页码段已写成 {catePg}）
	SecondURL string // 实测抓回的第 2 页地址
	Note      string // 面向 AI 的结论文本（进指纹样本）
}

var (
	nextPageLabelPattern = regexp.MustCompile(`(?i)>\s*(下一?页|next)`)
	aTagClosePattern     = regexp.MustCompile(`(?is)<a\b[^>]*>.+?</a>`)
	hrefExtractPattern   = regexp.MustCompile(`(?i)href="([^"]+)"`)
)

// probePaging 对分类页做分页实测。catalogBody 用未裁剪的完整页面。
// idPos 是「分类检测」实测出的 {cateId} 位置：传入后分页探测会锚定在该分类上，
// 候选链接必须保持同一 {cateId}，从而把"下一类"链接排除、只认真正的下一页，
// 最终产出同时含 {cateId} 与 {catePg} 的组合模板（二者位置均来自实测）。
func probePaging(ctx context.Context, fetcher *xbpq.Fetcher, catalogURL, catalogBody string, idPos idPosition) PagingFinding {
	if strings.TrimSpace(catalogURL) == "" || strings.TrimSpace(catalogBody) == "" {
		return PagingFinding{Note: "分页实测 结论：跳过（缺少分类页样本）。"}
	}
	linkCandidates := pagingCandidates(catalogURL, catalogBody, idPos)
	if ctx == nil {
		ctx = context.Background()
	}
	// 合成阶段要挨个试多种分页形态，超时比只试页面链接时放宽一些。
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	first := entrySet(catalogBody)
	if len(first) < 3 {
		return PagingFinding{Note: fmt.Sprintf("分页实测 结论：跳过（分类页只提出 %d 个条目链接，样本不足）。", len(first))}
	}
	anchored := idPosValid(idPos)

	// attempt 试一个候选页：先产出模板，再真抓取并与第 1 页比对内容重合。
	// 成功返回 finding，否则返回失败原因（进结论文本，供 AI/用户看到试过哪些）。
	attempt := func(candidate string, synthesized bool) (PagingFinding, string) {
		var template string
		var ok bool
		if anchored {
			// 已锚定分类：在同一 {cateId} 内探页码，直接产出含 {cateId}+{catePg} 的组合模板。
			template, ok = buildCombinedTemplate(catalogURL, candidate, idPos)
			if !ok {
				return PagingFinding{}, "该链接与第1页的差异不能解析为页码（多半是其它分类而非下一页）"
			}
		} else {
			var firstPage, secondPage string
			template, firstPage, secondPage, ok = buildPagingTemplate(catalogURL, candidate)
			if !ok {
				return PagingFinding{}, fmt.Sprintf("与第1页的差异段 %s→%s 不是 +1 数字，不像页码", digitOrDash(firstPage), digitOrDash(secondPage))
			}
		}
		body, err := fetcher.Get(ctx, candidate, catalogURL)
		if err != nil || len(body) < 300 {
			return PagingFinding{}, "拼接抓取失败" + errText(err)
		}
		second := entrySet(body)
		if len(second) == 0 {
			return PagingFinding{}, "抓回页里提不出条目链接"
		}
		score := overlap(first, second)
		if score >= 85 {
			// 拼得出去但内容同页：差异段多半是年份/地区之类筛选值，不是页码。
			return PagingFinding{}, fmt.Sprintf("内容与第1页重合 %d%%，该差异不是页码而是筛选值", score)
		}
		note := fmt.Sprintf("分页实测 结论：通过\n实测分类url模板：\"%s\"\n第2页实测地址：%s\n与第1页条目重合仅 %d%%（第1页 %d 条 / 第2页 %d 条），确认翻页生效。",
			template, candidate, score, len(first), len(second))
		switch {
		case synthesized:
			note += "该 {catePg} 形态由服务端在已实测的 {cateId} 模板上主动拼接第2页、抓回比对内容后确认（分类页里没有「下一页」链接，属纯拼接翻页站）。{cateId} 与 {catePg} 两个占位位置都经真实抓取确认，分类url 整体照抄这个组合模板，不要再拆开改形态。"
		case anchored:
			note += "该模板已同时含 {cateId}（位置来自「分类检测」实测）与 {catePg}，二者均为实测结论，直接整体照抄，不要再按静态推断拆改形态。"
		default:
			note += "写 分类url 时页码段逐字用 {catePg} 替换上面模板里的页码数字（其余段与筛选占位按需保留）——这是【实测结论】，优先级高于任何静态推断。"
		}
		return PagingFinding{Confirmed: true, Template: template, SecondURL: candidate, Note: note}, ""
	}

	var failures []string
	// ①先试分类页里真实存在的「下一页/页码」链接。
	for _, candidate := range linkCandidates {
		if finding, fail := attempt(candidate, false); fail == "" {
			return finding
		} else {
			failures = append(failures, candidate+"（"+fail+"）")
		}
	}
	// ②已锚定 {cateId} 却没找到可用的「下一页」链接（AJAX 翻页 / 纯拼接站）：
	//   按常见分页形态在 {cateId} 模板上主动拼接第 2 页去实测，而不是直接放弃 {catePg}。
	//   这样分类url 才能做到"先定 {cateId}，再按本站模板追加 {catePg}"。
	if anchored {
		for _, candidate := range synthPagingCandidates(catalogURL, idPos) {
			if finding, fail := attempt(candidate, true); fail == "" {
				return finding
			} else {
				failures = append(failures, candidate+"（"+fail+"）")
			}
		}
	}
	if len(failures) == 0 {
		return PagingFinding{Note: "分页实测 结论：跳过（分类页里未找到「下一页/页码」链接，可能是 AJAX 翻页或该分类只有一页）。{catePg} 未经实测——先按样本最像页码的段写，最终以验证 paging 步骤实测为准。"}
	}
	return PagingFinding{Note: "分页实测 结论：未通过\n已试候选拼接页：" + strings.Join(capList(failures), "\n") +
		"\n{catePg} 形态未能实测确定——按 paging 验证结果逐一试下一种形态（/2/、-2.html、?pg=2 等）。"}
}

func digitOrDash(s string) string {
	if s == "" {
		return "∅"
	}
	if len(s) > 8 {
		return s[:8] + "…"
	}
	return s
}

func capList(list []string) []string {
	if len(list) > 4 {
		return list[:4]
	}
	return list
}

func errText(err error) string {
	if err == nil {
		return "：响应过短"
	}
	return ": " + err.Error()
}

// pagingCandidates 从分类页找"疑似下一页"链接（最多 4 个，去重）：
// ①a 标签文字是 下一页/next；②href 与当前分类 URL 只差一段且该段是数字。
// idPos 有效时额外要求候选保持同一 {cateId}——把"下一类"链接排除，只认真正的下一页。
func pagingCandidates(catalogURL, catalogBody string, idPos idPosition) []string {
	base, err := url.Parse(catalogURL)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	var baseID string
	if idPosValid(idPos) {
		baseID = idPos.extractID(catalogURL)
	}
	for _, tag := range aTagClosePattern.FindAllString(catalogBody, 500) {
		href := hrefExtractPattern.FindStringSubmatch(tag)
		if href == nil {
			continue
		}
		raw := strings.TrimSpace(href[1])
		if raw == "" || raw == "#" || strings.HasPrefix(strings.ToLower(raw), "javascript") {
			continue
		}
		absolute := xbpq.Absolute(catalogURL, raw)
		other, err := url.Parse(absolute)
		if err != nil || !strings.EqualFold(other.Host, base.Host) || seen[absolute] {
			continue
		}
		// 与当前页"完全同一个地址"跳过；path 相同但 query 不同（?pg=2 形态）是合法候选。
		if other.Path == base.Path && other.RawQuery == base.RawQuery {
			continue
		}
		// 已锚定分类：候选必须保持同一 {cateId}，否则它是"下一类"链接而非"下一页"，
		// 会让分页把分类号误当页码，产出与 {cateId} 冲突的错误模板。
		if idPosValid(idPos) && idPos.extractID(absolute) != baseID {
			continue
		}
		qualified := nextPageLabelPattern.MatchString(tag) ||
			differsByNumber(base.String(), other.String())
		if idPosValid(idPos) {
			// 已锚定分类：同 {cateId} 链接即视为页码候选（如 ?page=2 形态无 +1 数字差异），
			// 是否真翻页由后面"抓取+内容重合"判定把关。
			qualified = true
		}
		if !qualified {
			continue
		}
		seen[absolute] = true
		out = append(out, absolute)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

// synthPagingCandidates 在已实测确定 {cateId} 位置的分类 URL 上，按常见分页形态
// 主动拼接"第 2 页"候选 URL。用于分类页里找不到「下一页」链接（AJAX 翻页 / 纯拼接站）
// 时仍然把 {catePg} 实测出来，而不是直接放弃分页、只留 {cateId}。
//
// 关键约束：所有候选都保持 {cateId} 段原值不变，只在其之外插入页码 2——
// 于是 buildCombinedTemplate 能把两个 URL 的 {cateId} 同时还原成占位符、
// 只把新插入的页码段抽成 {catePg}，产出 {cateId}+{catePg} 的组合模板
// （例如 /list/2.html + /list/2-2.html → /list/{cateId}-{catePg}.html）。
//
// 形态是否真的翻页，由 probePaging 的"抓取 + 内容重合"判定把关，猜错会被拒。
func synthPagingCandidates(catalogURL string, idPos idPosition) []string {
	tpl := idPos.substitute(catalogURL, "{cateId}")
	realID := idPos.extractID(catalogURL)
	if tpl == "" || realID == "" {
		return nil
	}
	parsed, err := url.Parse(tpl)
	if err != nil || parsed.Host == "" {
		return nil
	}
	path := parsed.Path
	lastSlash := strings.LastIndex(path, "/")
	if lastSlash < 0 {
		return nil
	}
	dir, file := path[:lastSlash+1], path[lastSlash+1:]
	var forms []string
	if dot := strings.LastIndex(file, "."); dot > 0 {
		stem, ext := file[:dot], file[dot:]
		// /list/{cateId}.html → -2 追加、_2 追加、以及 2 作目录层（/list/{cateId}/2.html）
		forms = append(forms, dir+stem+"-{catePg}"+ext, dir+stem+"_{catePg}"+ext, dir+stem+"/{catePg}"+ext)
	} else {
		forms = append(forms, dir+file+"-{catePg}", dir+file+"_{catePg}", dir+file+"/{catePg}")
	}
	// 查询参数形态：?page=2 / ?pg=2 / ?p=2（已有 query 时用 & 追加，原筛选参数保留）
	for _, key := range []string{"page", "pg", "p"} {
		if parsed.RawQuery == "" {
			forms = append(forms, path+"?"+key+"={catePg}")
		} else {
			forms = append(forms, path+"?"+parsed.RawQuery+"&"+key+"={catePg}")
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, form := range forms {
		candidate := strings.Replace(form, "{cateId}", realID, 1)
		candidate = strings.Replace(candidate, "{catePg}", "2", 1)
		if !strings.HasPrefix(candidate, "http") {
			candidate = parsed.Scheme + "://" + parsed.Host + candidate
		}
		if seen[candidate] || candidate == catalogURL {
			continue
		}
		seen[candidate] = true
		out = append(out, candidate)
	}
	return out
}

// idPosValid 分类 id 位置是否已被实测确定（path 段或 query 参数）。
// 注意：URL 按 "/" 切分后第 0 段恒为空串，所以合法的 pathIndex 必然 ≥1；
// 0（idPosition 零值）表示"未实测到位置"，必须判为无效，否则会把无位置误当有效。
func idPosValid(p idPosition) bool {
	return p.pathIndex > 0 || p.queryKey != ""
}

// firstDigitRun 返回字符串里第一个连续数字段（用于从分页差异里抽页码 token）。
func firstDigitRun(s string) string {
	start := -1
	for i, r := range s {
		if r >= '0' && r <= '9' {
			if start < 0 {
				start = i
			}
		} else if start >= 0 {
			return s[start:i]
		}
	}
	if start >= 0 {
		return s[start:]
	}
	return ""
}

// buildCombinedTemplate 在已知分类 id 位置的前提下，由（第1页 URL, 候选页 URL）产出
// 同时含 {cateId} 与 {catePg} 的组合模板：先把两 URL 的 {cateId} 段还原成占位符，
// 再对剩余差异段抽页码数字替换为 {catePg}，分隔符原样保留——这样 {cateId} 与 {catePg}
// 各占其真实位置，不再被当作同一个槽位互相覆盖。
// 例：list/2.html + list/2-2.html → list/{cateId}-{catePg}.html；
//
//	list/2.html + list/2/2.html   → list/{cateId}/{catePg}.html；
//	list/2.html + list/2.html?page=2 → list/{cateId}.html?page={catePg}。
func buildCombinedTemplate(catalogURL, candidate string, idPos idPosition) (string, bool) {
	base2 := idPos.substitute(catalogURL, "{cateId}")
	cand2 := idPos.substitute(candidate, "{cateId}")
	if base2 == "" || cand2 == "" {
		return "", false
	}
	prefix := commonPrefixLen(base2, cand2)
	restB := base2[prefix:]
	restC := cand2[prefix:]
	suffix := commonSuffixLen(restB, restC)
	if suffix > len(restB) || suffix > len(restC) {
		return "", false
	}
	candMid := restC[:len(restC)-suffix]
	if candMid == "" {
		return "", false
	}
	pageToken := firstDigitRun(candMid)
	var tmplMid string
	if pageToken == "" {
		// 退化：整段差异即页码（slug 形态），整体替换。
		tmplMid = "{catePg}"
	} else {
		tmplMid = strings.Replace(candMid, pageToken, "{catePg}", 1)
	}
	template := base2[:prefix] + tmplMid + restB[len(restB)-suffix:]
	return template, true
}

// differsByNumber a、b 仅差一段且该段是 +1 关系的数字。
func differsByNumber(a, b string) bool {
	prefix := commonPrefixLen(a, b)
	firstMiddle, secondMiddle, ok := splitDiff(a, b, prefix)
	if !ok {
		return false
	}
	na, errA := strconv.Atoi(firstMiddle)
	nb, errB := strconv.Atoi(secondMiddle)
	return errA == nil && errB == nil && nb == na+1
}

// buildPagingTemplate 用 (第1页URL, 候选页URL) 的字符串 diff 生成 {catePg} 模板：
// 公共前缀 + {catePg} + 公共后缀。差异中段两侧都必须是数字且 +1 关系才认可。
func buildPagingTemplate(catalogURL, secondURL string) (template, firstPage, secondPage string, ok bool) {
	prefix := commonPrefixLen(catalogURL, secondURL)
	firstMiddle, secondMiddle, ok := splitDiff(catalogURL, secondURL, prefix)
	if !ok {
		return "", "", "", false
	}
	na, errA := strconv.Atoi(firstMiddle)
	nb, errB := strconv.Atoi(secondMiddle)
	if errA != nil || errB != nil || nb != na+1 || len(firstMiddle) > 4 {
		return "", firstMiddle, secondMiddle, false
	}
	suffix := commonSuffixLen(catalogURL[prefix:], secondURL[prefix:])
	template = catalogURL[:prefix] + "{catePg}" + catalogURL[len(catalogURL)-suffix:]
	return template, firstMiddle, secondMiddle, true
}

// splitDiff 去掉公共前缀/后缀后取两侧差异段。
func splitDiff(a, b string, prefix int) (middleA, middleB string, ok bool) {
	if prefix >= len(a) || prefix >= len(b) {
		// 一方是另一方前缀（如 …/2 与 …/22），按剩余尾部解析数字差异。
		if prefix == len(a) {
			return "", b[prefix:], true
		}
		return a[prefix:], "", true
	}
	suffix := commonSuffixLen(a[prefix:], b[prefix:])
	endA, endB := len(a)-suffix, len(b)-suffix
	if endA < prefix || endB < prefix {
		return "", "", false
	}
	return a[prefix:endA], b[prefix:endB], true
}

func commonPrefixLen(a, b string) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	i := 0
	for i < limit && a[i] == b[i] {
		i++
	}
	return i
}

func commonSuffixLen(a, b string) int {
	i := 0
	for i < len(a) && i < len(b) && a[len(a)-1-i] == b[len(b)-1-i] {
		i++
	}
	return i
}

// entrySet 提取页面条目链接集合，用于两页比对。
// href 本身即可区分条目；页脚/导航两侧相同会抬高重合度，但真实翻页时
// 内容条目占大头，重合度仍会显著低于阈值（85%），不会误判成同页。
func entrySet(body string) map[string]bool {
	out := map[string]bool{}
	for _, tag := range aTagClosePattern.FindAllString(body, 3000) {
		href := hrefExtractPattern.FindStringSubmatch(tag)
		if href == nil {
			continue
		}
		value := strings.TrimSpace(href[1])
		if value == "" || strings.HasPrefix(strings.ToLower(value), "javascript") {
			continue
		}
		out[value] = true
	}
	return out
}

// overlap 两页条目重合百分比（以较少一侧为分母）。
func overlap(a, b map[string]bool) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	smaller, larger := a, b
	if len(b) < len(a) {
		smaller, larger = b, a
	}
	shared := 0
	for key := range smaller {
		if larger[key] {
			shared++
		}
	}
	return shared * 100 / len(smaller)
}

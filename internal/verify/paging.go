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
func probePaging(ctx context.Context, fetcher *xbpq.Fetcher, catalogURL, catalogBody string) PagingFinding {
	if strings.TrimSpace(catalogURL) == "" || strings.TrimSpace(catalogBody) == "" {
		return PagingFinding{Note: "分页实测 结论：跳过（缺少分类页样本）。"}
	}
	candidates := pagingCandidates(catalogURL, catalogBody)
	if len(candidates) == 0 {
		return PagingFinding{Note: "分页实测 结论：跳过（分类页里未找到「下一页/页码」链接，可能是 AJAX 翻页或该分类只有一页）。{catePg} 未经实测——先按样本最像页码的段写，最终以验证 paging 步骤实测为准。"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	first := entrySet(catalogBody)
	if len(first) < 3 {
		return PagingFinding{Note: fmt.Sprintf("分页实测 结论：跳过（分类页只提出 %d 个条目链接，样本不足）。", len(first))}
	}
	var failures []string
	for _, candidate := range candidates {
		template, firstPage, secondPage, ok := buildPagingTemplate(catalogURL, candidate)
		if !ok {
			failures = append(failures, fmt.Sprintf("%s（与第1页的差异段 %s→%s 不是 +1 数字，不像页码）", candidate, digitOrDash(firstPage), digitOrDash(secondPage)))
			continue
		}
		body, err := fetcher.Get(ctx, candidate, catalogURL)
		if err != nil || len(body) < 300 {
			failures = append(failures, candidate+"（拼接抓取失败"+errText(err)+"）")
			continue
		}
		second := entrySet(body)
		if len(second) == 0 {
			failures = append(failures, candidate+"（抓回页里提不出条目链接）")
			continue
		}
		score := overlap(first, second)
		if score >= 85 {
			// 拼得出去但内容同页：差异段多半是年份/地区之类筛选值，不是页码。
			failures = append(failures, fmt.Sprintf("%s（内容与第1页重合 %d%%，差异段不是页码而是筛选值）", candidate, score))
			continue
		}
		return PagingFinding{
			Confirmed: true,
			Template:  template,
			SecondURL: candidate,
			Note: fmt.Sprintf("分页实测 结论：通过\n实测分类url模板：\"%s\"\n第2页实测地址：%s\n与第1页条目重合仅 %d%%（第1页 %d 条 / 第2页 %d 条），确认翻页生效。写 分类url 时页码段逐字用 {catePg} 替换上面模板里的页码数字（其余段与筛选占位按需保留）——这是【实测结论】，优先级高于任何静态推断。",
				template, candidate, score, len(first), len(second)),
		}
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
func pagingCandidates(catalogURL, catalogBody string) []string {
	base, err := url.Parse(catalogURL)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
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
		qualified := nextPageLabelPattern.MatchString(tag) ||
			differsByNumber(base.String(), other.String())
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

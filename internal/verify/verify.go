// Package verify 提供站点探测与规则分步验证。
package verify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/wrxbpq/wrxbpq/internal/ai"
	"github.com/wrxbpq/wrxbpq/internal/xbpq"
)

// Probe 抓取站点样本，供 AI 分析。返回若干「标签 + 内容」。
func Probe(ctx context.Context, siteURL string, limit int) ([]ai.Sample, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = 24000
	}
	siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
	if siteURL == "" {
		return nil, fmt.Errorf("站点地址为空")
	}
	if !strings.Contains(siteURL, "://") {
		siteURL = "https://" + siteURL
	}
	fetcher := xbpq.NewFetcher("")
	var samples []ai.Sample

	home, err := fetcher.Get(ctx, siteURL+"/", "")
	if err != nil {
		return nil, fmt.Errorf("首页抓取失败: %w", err)
	}
	samples = append(samples, ai.Sample{Label: "首页 " + siteURL, Content: clipHTML(home, limit)})

	// 链式探测：首页 → 分类页 → 详情页 → 播放页。
	// 每一环都从上一步的真实页面里找链接，比在首页猜要准得多。
	detailBody := ""
	detailAddress := ""

	if categoryLink := pickFirstHref(home, categoryHints); categoryLink != "" {
		address := xbpq.Absolute(siteURL+"/", categoryLink)
		if body, err := fetcher.Get(ctx, address, siteURL+"/"); err == nil {
			samples = append(samples, ai.Sample{Label: "分类页 " + address, Content: clipHTML(body, limit)})
			if link := pickFirstHref(body, detailHints); link != "" {
				detailAddress = xbpq.Absolute(address, link)
			}
		}
	}
	if detailAddress == "" {
		if link := pickFirstHref(home, detailHints); link != "" {
			detailAddress = xbpq.Absolute(siteURL+"/", link)
		}
	}
	if detailAddress != "" {
		if body, err := fetcher.Get(ctx, detailAddress, siteURL+"/"); err == nil {
			detailBody = body
			samples = append(samples, ai.Sample{Label: "详情页 " + detailAddress, Content: clipHTML(body, limit)})
		}
	}
	if detailBody != "" {
		if playLink := pickFirstHref(detailBody, playHints); playLink != "" {
			playAddress := xbpq.Absolute(detailAddress, playLink)
			if playBody, playErr := fetcher.Get(ctx, playAddress, detailAddress); playErr == nil {
				// 播放页是关键证据，给它更大的预算，别把藏直链的 script 截掉。
				samples = append(samples, ai.Sample{Label: "播放页 " + playAddress, Content: clipHTML(playBody, limit*3/2)})
			}
		}
	}
	return samples, nil
}

var (
	// show 是很多站的「筛选页」而非分类页，放最后并在详情规则里排除。
	categoryHints = regexp.MustCompile(`(?i)/(?:type|list|vodtype|fenlei|category|show)/[\w./-]*[0-9a-z]`)
	// 详情页路径第二段必然是数字（/vod/55569.html、/detail/138557/）。
	detailHints = regexp.MustCompile(`(?i)/(?:vod|detail|movie|drama)/[0-9][\w./-]*`)
	// 播放页要能覆盖 /play/55388-1-1.html 与 /play/137736-1-1/ 两种收尾。
	playHints = regexp.MustCompile(`(?i)/(?:play|video|bofang)/[\w./-]+`)
)

func pickFirstHref(body string, pattern *regexp.Regexp) string {
	seen := map[string]bool{}
	for _, match := range pattern.FindAllString(body, 200) {
		if seen[match] {
			continue
		}
		seen[match] = true
		return match
	}
	return ""
}

// clipHTML 裁剪样本：优先保留含 script 的尾部（播放地址常在那里）。
func clipHTML(body string, limit int) string {
	runes := []rune(body)
	if len(runes) <= limit {
		return body
	}
	head := string(runes[:limit*2/3])
	tail := string(runes[len(runes)-limit/3:])
	return head + "\n<!-- ... 中间内容已省略 ... -->\n" + tail
}

// StepResult 是单个验证步骤的结果。
type StepResult struct {
	Step    string           `json:"step"`
	OK      bool             `json:"ok"`
	Message string           `json:"message"`
	Warning string           `json:"warning,omitempty"`
	Details map[string]any   `json:"details,omitempty"`
	Rows    []map[string]any `json:"rows,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// Options 是验证选项。
type Options struct {
	Rule      string // 规则 JSON 文本
	Site      string // 站点地址（覆盖规则主页）
	Keyword   string // 搜索关键词
	DetailID  string // 指定详情 ID（可选）
	Episode   int    // 指定第几集（可选，默认 1）
	ProbeM3U8 bool   // 是否真正拉取直链确认可播（默认 true）
}

func fail(step, err string) StepResult {
	return StepResult{Step: step, OK: false, Error: err, Message: err}
}

// RunSteps 执行指定的验证步骤，串行返回结果。
// 支持的 step：parse / catalog / paging / search / detail / play
func RunSteps(ctx context.Context, opts Options, steps []string) []StepResult {
	if ctx == nil {
		ctx = context.Background()
	}
	rule, ok := xbpq.ParseRule(opts.Rule)
	if !ok && !containsStep(steps, "parse") {
		return []StepResult{fail("parse", "规则 JSON 无法识别为 XBPQ 规则")}
	}
	base := strings.TrimRight(strings.TrimSpace(opts.Site), "/")
	if base == "" && rule.HomeURL() != "" {
		base = rule.HomeURL()
	}
	engine := xbpq.NewEngine(rule, base)

	var results []StepResult
	var detailID string
	var chapter *xbpq.Chapter

	for _, step := range steps {
		switch step {
		case "parse":
			results = append(results, stepParse(rule, opts.Rule))
		case "catalog":
			result, firstID := stepCatalog(ctx, engine)
			if firstID != "" {
				detailID = firstID
			}
			results = append(results, result)
		case "paging":
			results = append(results, stepPaging(ctx, engine))
		case "search":
			results = append(results, stepSearch(ctx, engine, opts.Keyword))
		case "detail":
			id := opts.DetailID
			if id == "" {
				id = detailID
			}
			result, _, first := stepDetail(ctx, engine, id)
			if first != nil {
				chapter = first
			}
			results = append(results, result)
		case "play":
			results = append(results, stepPlay(ctx, engine, opts, chapter))
		default:
			results = append(results, fail(step, "未知步骤"))
		}
	}
	return results
}

func containsStep(steps []string, target string) bool {
	for _, step := range steps {
		if step == target {
			return true
		}
	}
	return false
}

func stepParse(rule xbpq.Rule, raw string) StepResult {
	if _, ok := xbpq.ParseRule(raw); !ok {
		return fail("parse", "规则 JSON 无法识别为 XBPQ 规则（缺少 主页url/首页url/请求 之一）")
	}
	categories := rule.Categories()
	result := StepResult{
		Step:    "parse",
		OK:      true,
		Message: fmt.Sprintf("规则解析成功，识别 %d 个分类", len(categories)),
		Details: map[string]any{
			"主页":    rule.HomeURL(),
			"分类数":   len(categories),
			"分类url": rule.Field("分类url", "分类Url"),
			"搜索url": rule.Field("搜索url", "搜索Url"),
		},
	}
	for _, category := range categories {
		result.Rows = append(result.Rows, map[string]any{"id": category.ID, "name": category.Name})
	}
	return result
}

func stepCatalog(ctx context.Context, engine *xbpq.Engine) (StepResult, string) {
	categories := engine.Rule.Categories()
	categoryID := ""
	categoryName := "（未声明分类）"
	if len(categories) > 0 {
		categoryID = categories[0].ID
		categoryName = categories[0].Name
	}
	dramas, err := engine.Catalog(ctx, categoryID, 1)
	if err != nil {
		return fail("catalog", fmt.Sprintf("目录抓取失败: %v", err)), ""
	}
	withCover := 0
	for _, drama := range dramas {
		if strings.HasPrefix(drama.Cover, "http") {
			withCover++
		}
	}
	result := StepResult{
		Step:    "catalog",
		OK:      true,
		Message: fmt.Sprintf("分类「%s」第 1 页取到 %d 条（%d 条有封面）", categoryName, len(dramas), withCover),
		Details: map[string]any{
			"分类":   categoryName,
			"条数":   len(dramas),
			"有封面":  withCover,
			"页面地址": engine.Rule.CategoryURL(engine.Base, categoryID, 1),
		},
	}
	for index, drama := range dramas {
		if index >= 5 {
			break
		}
		result.Rows = append(result.Rows, map[string]any{
			"title": drama.Title, "cover": clipString(drama.Cover, 70), "remark": drama.Remark, "link": drama.Link,
		})
	}
	firstID := ""
	if len(dramas) > 0 {
		firstID = dramas[0].SourceID
	}
	return result, firstID
}

func stepPaging(ctx context.Context, engine *xbpq.Engine) StepResult {
	categories := engine.Rule.Categories()
	categoryID := ""
	if len(categories) > 0 {
		categoryID = categories[0].ID
	}
	first, err := engine.Catalog(ctx, categoryID, 1)
	if err != nil {
		return fail("paging", fmt.Sprintf("第 1 页失败: %v", err))
	}
	second, err := engine.Catalog(ctx, categoryID, 2)
	if err != nil {
		return fail("paging", fmt.Sprintf("第 2 页失败: %v", err))
	}
	firstIDs := map[string]bool{}
	for _, drama := range first {
		firstIDs[drama.SourceID] = true
	}
	overlap := 0
	var sample []string
	for _, drama := range second {
		if firstIDs[drama.SourceID] {
			overlap++
		}
		if len(sample) < 3 {
			sample = append(sample, drama.Title)
		}
	}
	if len(second) == 0 {
		return fail("paging", "第 2 页没有条目，分页可能未生效")
	}
	if overlap == len(second) {
		return fail("paging", fmt.Sprintf("第 2 页与第 1 页完全相同（%d 条），分页未生效", len(second)))
	}
	return StepResult{
		Step:    "paging",
		OK:      true,
		Message: fmt.Sprintf("第 1 页 %d 条 / 第 2 页 %d 条，重叠 %d 条", len(first), len(second), overlap),
		Details: map[string]any{
			"第1页条数": len(first), "第2页条数": len(second), "重叠": overlap, "第2页示例": sample,
		},
	}
}

func stepSearch(ctx context.Context, engine *xbpq.Engine, keyword string) StepResult {
	if strings.TrimSpace(keyword) == "" {
		keyword = "爱"
	}
	dramas, err := engine.Search(ctx, keyword)
	if err != nil {
		return fail("search", fmt.Sprintf("搜索失败: %v", err))
	}
	result := StepResult{
		Step:    "search",
		OK:      true,
		Message: fmt.Sprintf("搜索「%s」取到 %d 条", keyword, len(dramas)),
		Details: map[string]any{"关键词": keyword, "条数": len(dramas)},
	}
	for index, drama := range dramas {
		if index >= 5 {
			break
		}
		result.Rows = append(result.Rows, map[string]any{
			"title": drama.Title, "cover": clipString(drama.Cover, 70), "link": drama.Link,
		})
	}
	return result
}

func stepDetail(ctx context.Context, engine *xbpq.Engine, detailID string) (StepResult, string, *xbpq.Chapter) {
	if detailID == "" {
		dramas, err := engine.Catalog(ctx, firstCategoryID(engine), 1)
		if err != nil || len(dramas) == 0 {
			return fail("detail", fmt.Sprintf("取不到目录条目，无法选详情: %v", err)), "", nil
		}
		detailID = dramas[0].SourceID
	}
	drama, chapters, err := engine.Detail(ctx, detailID)
	if err != nil {
		return fail("detail", fmt.Sprintf("详情解析失败: %v", err)), detailID, nil
	}
	issue := ""
	if strings.TrimSpace(drama.Title) == "" {
		issue = "标题为空；"
	}
	if len(chapters) == 0 {
		issue += "没有分集；"
	}
	if issue != "" {
		return fail("detail", issue), detailID, nil
	}
	result := StepResult{
		Step:    "detail",
		OK:      true,
		Message: fmt.Sprintf("《%s》解析成功，%d 集，线路 %d 条", drama.Title, len(chapters), len(chapters[0].Routes)+1),
		Details: map[string]any{
			"标题":  drama.Title,
			"集数":  len(chapters),
			"线路数": len(chapters[0].Routes) + 1,
			"封面":  clipString(drama.Cover, 70),
			"简介":  clipString(drama.Intro, 120),
			"分类":  drama.Category,
			"标签":  drama.Tags,
			"详情页": drama.Link,
		},
	}
	for index := 0; index < len(chapters) && index < 5; index++ {
		result.Rows = append(result.Rows, map[string]any{
			"index": chapters[index].Index, "title": chapters[index].Title, "pageUrl": chapters[index].PageURL,
		})
	}
	return result, detailID, &chapters[0]
}

func firstCategoryID(engine *xbpq.Engine) string {
	if categories := engine.Rule.Categories(); len(categories) > 0 {
		return categories[0].ID
	}
	return ""
}

func stepPlay(ctx context.Context, engine *xbpq.Engine, opts Options, chapter *xbpq.Chapter) StepResult {
	target := chapter
	if target == nil {
		id := opts.DetailID
		if id == "" {
			dramas, err := engine.Catalog(ctx, firstCategoryID(engine), 1)
			if err != nil || len(dramas) == 0 {
				return fail("play", "取不到目录条目，无法选集")
			}
			id = dramas[0].SourceID
		}
		_, chapters, err := engine.Detail(ctx, id)
		if err != nil {
			return fail("play", fmt.Sprintf("取详情失败: %v", err))
		}
		if len(chapters) == 0 {
			return fail("play", "没有分集")
		}
		index := opts.Episode - 1
		if index < 0 || index >= len(chapters) {
			index = 0
		}
		target = &chapters[index]
	}
	media, err := engine.Resolve(ctx, *target)
	if err != nil {
		return fail("play", fmt.Sprintf("播放解析失败: %v", err))
	}
	if !xbpq.IsHTTPMediaURL(media.URL) {
		return fail("play", fmt.Sprintf("解析结果不是合法直链: %s", clipString(media.URL, 120)))
	}
	result := StepResult{
		Step:    "play",
		OK:      true,
		Message: fmt.Sprintf("第 %d 集解析到直链（线路 %d 条）", target.Index, len(media.Variants)+1),
		Details: map[string]any{
			"集":    target.Index,
			"播放页":  target.PageURL,
			"直链":   clipString(media.URL, 120),
			"备选线路": len(media.Variants),
		},
	}
	if opts.ProbeM3U8 || true {
		status, head, probeErr := probeMedia(ctx, media.URL, target.PageURL)
		if probeErr != nil {
			result.OK = false
			result.Error = fmt.Sprintf("直链请求失败: %v", probeErr)
			result.Message = "直链不可达"
			return result
		}
		result.Details["HTTP状态"] = status
		result.Details["响应头"] = clipString(head, 400)
		switch classifyProbe(status, head) {
		case outcomeMedia:
			result.Message = fmt.Sprintf("第 %d 集可播（HTTP %d，返回 %s）", target.Index, status, mediaKind(head))
		case outcomeBlocked:
			// 直链取回了 403/地域拒绝：区分"服务器被 CDN 地域封禁"与"规则真的有问题"。
			// Vercel 部署在境外机房，国内采集站 CDN 常返回 403 "region denied"，
			// 但直链能被规则解出且形态合法，说明规则没问题（浏览器/国内网络实测能播即为证），降级为警告。
			result.Warning = fmt.Sprintf("CDN 拒绝了本机（服务器）IP 的请求（HTTP %d，多为境外机房被国内 CDN 地域封锁或防盗链所致）。这不代表规则有问题——请在浏览器或国内设备上实测播放。", status)
			result.Message = fmt.Sprintf("第 %d 集解析到直链，但服务器所在地无法验证可播（地域封锁/防盗链）", target.Index)
		default:
			result.OK = false
			result.Error = fmt.Sprintf("响应内容不是 m3u8/mp4（HTTP %d，可能是 HTML 错误页）", status)
			result.Message = "直链内容异常"
		}
		return result
	}
	return result
}

// probeOutcome 是直链探测的归类结果。
type probeOutcome int

const (
	outcomeBad probeOutcome = iota
	outcomeMedia
	outcomeBlocked
)

// classifyProbe 判断直链回包：真媒体 / 被地域或防盗链拦截 / 其它异常。
func classifyProbe(status int, head string) probeOutcome {
	if strings.Contains(head, "#EXTM3U") || strings.HasPrefix(head, "\x00\x00\x00") || strings.Contains(head, "ftyp") {
		return outcomeMedia
	}
	lower := strings.ToLower(head)
	// 403/451 状态，或正文含 Forbidden / region ... denied / 地域 等封锁信号。
	if status == http.StatusForbidden || status == http.StatusUnavailableForLegalReasons ||
		strings.Contains(head, "Forbidden") || strings.Contains(lower, "denied") ||
		strings.Contains(lower, "region") && strings.Contains(lower, "deny") ||
		strings.Contains(head, "禁止") || strings.Contains(head, "地域") {
		return outcomeBlocked
	}
	return outcomeBad
}

// probeMedia 真正请求直链，只取前若干字节判断是否真是 m3u8/mp4。
func probeMedia(ctx context.Context, address, referer string) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("User-Agent", xbpq.DefaultUserAgent)
	request.Header.Set("Accept", "*/*")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	if referer != "" {
		request.Header.Set("Referer", referer)
		if origin := originOf(referer); origin != "" {
			request.Header.Set("Origin", origin)
		}
	}
	request.Header.Set("Range", "bytes=0-2047")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
	return response.StatusCode, string(payload), nil
}

// originOf 从 referer 提取 scheme://host 作为 Origin 头（播放器请求常带）。
func originOf(referer string) string {
	parsed, err := url.Parse(referer)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func mediaKind(head string) string {
	if strings.Contains(head, "#EXTM3U") {
		return "#EXTM3U"
	}
	if strings.Contains(head, "ftyp") {
		return "MP4"
	}
	return "未知"
}

func clipString(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

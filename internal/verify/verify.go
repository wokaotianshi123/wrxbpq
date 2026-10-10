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

	// 分类实测：导航候选分类逐个真实抓取，验证 {cateId} 段确实能出内容。
	// 结论进「分类检测」样本，指纹与 AI 以此为准，不再拿导航扒的数字直接叫 AI 照抄。
	categoryFinding := probeCategories(ctx, fetcher, siteURL, home)
	samples = append(samples, ai.Sample{Label: "分类检测", Content: categoryFinding.Note})

	// 链式探测：首页 → 分类页 → 详情页 → 播放页。
	// 每一环都从上一步的真实页面里找链接，比在首页猜要准得多。
	detailBody := ""
	detailAddress := ""
	directPlay := "" // 分类页/首页直挂的播放链接（详情环节断链时的兜底）

	// 分页实测的锚点：优先用「分类检测」已实测通过的分类 URL——这样分页探测会锁定
	// 该 {cateId}，不会把"下一类"链接误当"下一页"，从而产出含 {cateId}+{catePg} 的组合模板。
	pagingBase := ""
	if len(categoryFinding.Confirmed) > 0 {
		pagingBase = categoryFinding.Confirmed[0].URL
	} else if categoryLink := pickFirstHref(home, categoryHints); categoryLink != "" {
		pagingBase = xbpq.Absolute(siteURL+"/", categoryLink)
	}
	if pagingBase != "" {
		address := pagingBase
		if body, err := fetcher.Get(ctx, address, siteURL+"/"); err == nil {
			samples = append(samples, ai.Sample{Label: "分类页 " + address, Content: clipHTML(body, limit)})
			// 分页形态实测：拼第 2 页真抓回来和第 1 页比对，实测结论进「分页实测」样本。
			finding := probePaging(ctx, fetcher, address, body, categoryFinding.IDPosition)
			samples = append(samples, ai.Sample{Label: "分页实测", Content: finding.Note})
			if detail := pickFirstHref(body, detailHints); detail != "" {
				detailAddress = xbpq.Absolute(address, detail)
			}
			// 分类页里就有播放链接（部分站列表直接挂 /vodplay/…）：留作详情抓取失败时的兜底。
			if play := pickFirstHref(body, playHints); play != "" {
				directPlay = xbpq.Absolute(address, play)
			}
		}
	}
	if detailAddress == "" {
		if link := pickFirstHref(home, detailHints); link != "" {
			detailAddress = xbpq.Absolute(siteURL+"/", link)
		}
	}
	// 首页直挂播放链接（zmwgy.net 式 MacCMS：首页只有 /voddetail/ 与 /vodplay/，
	// 详情路径形态可能没进 detailHints 或详情抓取失败）：兜底样本别丢。
	if directPlay == "" {
		if play := pickFirstHref(home, playHints); play != "" {
			directPlay = xbpq.Absolute(siteURL+"/", play)
		}
	}
	if detailAddress != "" {
		if body, err := fetcher.Get(ctx, detailAddress, siteURL+"/"); err == nil {
			detailBody = body
			// 详情页给 1.3 倍预算：分集/线路容器在页面中后部，24000 截断会把
			// 第 2、3 条线路容器切掉，指纹多线路计数就错了。
			samples = append(samples, ai.Sample{Label: "详情页 " + detailAddress, Content: clipHTML(body, int(float64(limit)*1.3))})
		}
	}
	// 「详情url」模板样本：给出由真实详情页地址实测反推的 详情url 模板。
	// 该字段不是硬性必写（10-10 取消旧规定）——简写版可省略靠兜底，完整版应显式写出；
	// 无论哪版，写了就照这里的实测模板抄。模板从真实详情页地址反推（ID 段替换成 {id}
	// 后再拼回去、与原地址逐字一致才算实测通过），不做静态猜测。
	samples = append(samples, detailURLSample(detailAddress))
	playCaptured := false
	if playLink := pickFirstHref(detailBody, playHints); playLink != "" {
		playCaptured = fetchPlay(ctx, fetcher, xbpq.Absolute(detailAddress, playLink), detailAddress, &samples, limit)
	} else if directPlay != "" {
		playCaptured = fetchPlay(ctx, fetcher, directPlay, detailAddress, &samples, limit)
	}
	// 播放页没抓到样本（命名不在识别模式 / 抓取失败）→ 给诊断提示，别让 AI 对着没有播放页的样本瞎猜。
	if !playCaptured {
		unmatched := scanUnmatchedPlayHref(detailBody)
		if unmatched == "" {
			unmatched = scanUnmatchedPlayHref(home)
		}
		hint := "样本中未抓到播放页（详情页里没找到可识别的播放链接，或抓取失败）。"
		if unmatched != "" {
			hint += "详情页存在疑似播放链接但未命中识别模式（样例：" + unmatched + "）。"
		}
		hint += "写 内容进播放页链接/url_after 等字段时，请对照详情页样本里的分集 href 形态（如 /vodplay/id-x-y.html）推断，并以 play 步骤实测直链为准。"
		samples = append(samples, ai.Sample{Label: "播放页提示", Content: hint})
	}
	return samples, nil
}

// scanUnmatchedPlayHref 从页面正文找带 play 字样但 playHints 不认的 href 样例（供诊断提示）。
// 排除 #anchor 锚点（#con_playlist_1 之类 tab 切换不是播放页）。
func scanUnmatchedPlayHref(body string) string {
	for _, m := range hrefExtractPattern.FindAllStringSubmatch(body, 400) {
		value := strings.TrimSpace(m[1])
		if value == "" || strings.HasPrefix(value, "#") || playHints.MatchString(value) {
			continue
		}
		if strings.Contains(strings.ToLower(value), "play") {
			return clipString(value, 80)
		}
	}
	return ""
}

// fetchPlay 真抓播放页并追加样本；播放页是关键证据，给 1.5 倍预算。返回是否成功抓到。
func fetchPlay(ctx context.Context, fetcher *xbpq.Fetcher, address, referer string, samples *[]ai.Sample, limit int) bool {
	if strings.TrimSpace(address) == "" {
		return false
	}
	playBody, err := fetcher.Get(ctx, address, referer)
	if err != nil || strings.TrimSpace(playBody) == "" {
		return false
	}
	*samples = append(*samples, ai.Sample{Label: "播放页 " + address, Content: clipHTML(playBody, limit*3/2)})
	return true
}

// samplePureID 裸数字 ID（供详情url 模板反推）。
var samplePureID = regexp.MustCompile(`^[0-9]{1,12}$`)

// detailIDPosition 从真实详情页 URL 反推 详情url 模板：数字 ID 在路径段
// （/voddetail/120200.html、/detail/138557/、/type/tv/123/）或 query 参数（?id=123）形态逐一尝试；
// 产出后用"占位符回填 → 必须与原地址逐字一致"自校验——不做静态猜测。
func detailIDPosition(address string) (template, exampleID string, ok bool) {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" {
		return "", "", false
	}
	base := parsed.Scheme + "://" + parsed.Host
	segments := strings.Split(parsed.Path, "/")
	for index := len(segments) - 1; index >= 1; index-- {
		segment := segments[index]
		stem, ext := segment, ""
		if dot := strings.LastIndex(segment, "."); dot > 0 {
			stem, ext = segment[:dot], segment[dot:]
		}
		if !samplePureID.MatchString(stem) {
			continue
		}
		guard := segments[index]
		segments[index] = "{id}" + ext
		candidate := base + strings.Join(segments, "/")
		segments[index] = guard
		if strings.Replace(candidate, "{id}", stem, 1) == address {
			return candidate, stem, true
		}
	}
	// query 形态：恰好一个参数值是纯数字。
	query := parsed.Query()
	var keys []string
	for key := range query {
		if samplePureID.MatchString(query.Get(key)) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 1 {
		value := query.Get(keys[0])
		candidate := base + parsed.Path + "?" + strings.Replace(parsed.RawQuery, keys[0]+"="+value, keys[0]+"={id}", 1)
		if strings.Replace(candidate, "{id}", value, 1) == address {
			return candidate, value, true
		}
	}
	return "", "", false
}

// detailURLSample 生成「详情url 模板」样本：给出实测反推的 详情url 模板（或形态识别失败提示）。
// 详情url 非硬性必写——简写版可省略靠兜底，完整版应显式写出；写了就照本样本的实测模板抄。
func detailURLSample(detailAddress string) ai.Sample {
	if detailAddress == "" {
		return ai.Sample{Label: "详情url 模板", Content: "详情url 字段（占位符 {id}）不是硬性必写：简写版可省略，交给模板/列表链接还原兜底。" +
			"本次未抓到详情页，无法实测模板——若选完整版需要写 详情url，请按列表链接形态里找数字 ID 段换成 {id}，并以 detail 步骤实测为准。"}
	}
	template, exampleID, ok := detailIDPosition(detailAddress)
	if !ok {
		return ai.Sample{Label: "详情url 模板", Content: "详情url 字段（占位符 {id}）不是硬性必写：简写版可省略靠兜底。" +
			"详情页地址 " + detailAddress + " 里未识别出可反推的数字 ID 形态——若要写 详情url（完整版应写），从详情页样本里按站点实际 URL 结构写，写好后用 detail 步骤实测确认拼出的地址能打开。"}
	}
	return ai.Sample{Label: "详情url 模板", Content: "详情url 字段（占位符 {id}）不是硬性必写：简写版可省略交给兜底，完整版应显式写出。" +
		"\n实测详情url模板：\"" + template + "\"（由真实详情页 " + detailAddress + " 反推：ID 段 " + exampleID + " 换成 {id}，回填比对与原地址逐字一致）" +
		"\n要写 详情url 就照抄上面这个实测模板（列表条目链接里的数字 ID 会填进 {id}），禁止改形态；写完后以 detail 步骤实测能打开为准。"}
}

var (
	// show 是很多站的「筛选页」而非分类页，放最后并在详情规则里排除。
	categoryHints = regexp.MustCompile(`(?i)/(?:type|list|vodtype|fenlei|category|show)/[\w./-]*[0-9a-z]`)
	// 详情页路径第二段必然是数字（/vod/55569.html、/detail/138557/）。
	// vod 前缀可选：MacCMS 命名是 /voddetail/120200.html（zmwgy.net 式），
	// 旧写法 /(?:vod|detail)/ 要求斜杠紧跟 vod，/voddetail/ 整段不匹配 → 详情环节断链。
	// 末路 /[\w-]+/[0-9]{3,}\.html 补苹果CMS新版 mxone 皮肤（hanjuds.com 式）：
	// 详情是 /{分类目录}/{数字}.html（如 /80s/156040.html），没有 vod/detail 关键词段，
	// 原形态全部不匹配 → 分类页扒不到详情链接、Probe 停在分类页、指纹缺详情/播放样本。
	detailHints = regexp.MustCompile(`(?i)/(?:vod)?(?:detail|vod|movie|drama)/[0-9][\w./-]*|/[\w-]+/[0-9]{3,}\.html`)
	// 播放页要能覆盖 /play/55388-1-1.html、/play/137736-1-1/ 与 MacCMS 的 /vodplay/120200-1-1.html。
	// 末路补 mxone 连字符命名 /{目录}/play-{id}-{线}-{集}.html（hanjuds.com 式）：
	// 原形态要求 play/ 斜杠紧跟，play- 整段不匹配 → 详情页扒不到播放链接、play 环节断链。
	// 注意要连目录段一起捕获（/[\w-]+/play-…），只捕 /play-… 会丢目录前缀、拼出 404。
	playHints = regexp.MustCompile(`(?i)/(?:vod)?(?:play|bofang)/[\w./-]+|/video/[\w./-]+|/[\w-]+/[\w-]*play-[0-9][\w.-]*`)
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
	routeCount := 1
	for _, chapter := range chapters {
		if n := len(chapter.Routes) + 1; n > routeCount {
			routeCount = n
		}
	}
	result := StepResult{
		Step:    "detail",
		OK:      true,
		Message: fmt.Sprintf("《%s》解析成功，%d 集，线路 %d 条", drama.Title, len(chapters), routeCount),
		Details: map[string]any{
			"标题":  drama.Title,
			"集数":  len(chapters),
			"线路数": routeCount,
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

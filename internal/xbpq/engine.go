package xbpq

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/html"
)

// Drama 是列表/详情层的剧集条目。
type Drama struct {
	ID         string   `json:"id"`
	SourceID   string   `json:"sourceId"`
	Title      string   `json:"title"`
	Cover      string   `json:"cover"`
	Remark     string   `json:"remark"`
	Intro      string   `json:"intro,omitempty"`
	Category   string   `json:"category,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Link       string   `json:"link"`
	RouteCount int      `json:"routeCount,omitempty"`
}

// Chapter 是一集。
type Chapter struct {
	Index   int      `json:"index"`
	Title   string   `json:"title"`
	PageURL string   `json:"pageUrl"`
	Routes  []string `json:"routes,omitempty"`
}

// Media 是解析出的可播放地址。
type Media struct {
	URL      string  `json:"url"`
	Referer  string  `json:"referer,omitempty"`
	Variants []Media `json:"variants,omitempty"`
}

// Engine 按一份规则执行采集。
type Engine struct {
	Rule    Rule
	Base    string
	Fetcher *Fetcher
}

// NewEngine 构造引擎。base 为空时取规则主页。
func NewEngine(rule Rule, base string) *Engine {
	if strings.TrimSpace(base) == "" {
		base = rule.HomeURL()
	}
	return &Engine{Rule: rule, Base: base, Fetcher: NewFetcher(rule.UserAgent())}
}

// ---- 条目抽取 ----

// item 是抽取出的一个列表条目（selector 模式带节点，cut 模式只有文本段）。
type item struct {
	text string
	node *html.Node
	doc  *html.Node
}

func (e *Engine) extractItems(ctx context.Context, pageURL, prefix string) []item {
	body, err := e.Fetcher.Get(ctx, pageURL, e.Base+"/")
	if err != nil || body == "" {
		return nil
	}
	arrayPattern := e.Rule.Field(prefix+"数组", "数组")
	if arrayPattern == "" {
		if jsonItems := jsonList(body); len(jsonItems) > 0 {
			return jsonItems
		}
		return nil
	}
	document, _ := html.Parse(strings.NewReader(body))
	if strings.HasPrefix(arrayPattern, "p:") || strings.HasPrefix(arrayPattern, "jsoup:") {
		sel, ok := parseSelector(arrayPattern)
		if !ok {
			return nil
		}
		var items []item
		for _, node := range selectorNodes(document, sel) {
			items = append(items, item{node: node, doc: document, text: RenderHTML(node)})
		}
		return items
	}
	// 截掉 [列表] 之外的部分：二次截取先行
	if region := e.Rule.Field(prefix+"二次截取", "二次截取"); region != "" {
		if cut := CutOnce(body, region); cut != "" {
			body = cut
			document, _ = html.Parse(strings.NewReader(body))
		}
	}
	var items []item
	for _, segment := range List(body, arrayPattern) {
		items = append(items, item{text: segment, doc: document})
	}
	if len(items) == 0 {
		if jsonItems := jsonList(body); len(jsonItems) > 0 {
			return jsonItems
		}
	}
	return items
}

// jsonList 兜底：页面里嵌着 MacCMS 风格 JSON 时直接解析。
func jsonList(body string) []item {
	start := strings.Index(body, `[{`)
	if start < 0 {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(body[start:]))
	var payload any
	if decoder.Decode(&payload) != nil {
		return nil
	}
	var rows []any
	switch typed := payload.(type) {
	case []any:
		rows = typed
	case map[string]any:
		for _, key := range []string{"list", "data", "result", "rows"} {
			if entries, found := typed[key].([]any); found {
				rows = entries
				break
			}
		}
	}
	var items []item
	for _, row := range rows {
		node, ok := row.(map[string]any)
		if !ok {
			continue
		}
		body, err := json.Marshal(node)
		if err != nil {
			continue
		}
		items = append(items, item{text: string(body)})
	}
	return items
}

// itemField 从条目里按 pattern 取值。
func itemField(entry item, pattern string) string {
	if pattern == "" || entry.text == "" {
		return ""
	}
	if strings.HasPrefix(pattern, "p:") || strings.HasPrefix(pattern, "jsoup:") {
		if entry.node != nil {
			sel, ok := parseSelector(pattern)
			if !ok {
				return ""
			}
			nodes := selectorNodes(entry.node, sel)
			if len(nodes) == 0 && entry.doc != nil {
				nodes = selectorNodes(entry.doc, sel)
			}
			for _, node := range nodes {
				if !PartWithin(node, entry.node) {
					continue
				}
				if sel.target.attr != "" {
					return strings.TrimSpace(HTMLAttr(node, sel.target.attr))
				}
				return HTMLText(node)
			}
			if len(nodes) > 0 {
				node := nodes[0]
				if sel.target.attr != "" {
					return strings.TrimSpace(HTMLAttr(node, sel.target.attr))
				}
				return HTMLText(node)
			}
			return ""
		}
		return SelectorFirstString(entry.text, pattern)
	}
	if strings.Contains(pattern, `"`) && strings.Contains(entry.text, "{") && json.Valid([]byte(entry.text)) {
		if value := jsonField(entry.text, pattern); value != "" {
			return value
		}
	}
	return CutOnce(entry.text, pattern)
}

func jsonField(itemJSON, pattern string) string {
	var node map[string]any
	if json.Unmarshal([]byte(itemJSON), &node) != nil {
		return ""
	}
	key := CutOnce(pattern, `"&&"`)
	if key == "" {
		key = strings.NewReplacer(`"`, "", ":", "", "&&", "", "+", "").Replace(pattern)
		key = strings.TrimSpace(key)
	}
	for name, value := range node {
		if strings.EqualFold(strings.TrimSpace(name), key) {
			return stringValue(value)
		}
	}
	return ""
}

// ---- 目录 ----

var catalogNumericCategory = regexp.MustCompile(`^/?(\d{1,6})(?:\.html)?$`)

// Catalog 抓一页目录。categoryID 为空时取规则首个分类。
func (e *Engine) Catalog(ctx context.Context, categoryID string, page int) ([]Drama, error) {
	categoryID = strings.TrimSpace(categoryID)
	if matches := catalogNumericCategory.FindStringSubmatch(strings.TrimPrefix(categoryID, "/")); len(matches) > 1 {
		categoryID = matches[1]
	}
	if categoryID == "" {
		if categories := e.Rule.Categories(); len(categories) > 0 {
			categoryID = categories[0].ID
		}
	}
	pageURL := e.Rule.CategoryURL(e.Base, categoryID, page)
	if pageURL == "" {
		return nil, fmt.Errorf("规则缺少分类url")
	}
	prefix := ""
	if categoryID != "" {
		prefix = "列表"
	}
	items := e.extractItems(ctx, pageURL, prefix)
	if len(items) == 0 {
		return nil, fmt.Errorf("规则未抽取到列表条目")
	}
	titlePattern := e.Rule.Field("标题", prefix+"标题")
	linkPattern := e.Rule.Field("链接", prefix+"链接")
	picturePattern := e.Rule.Field("图片", prefix+"图片", "主图")
	remarkPattern := e.Rule.Field("副标题", prefix+"副标题", "备注")
	numericID := e.Rule.Field("详情url", "详情页url") != ""
	var dramas []Drama
	for _, entry := range items {
		title := CleanText(itemField(entry, titlePattern))
		link := JoinLink(pageURL, itemField(entry, linkPattern))
		if title == "" || link == "" || !strings.HasPrefix(link, "http") {
			continue
		}
		id := IDFromLink(link, numericID)
		dramas = append(dramas, Drama{
			ID:       id,
			SourceID: id,
			Title:    title,
			Cover:    JoinLink(pageURL, itemField(entry, picturePattern)),
			Remark:   CleanText(itemField(entry, remarkPattern)),
			Link:     link,
		})
	}
	if len(dramas) == 0 {
		return nil, fmt.Errorf("规则抽取的条目缺少标题或链接")
	}
	return dramas, nil
}

// ---- 搜索 ----

// Search 按关键词搜索。
func (e *Engine) Search(ctx context.Context, query string) ([]Drama, error) {
	template := e.Rule.Field("搜索url", "搜索Url")
	if template == "" {
		return nil, fmt.Errorf("规则缺少搜索url")
	}
	encoded := url.QueryEscape(query)
	pathEscaped := url.PathEscape(query)
	candidates := []string{
		strings.NewReplacer("{wd}", encoded).Replace(template),
		strings.NewReplacer("{wd}", pathEscaped).Replace(template),
	}
	var lastErr error
	for _, candidate := range candidates {
		address, method, body := splitSpec(candidate)
		if address == "" {
			continue
		}
		if !strings.Contains(address, "{wd}") {
			pageURL := RenderURL(address, e.Base, map[string]string{"wd": encoded})
			if method != "" && strings.Contains(body, "{wd}") {
				body = strings.ReplaceAll(body, "{wd}", query)
			}
			spec := ComposeSpec(pageURL, method, body)
			items := e.extractItems(ctx, spec, "搜索")
			if len(items) == 0 {
				items = e.extractItems(ctx, spec, "")
			}
			if len(items) == 0 {
				lastErr = fmt.Errorf("搜索页未抽取到结果")
				continue
			}
			dramas := e.itemsToCatalog(pageURL, items, "搜索")
			if len(dramas) == 0 {
				lastErr = fmt.Errorf("搜索结果缺少标题或链接")
				continue
			}
			return dramas, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("搜索规则不可用")
	}
	return nil, lastErr
}

func (e *Engine) itemsToCatalog(pageURL string, items []item, prefix string) []Drama {
	titlePattern := e.Rule.Field(prefix+"标题", "标题")
	linkPattern := e.Rule.Field(prefix+"链接", "链接")
	picturePattern := e.Rule.Field(prefix+"图片", "图片")
	remarkPattern := e.Rule.Field(prefix+"副标题", "副标题")
	numericID := e.Rule.Field("详情url", "详情页url") != ""
	var dramas []Drama
	for _, entry := range items {
		title := CleanText(itemField(entry, titlePattern))
		link := JoinLink(pageURL, itemField(entry, linkPattern))
		if title == "" || link == "" || !strings.HasPrefix(link, "http") {
			continue
		}
		id := IDFromLink(link, numericID)
		dramas = append(dramas, Drama{
			ID:       id,
			SourceID: id,
			Title:    title,
			Cover:    JoinLink(pageURL, itemField(entry, picturePattern)),
			Remark:   CleanText(itemField(entry, remarkPattern)),
			Link:     link,
		})
	}
	return dramas
}

// ---- 详情与分集 ----

type episode struct {
	title string
	url   string
}

// Detail 抓详情页并解析分集。
func (e *Engine) Detail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	pageURL := LinkFromID(e.Base, sourceID)
	if pageURL == "" {
		pageURL = RenderURL(e.Rule.Field("详情url", "详情页url"), e.Base, map[string]string{"id": sourceID})
	}
	if pageURL == "" || !strings.HasPrefix(pageURL, "http") {
		return Drama{}, nil, fmt.Errorf("无法定位详情页地址")
	}
	body, err := e.Fetcher.Get(ctx, pageURL, e.Base+"/")
	if err != nil {
		return Drama{}, nil, err
	}
	document, _ := html.Parse(strings.NewReader(body))
	entry := item{text: body, doc: document}
	pick := func(patterns ...string) string {
		for _, pattern := range patterns {
			if pattern == "" {
				continue
			}
			if value := itemField(entry, pattern); value != "" {
				return CleanText(value)
			}
		}
		return ""
	}
	title := pick(e.Rule.Field("影片名称"), e.Rule.Field("name"))
	if title == "" {
		title = DetailTitle(document)
	}
	if title == "" {
		for _, name := range []string{"h1", "h2"} {
			nodes := HTMLNodes(document, func(node *html.Node) bool { return node.Data == name })
			if len(nodes) > 0 {
				if text := CleanText(HTMLText(nodes[0])); len([]rune(text)) >= 2 && len([]rune(text)) <= 60 {
					title = text
				}
				break
			}
		}
	}
	if title == "" {
		if nodes := HTMLNodes(document, func(node *html.Node) bool { return node.Data == "h1" }); len(nodes) > 0 {
			title = CleanText(HTMLText(nodes[0]))
		}
	}
	intro := pick(e.Rule.Field("简介"), DetailIntro(document))
	cover := ""
	if pattern := e.Rule.Field("封面", "图片"); pattern != "" {
		cover = JoinLink(pageURL, itemField(entry, pattern))
	}
	if cover == "" {
		cover = DetailCover(document, pageURL)
	}
	category := pick(e.Rule.Field("类型"), DetailCategory(document))
	drama := Drama{
		ID:       sourceID,
		SourceID: sourceID,
		Title:    title,
		Intro:    intro,
		Cover:    cover,
		Category: category,
		Remark:   pick(e.Rule.Field("状态", "影片状态")),
		Link:     pageURL,
	}
	if director := pick(e.Rule.Field("导演")); director != "" {
		drama.Tags = append(drama.Tags, "导演:"+director)
	}
	if actor := pick(e.Rule.Field("主演")); actor != "" {
		drama.Tags = append(drama.Tags, "主演:"+actor)
	}
	episodes, routeGroups := e.episodes(body, pageURL)
	if len(episodes) == 0 {
		return Drama{}, nil, fmt.Errorf("规则未解析到分集")
	}
	var chapters []Chapter
	for index, one := range episodes {
		link := JoinLink(pageURL, one.url)
		if link == "" {
			continue
		}
		chapters = append(chapters, Chapter{
			Index:   index + 1,
			Title:   one.title,
			PageURL: link,
			Routes:  alternateRoutes(routeGroups, index, one, link, pageURL),
		})
	}
	if len(chapters) == 0 {
		return Drama{}, nil, fmt.Errorf("规则未解析到可播放分集")
	}
	drama.RouteCount = len(routeGroups)
	return drama, chapters, nil
}

// episodes 解析播放串：播放数组→$$$ 线路→# 分集→标题$链接。
// 返回「集数最多的那条线路」与「全部线路」（供多线路切换）。
func (e *Engine) episodes(body, pageURL string) ([]episode, [][]episode) {
	arrayPattern := e.Rule.Field("播放数组")
	if arrayPattern == "" {
		return nil, nil
	}
	raw := ""
	if routeArray := e.Rule.Field("线路数组"); routeArray != "" {
		if segments := List(body, routeArray); len(segments) > 0 {
			raw = strings.Join(segments, "$$$")
		}
	}
	if raw == "" {
		raw = CutOnce(body, arrayPattern)
	}
	if raw == "" {
		return nil, nil
	}
	raw = strings.ReplaceAll(raw, "\r\n", "#")
	if strings.Contains(raw, `\/`) {
		raw = strings.ReplaceAll(raw, `\/`, "/")
	}
	listSplit := e.Rule.Field("播放列表")
	if listSplit == "" {
		listSplit = "#"
	} else if listSplit == "&&" {
		listSplit = "#"
	}
	titlePattern := e.Rule.Field("播放标题")
	linkPattern := e.Rule.Field("播放链接")
	var best []episode
	var routes [][]episode
	for _, group := range strings.Split(raw, "$$$") {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		var entries []episode
		for _, one := range strings.Split(group, listSplit) {
			one = strings.TrimSpace(one)
			if one == "" {
				continue
			}
			name, address := one, ""
			if head, tail, found := strings.Cut(one, "$"); found {
				name, address = head, tail
			}
			if titlePattern != "" {
				if cut := CutOnce(one, titlePattern); cut != "" {
					name = cut
				}
			}
			if linkPattern != "" {
				if cut := CutOnce(one, linkPattern); cut != "" {
					address = cut
				}
			}
			if strings.HasPrefix(address, "$") {
				continue
			}
			address = NormalizePlaybackURL(strings.TrimSpace(address))
			name = CleanText(name)
			if name == "" && strings.Contains(address, "://") {
				name = "第" + strconv.Itoa(len(entries)+1) + "集"
			}
			if name == "" || address == "" {
				continue
			}
			entries = append(entries, episode{title: name, url: address})
		}
		if len(entries) > len(best) {
			best = entries
		}
		if len(entries) > 0 {
			routes = append(routes, entries)
		}
	}
	absolutize := func(one episode) (episode, bool) {
		link := one.url
		if !strings.Contains(link, "://") && !strings.HasPrefix(link, "/") {
			return episode{}, false
		}
		if !strings.HasPrefix(link, "http") {
			link = Absolute(pageURL, link)
		}
		return episode{title: one.title, url: link}, true
	}
	var out []episode
	for _, one := range best {
		if fixed, ok := absolutize(one); ok {
			out = append(out, fixed)
		}
	}
	var outRoutes [][]episode
	for _, group := range routes {
		var fixed []episode
		for _, one := range group {
			if entry, ok := absolutize(one); ok {
				fixed = append(fixed, entry)
			}
		}
		if len(fixed) > 0 {
			outRoutes = append(outRoutes, fixed)
		}
	}
	return out, outRoutes
}

// alternateRoutes 收集同一集在其它线路上的播放页地址。
func alternateRoutes(groups [][]episode, index int, main episode, mainLink, pageURL string) []string {
	if len(groups) < 2 {
		return nil
	}
	var out []string
	seen := map[string]bool{mainLink: true}
	for _, group := range groups {
		if index >= len(group) {
			continue
		}
		one := group[index]
		if one.title != "" && main.title != "" && one.title != main.title {
			continue
		}
		link := JoinLink(pageURL, one.url)
		if link == "" || seen[link] {
			continue
		}
		seen[link] = true
		out = append(out, link)
	}
	return out
}

// ---- 播放解析 ----

// Resolve 解析一集的播放页，返回直链与可切换线路。
func (e *Engine) Resolve(ctx context.Context, chapter Chapter) (Media, error) {
	pageURL := strings.TrimSpace(chapter.PageURL)
	if pageURL == "" {
		return Media{}, fmt.Errorf("分集缺少播放页地址")
	}
	referer := e.Base + "/"
	media, err := e.resolveOne(ctx, pageURL, referer)
	if err == nil {
		media.Variants = e.resolveRoutes(ctx, chapter.Routes)
		return media, nil
	}
	for _, route := range chapter.Routes {
		if route == "" || route == pageURL {
			continue
		}
		if alt, altErr := e.resolveOne(ctx, route, referer); altErr == nil {
			alt.Variants = e.resolveRoutes(ctx, without(chapter.Routes, route))
			return alt, nil
		}
	}
	return Media{}, err
}

// resolveOne 单个播放页取直链。
func (e *Engine) resolveOne(ctx context.Context, pageURL, referer string) (Media, error) {
	body, err := e.Fetcher.Get(ctx, pageURL, referer)
	if err != nil {
		return Media{}, err
	}
	if jump := e.Rule.Field("跳转播放链接"); jump != "" {
		for _, candidate := range jumpCandidates(body, jump) {
			candidate = JoinLink(pageURL, candidate)
			candidate = NormalizePlaybackURL(candidate)
			if IsHTTPMediaURL(candidate) && LooksLikeMedia(candidate) {
				return Media{URL: candidate, Referer: pageURL}, nil
			}
			if strings.HasPrefix(candidate, "http") && !LooksLikeMedia(candidate) {
				if second, secondErr := e.Fetcher.Get(ctx, candidate, pageURL); secondErr == nil {
					if direct := jumpValue(second, jump); direct != "" {
						direct = NormalizePlaybackURL(direct)
						if IsHTTPMediaURL(direct) {
							return Media{URL: direct, Referer: candidate}, nil
						}
					}
				}
			}
		}
	}
	if address := PlayerURL(body); address != "" {
		address = NormalizePlaybackURL(address)
		if IsHTTPMediaURL(address) {
			return Media{URL: address, Referer: pageURL}, nil
		}
	}
	return Media{}, fmt.Errorf("未解析到播放地址")
}

// resolveRoutes 并发解析其它线路；失败的线路自动剔除。
func (e *Engine) resolveRoutes(ctx context.Context, routes []string) []Media {
	var targets []string
	for _, route := range routes {
		route = strings.TrimSpace(route)
		if route != "" {
			targets = append(targets, route)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	const maxConcurrent = 4
	sem := make(chan struct{}, maxConcurrent)
	results := make([]Media, len(targets))
	done := make([]bool, len(targets))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for index, route := range targets {
		wg.Add(1)
		go func(slot int, address string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			got, err := e.resolveOne(ctx, address, e.Base+"/")
			if err != nil || got.URL == "" {
				return
			}
			mu.Lock()
			results[slot], done[slot] = got, true
			mu.Unlock()
		}(index, route)
	}
	wg.Wait()
	var variants []Media
	for index, ok := range done {
		if ok {
			variants = append(variants, results[index])
		}
	}
	return variants
}

func without(routes []string, skip string) []string {
	var out []string
	for _, route := range routes {
		if route == skip {
			continue
		}
		out = append(out, route)
	}
	return out
}

func jumpValue(body, jump string) string {
	value := CutOnce(body, jump)
	if value == "" {
		return ""
	}
	if index := strings.IndexAny(value, "\r\n"); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}

func jumpCandidates(body, jump string) []string {
	var out []string
	if value := jumpValue(body, jump); value != "" {
		out = append(out, value)
	}
	for _, part := range strings.Split(jump, "||") {
		steps := parseSteps(part)
		if len(steps) == 0 {
			continue
		}
		segment, _, ok := applyStepScan(body, steps[0])
		if ok && !strings.Contains(segment, "\n") && !strings.Contains(segment, " ") {
			out = append(out, segment)
		}
	}
	var deduped []string
	seen := map[string]bool{}
	for _, value := range out {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		deduped = append(deduped, value)
	}
	return deduped
}

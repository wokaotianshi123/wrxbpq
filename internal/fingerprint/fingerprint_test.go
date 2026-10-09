package fingerprint

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wrxbpq/wrxbpq/internal/ai"
	"github.com/wrxbpq/wrxbpq/internal/verify"
)

// TestAnalyzeFFV1RealSite 对真实站点跑一遍 Probe + Analyze，肉眼校验指纹质量。
// 需要网络；默认跳过，设 WRXBQP_ONLINE=1 时执行：go test ./internal/fingerprint/ -run FFV1 -v
func TestAnalyzeFFV1RealSite(t *testing.T) {
	if os.Getenv("WRXBQP_ONLINE") == "" {
		t.Skip("设 WRXBQP_ONLINE=1 才跑联网指纹测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	samples, err := verify.Probe(ctx, "https://www.ffv1.com", 0)
	if err != nil {
		t.Fatalf("Probe 失败: %v", err)
	}
	for _, sample := range samples {
		t.Logf("样本 %s — %d 字符", sample.Label, len(sample.Content))
	}
	text := Analyze(samples)
	t.Logf("\n%s", text)

	// 关键锚点必须出现在指纹里（这些正是 AI 曾经抄错的地方）
	musts := []string{"条目边界锚点", "播放数组 建议", "代表条目原文"}
	for _, token := range musts {
		if !strings.Contains(text, token) {
			t.Errorf("指纹缺少关键行 %q", token)
		}
	}
	// 播放数组建议必须是去尾 > 的形态
	if strings.Contains(text, `播放数组 建议 "<div class=\"row\">&&`) {
		t.Errorf("播放数组 建议带了闭合 >（AI 踩过的坑没被纠正）")
	}
	// 指纹建议的播放数组锚点应能匹配真实页面（与已验证规则等价的前缀形态）
	if line := fingerprintPlayAnchor(t, text); line != "" {
		if !strings.Contains(line, `class=\"row\"`) || strings.Contains(line, "style") {
			t.Errorf("播放数组 建议锚点不理想: %s", line)
		}
	}
}

// fingerprintPlayAnchor 从指纹文本里取出 播放数组 建议 的 pattern。
func fingerprintPlayAnchor(t *testing.T, text string) string {
	t.Helper()
	marker := `播放数组 建议 "`
	at := strings.Index(text, marker)
	if at < 0 {
		return ""
	}
	rest := text[at+len(marker):]
	end := strings.Index(rest, "\"——")
	if end < 0 {
		end = strings.Index(rest, "\n")
	}
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// TestCheckBadRule 用用户 AI 真实产出的坏规则验证格式自检能抓到问题。
func TestCheckBadRule(t *testing.T) {
	samples := []ai.Sample{{
		Label:   "分类页 https://www.ffv1.com/type/tv/1/",
		Content: `<ul class="stui-vodlist__media"><li class="active"><a href="/detail/123/" title="片名">片名</a><span class="pic"><a data-original="https://img/x.jpg"></a></span></li></ul>`,
	}, {
		Label:   "详情页 https://www.ffv1.com/detail/123/",
		Content: `<div class="row" style="display: block;"><ul class="list16"><li><a href="/play/123-1-1/">第01集</a></li></ul></div>`,
	}}
	bad := `{
"主页url": "https://www.ffv1.com",
"数组": "<li class=\"videoItem\" id=\"\">&&</li>",
"标题": "title=\\\"&&\\\"",
"播放数组": "<div class=\"row\">&&</div>",
"线路数组": "<div class=\"row\">&&</div>",
"播放列表": "<li>",
"跳转播放链接": "url: '&&'"
}`
	issues := Check(bad, samples)
	if len(issues) == 0 {
		t.Fatalf("坏规则应被检出问题，实际 0 条")
	}
	found := map[string]bool{}
	for _, issue := range issues {
		t.Logf("检出 [%s] %s", issue.Field, issue.Problem)
		found[issue.Field] = true
	}
	// 数组锚点样本里没有 → 应检出；播放数组带 > → 锚点在样本里一字未现（真实带 style=）→ 应检出；
	// 标题 含字面 \" → 应检出；线路数组=播放数组 → 应检出。
	for _, want := range []string{"数组", "标题", "播放数组", "线路数组"} {
		if !found[want] {
			t.Errorf("未检出字段 %q", want)
		}
	}
}

// TestCheckGoodRule 已验证规则应零误报。
func TestCheckGoodRule(t *testing.T) {
	samples := []ai.Sample{{
		Label:   "分类页",
		Content: `<div class="search container"><ul><li><a href="/detail/1/" title="A"><img data-original="https://i/a.jpg"></a><p>更新至1集</p></li><li><a href="/detail/2/" title="B"><img data-original="https://i/b.jpg"></a><p>更新至2集</p></li><li><a href="/detail/3/" title="C"><img data-original="https://i/c.jpg"></a><p>更新至3集</p></li><li><a href="/detail/4/" title="D"><img data-original="https://i/d.jpg"></a><p>更新至4集</p></li><li><a href="/detail/5/" title="E"><img data-original="https://i/e.jpg"></a><p>更新至5集</p></li><li><a href="/detail/6/" title="F"><img data-original="https://i/f.jpg"></a><p>更新至6集</p></li></ul></div>`,
	}, {
		Label:   "详情页",
		Content: `<div class="row" style="display: block;"><ul class="list16"><li><a href="/play/1-1-1/">第01集</a></li><li><a href="/play/1-1-2/">第02集</a></li><li><a href="/play/1-1-3/">第03集</a></li><li><a href="/play/1-1-4/">第04集</a></li><li><a href="/play/1-1-5/">第05集</a></li><li><a href="/play/1-1-6/">第06集</a></li></ul></div>`,
	}, {
		Label:   "播放页",
		Content: `<script type="text/javascript">var player_aaaa={"flag":"play","encrypt":0}; const config = {url: 'https://cdn.example.com/2026/index.m3u8',autoplay: true}</script>`,
	}}
	good := `{
"主页url": "https://www.ffv1.com",
"二次截取": "<div class=\"search container\">&&",
"数组": "<li>&&</li>",
"标题": "title=\"&&\"",
"链接": "href=\"&&\"",
"列表图片": "data-original=\"&&\"",
"副标题": "<p>&&</p>",
"播放数组": "<div class=\"row\"&&</div>",
"播放列表": "<li>",
"播放标题": ">&&</a>",
"播放链接": "href=\"&&\"",
"跳转播放链接": "url: '&&'"
}`
	issues := Check(good, samples)
	for _, issue := range issues {
		t.Errorf("好规则被误报: [%s] %s", issue.Field, issue.Problem)
	}
	fp := Analyze(samples)
	t.Logf("\n%s", fp)
	if !containsStr(fp, "第01集") {
		t.Errorf("指纹未包含分集条目原文")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// TestAnalyzeMacCMSStyleSite 固化 6789ysw.com（kankan/stui 模板）形态：
// 分集容器是 ul、多线路、player_aaaa 转义直链。指纹必须给出
// ul&&</ul> 播放数组、多线路指引、省略 跳转播放链接 建议。
func TestAnalyzeMacCMSStyleSite(t *testing.T) {
	var items strings.Builder
	for i := 1; i <= 10; i++ {
		items.WriteString(`<li class="col-md-6 col-sm-4 col-xs-3"><div class="stui-vodlist__box"><a class="stui-vodlist__thumb lazyload" href="/vod/` + string(rune('0'+i)) + `55.html" title="片` + string(rune('0'+i)) + `" data-original="https://img/x` + string(rune('0'+i)) + `.jpg"><span class="pic-text text-right">HD</span></a></div></li>`)
	}
	catalog := `<html><body class="body"><div class="col-lg-wide-75 col-xs-1 padding-0"><div class="row">` + items.String() + `</div></div><div class="col-lg-wide-25"><ul class="m-y detailcol"><li><a href="/label/">标签</a></li></ul></div></body></html>`
	var eps strings.Builder
	for _, sid := range []int{1, 2, 3} {
		eps.WriteString(`<div class="stui-pannel stui-pannel-bg clearfix"><div class="stui-pannel-box b playlist mb"><div class="stui-pannel_hd"><h3 class="title">播放线路 `)
		eps.WriteString(string(rune('0' + sid)))
		eps.WriteString(`</h3></div><div class="stui-pannel_bd col-pd clearfix"><ul class="stui-content__playlist clearfix"><li ><a href="/play/55-`)
		eps.WriteString(string(rune('0' + sid)))
		eps.WriteString(`-1.html">01</a></li><li ><a href="/play/55-`)
		eps.WriteString(string(rune('0' + sid)))
		eps.WriteString(`-2.html">02</a></li></ul></div></div></div>`)
	}
	detail := `<html><body class="myui-page"><h1 class="title">某片<span class="score">0.0</span></h1>` + eps.String() + `</body></html>`
	play := `<html><body><script>var maccms={"path":"","mid":"1","url":"www.6789ysw.com"};</script><script type="text/javascript">var player_aaaa={"flag":"play","encrypt":0,"link":"\/play\/55-1-1.html","url":"https:\/\/vv.jisuzyv.com\/play\/abc\/index.m3u8","from":"dplayer"}</script></body></html>`
	samples := []ai.Sample{
		{Label: "首页 https://www.6789ysw.com/", Content: `<html><body><a href="/list/2.html">电视剧</a><a href="/list/1.html">电影</a><a href="/list/2-2.html">下一页</a><form id="search" action="/search/-------------.html"><input name="wd"></form></body></html>`},
		{Label: "分类页 https://www.6789ysw.com/list/2-1.html", Content: catalog},
		{Label: "详情页 https://www.6789ysw.com/vod/55.html", Content: detail},
		{Label: "播放页 https://www.6789ysw.com/play/55-1-1.html", Content: play},
	}
	text := Analyze(samples)
	t.Logf("\n%s", text)
	checks := map[string]string{
		"播放数组建议用 ul 内层容器":  `播放数组 建议 "<ul class=\"stui-content__playlist clearfix\"&&</ul>"`,
		"多线路指引":            "检测到多线路站",
		"必须写线路数组":          "【必须写 线路数组】",
		"player_aaaa 省略指引": "检测到 MacCMS player_aaaa 配置对象：跳转播放链接 建议【整个字段省略不写】",
		"转义直链找到":           "https://vv.jisuzyv.com/play/abc/index.m3u8",
		"数组+链接实测":          "截出指向 /vod/ 的可跳转链接",
		"分集条目原文避开留言":       "第一个分集条目原文",
		"MacCMS 分页形态":      "/list/{cateId}-2.html",
	}
	for name, token := range checks {
		if !strings.Contains(text, token) {
			t.Errorf("%s：指纹未含 %q", name, token)
		}
	}
	// 该站的正确规则（含 线路数组==播放数组）不得被 Check 误报
	good := `{"主页url":"https://www.6789ysw.com/","数组":"<li class=\"col-md-6 col-sm-4 col-xs-3\"&&</li>","二次截取":"<div class=\"col-lg-wide-75 col-xs-1 padding-0\"&&","标题":"title=\"&&\"","链接":"href=\"&&\"","列表图片":"data-original=\"&&\"","播放数组":"<ul class=\"stui-content__playlist clearfix\"&&</ul>","线路数组":"<ul class=\"stui-content__playlist clearfix\"&&</ul>","播放列表":"<li","播放标题":">&&</a>","播放链接":"href=\"&&\""}`
	for _, issue := range Check(good, samples) {
		t.Errorf("多线路好规则被误报: [%s] %s", issue.Field, issue.Problem)
	}
	// AI 坏规则（链接吃路径前缀）必须被"提取合理性"检出
	bad := `{"主页url":"https://www.6789ysw.com/","数组":"<li class=\"col-md-6&&</li>","标题":"title=\"&&\"","链接":"href=\"/vod/&&.html\"","播放数组":"<div class=\"stui-pannel_bd col-pd clearfix\"&&</div>","播放列表":"<li"}`
	issues := Check(bad, samples)
	foundLink := false
	for _, issue := range issues {
		t.Logf("坏规则检出 [%s] %s", issue.Field, issue.Problem)
		if issue.Field == "链接" {
			foundLink = true
		}
	}
	if !foundLink {
		t.Errorf("链接吃路径前缀的坏规则未被检出")
	}
}

// TestAnalyzeTemplateBlock 固化「模板与简写」块：
// MacCMS 站（/list/2-1.html 形态 + stui 皮肤）必须报命中家族与可省略字段清单；
// 自定义皮肤站必须给出"不要简写"警示，避免 AI 盲用模板。
func TestAnalyzeTemplateBlock(t *testing.T) {
	var items strings.Builder
	for i := 1; i <= 10; i++ {
		items.WriteString(`<li><a class="stui-vodlist__thumb" href="/vod/` + string(rune('0'+i)) + `1.html" title="片` + string(rune('0'+i)) + `" data-original="https://img/x.jpg"></a></li>`)
	}
	maccmsSamples := []ai.Sample{
		{Label: "首页 https://a.com/", Content: `<html><body><a href="/list/2-1.html">电视剧</a><a href="/list/1-1.html">电影</a></body></html>`},
		{Label: "分类页 https://a.com/list/2-1.html", Content: `<html><body class="stui-headers"><div class="stui-vodlist__head">` + items.String() + `</div></body></html>`},
	}
	text := Analyze(maccmsSamples)
	t.Logf("\n%s", text)
	for _, token := range []string{
		"命中内置模板家族",
		"可以省略不写",
		"页面皮肤探测：stui",
	} {
		if !strings.Contains(text, token) {
			t.Errorf("MacCMS 站指纹缺少 %q", token)
		}
	}

	custom := []ai.Sample{
		{Label: "首页 https://b.com/", Content: `<html><body><a href="/weird/movie/p2/">电影</a></body></html>`},
		{Label: "分类页 https://b.com/weird/movie/p2/", Content: `<html><body><div class="poster-grid"><span data-id="9"><em>某片</em></span></div><span data-id="8"><em>另片</em></span><span data-id="7"><em>三片</em></span><span data-id="6"><em>四片</em></span><span data-id="5"><em>五片</em></span><span data-id="4"><em>六片</em></span><span data-id="3"><em>七片</em></span></body></html>`},
	}
	customText := Analyze(custom)
	t.Logf("\n%s", customText)
	if !strings.Contains(customText, "未识别到") && !strings.Contains(customText, "未命中内置模板") {
		t.Errorf("自定义皮肤站应警示不要简写，实际指纹：\n%s", customText)
	}
}

// TestCheckAbbreviatedRuleRules 固化两条简写铁律的确定性拦截：
// ① 省 主页url 时 分类url 必须含域名；② 分类url 必须写 {catePg} 分页占位。
func TestCheckAbbreviatedRuleRules(t *testing.T) {
	samples := []ai.Sample{{
		Label:   "首页 https://www.6789ysw.com",
		Content: `<a class="stui-vodlist__thumb" href="/vodshow/1-----------.html" title="片">片</a>`,
	}}
	// 缺 {catePg} 的相对分类url（无主页url）→ 两条都应报。
	bad := `{"分类url":"/index.php/vod/show/id/{cateId}.html","分类":"电影$1#电视剧$2"}`
	issues := Check(bad, samples)
	var categoryIssues int
	for _, issue := range issues {
		if issue.Field == "分类url" {
			categoryIssues++
			t.Logf("检出 [%s] %s", issue.Field, issue.Problem)
		}
	}
	if categoryIssues != 2 {
		t.Errorf("相对路径且缺 {catePg} 应报 2 条分类url问题，实际 %d 条", categoryIssues)
	}
	// 合法简写：绝对地址 + {catePg} → 分类url 不应被报。
	good := `{"分类url":"https://www.6789ysw.com/index.php/vod/show/id/{cateId}/page/{catePg}.html","分类":"电影$1#电视剧$2"}`
	for _, issue := range Check(good, samples) {
		if issue.Field == "分类url" {
			t.Errorf("合法简写规则的分类url被误报: %s", issue.Problem)
		}
	}
	// 写了 主页url 时，相对 分类url 合法，但缺 {catePg} 仍应报。
	withHome := `{"主页url":"https://www.6789ysw.com","分类url":"/index.php/vod/show/id/{cateId}.html","分类":"电影$1"}`
	var missing bool
	for _, issue := range Check(withHome, samples) {
		if issue.Field == "分类url" && strings.Contains(issue.Problem, "{catePg}") {
			missing = true
		}
	}
	if !missing {
		t.Errorf("带主页url但缺 {catePg} 仍应检出分页问题")
	}
}

// TestCheckCategoryURLCateIDRule 固化简写第三条铁律：分类url 必须同时含 {cateId} 与 {catePg}。
// 多分类站缺 {cateId} → 切分类永远打开同一页，必须确定性拦截；单分类站缺 {cateId} 属合法例外。
func TestCheckCategoryURLCateIDRule(t *testing.T) {
	samples := []ai.Sample{{
		Label:   "首页 https://a.com",
		Content: `<a class="stui-vodlist__thumb" href="/vod/1.html" title="片">片</a>`,
	}}
	// 多分类 + 缺 {cateId} + 缺 {catePg} + 相对路径 → 分类url 至少报 {catePg}、{cateId}、域名三条。
	multiBad := `{"分类url":"https://a.com/list/index.html","分类":"电影$1#电视剧$2","主页url":"https://a.com"}`
	var missingCateID, missingCatePg bool
	for _, issue := range Check(multiBad, samples) {
		if issue.Field != "分类url" {
			continue
		}
		if strings.Contains(issue.Problem, "{cateId}") {
			missingCateID = true
		}
		if strings.Contains(issue.Problem, "{catePg}") {
			missingCatePg = true
		}
	}
	if !missingCateID {
		t.Errorf("多分类站缺 {cateId} 应被检出：\n%s", dumpIssues(Check(multiBad, samples)))
	}
	if !missingCatePg {
		t.Errorf("缺 {catePg} 仍应被检出")
	}
	// 两个占位齐备 → 分类url 不应报错。
	good := `{"分类url":"https://a.com/list/{cateId}-{catePg}.html","分类":"电影$1#电视剧$2","主页url":"https://a.com"}`
	for _, issue := range Check(good, samples) {
		if issue.Field == "分类url" {
			t.Errorf("双占位齐备的分类url被误报: %s", issue.Problem)
		}
	}
	// 单分类站缺 {cateId}（ID 不体现在 URL 形态）→ 合法例外，不报。
	single := `{"分类url":"https://a.com/all/{catePg}.html","分类":"短剧$1","主页url":"https://a.com"}`
	for _, issue := range Check(single, samples) {
		if issue.Field == "分类url" && strings.Contains(issue.Problem, "{cateId}") {
			t.Errorf("单分类站缺 {cateId} 不应误报: %s", issue.Problem)
		}
	}
}

// TestAnalyzeMissingCateIDWarn 固化指纹警示：多分类站实测/推断形态缺 {cateId} 时必须警示补占位。
// 场景：分页实测通过的模板只带 {catePg}（如 …/hot/{catePg}.html），而分类检测
// 已实测出多个分类——说明 {cateId} 段没有被形态覆盖，必须提示补上，与 {catePg} 并存。
func TestAnalyzeMissingCateIDWarn(t *testing.T) {
	samples := []ai.Sample{
		{Label: "首页 https://a.com/", Content: `<html><body class="stui-x"><a href="/weird/movie/">电影</a><a href="/weird/tv/">电视剧</a></body></html>`},
		{Label: "分类检测", Content: "分类检测 结论：通过。\n已实测分类串：\"电影$1#电视剧$2\"（每条都真实抓取过且页面可提出 ≥3 个条目，分类字段从这里逐字取）\n分类url 的 {cateId} 位置（实测）：路径段 \"hot\" 中 \"1\" 一段（如 https://a.com/hot/1.html）"},
		{Label: "分页实测", Content: "分页实测 结论：通过\n实测分类url模板：\"https://a.com/hot/{catePg}.html\"\n第2页实测地址：https://a.com/hot/2.html\n与第1页条目重合仅 0%（第1页 6 条 / 第2页 6 条），确认翻页生效。"},
	}
	text := Analyze(samples)
	t.Logf("\n%s", text)
	if !strings.Contains(text, "缺少分类占位 {cateId}") {
		t.Errorf("形态缺 {cateId} 应给出警示，实际指纹：\n%s", text)
	}
}

// TestRouteEvidenceDOMGroupsCatchMissedRoutes 固化 DOM 实测线路证据的价值：
// 详情页两条线路的 ul 容器 class 各不相同（playlist-dp / playlist-ali），
// 字符串投票众数只认一个锚点、独立截取只截出 1 段（containers=1），
// 旧逻辑会漏判单线路 → 漏写 线路数组。DOM 分组必须数出 2 组，
// detailBlock 报【必须写 线路数组】，Check 对没写线路数组的规则报确定性遗漏。
func TestRouteEvidenceDOMGroupsCatchMissedRoutes(t *testing.T) {
	detail := `<html><body class="stui-x"><h1>片名</h1>` +
		`<ul class="stui-content__playlist playlist-dp clearfix itemcol"><li><a href="/play/9-1-1.html">第01集</a></li><li><a href="/play/9-1-2.html">第02集</a></li></ul>` +
		`<ul class="stui-content__playlist playlist-ali clearfix itemcol-3"><li><a href="/play/9-2-1.html">第01集</a></li><li><a href="/play/9-2-2.html">第02集</a></li></ul>` +
		`</body></html>`
	ev := routeEvidenceOf(detail)
	if ev.ulGroups != 2 {
		t.Errorf("DOM 分组应数出 2 条线路，实际 %d（明细 %s）", ev.ulGroups, ev.ulGroupDetail)
	}
	if !ev.multi() {
		t.Errorf("class 逐线路不同的双线路站必须判定为多线路（containers=%d ulGroups=%d）", ev.containers, ev.ulGroups)
	}
	samples := []ai.Sample{
		{Label: "首页 https://a.com/", Content: `<a class="stui-x" href="/vod/9.html">片</a>`},
		{Label: "详情页 https://a.com/vod/9.html", Content: detail},
	}
	text := Analyze(samples)
	t.Logf("\n%s", text)
	if !strings.Contains(text, "【必须写 线路数组】") {
		t.Errorf("指纹应提示必须写 线路数组，实际：\n%s", text)
	}
	// ul 前缀相同（class 首段一致）时 DOM 锚点建议可直接给出
	if ev.containerAnchor != "" && !strings.Contains(text, "DOM 实测并列容器") {
		t.Errorf("锚点可覆盖全部线路组时应输出 DOM 实测容器建议，实际锚点 %q", ev.containerAnchor)
	}
	// 没写 线路数组 的规则必须被 Check 回喂修复
	ruleNoRoute := `{"主页url":"https://a.com","播放数组":"<ul class=\"stui-content__playlist playlist-dp clearfix itemcol\"&&</ul>","播放列表":"<li","播放标题":">&&</a>","播放链接":"href=\"&&\""}`
	issues := Check(ruleNoRoute, samples)
	found := false
	for _, issue := range issues {
		if issue.Field == "播放数组" && strings.Contains(issue.Problem, "线路数组") {
			found = true
			t.Logf("检出 [%s] %s", issue.Field, issue.Problem)
		}
	}
	if !found {
		t.Errorf("漏写 线路数组 的多线路规则应被检出，实际问题：%s", dumpIssues(issues))
	}
}

// TestRouteEvidenceDOMGroupsSingleRouteNoFalsePositive 固化反向不误报：
// 单线路站（一个 ul 一串分集）DOM 分组只算 1 组，不得催 线路数组。
func TestRouteEvidenceDOMGroupsSingleRouteNoFalsePositive(t *testing.T) {
	detail := `<html><body><ul class="playlist"><li><a href="/play/9-1-1.html">第01集</a></li><li><a href="/play/9-1-2.html">第02集</a></li><li><a href="/play/9-1-3.html">第03集</a></li></ul></body></html>`
	ev := routeEvidenceOf(detail)
	if ev.multi() {
		t.Errorf("单线路站不应判多线路：%+v", ev)
	}
}

func dumpIssues(issues []CheckIssue) string {
	var b strings.Builder
	for _, issue := range issues {
		b.WriteString("[" + issue.Field + "] " + issue.Problem + "\n")
	}
	return b.String()
}

// TestAnalyzeTemplateBlockPaging 固化指纹对简写铁律的提示：
// 推断形态缺 {catePg} 时必须警示；相对形态必须给出「省略主页url则写绝对地址」的示例。
func TestAnalyzeTemplateBlockPaging(t *testing.T) {
	// /list/2.html（plain 形态）→ 推断出的模板缺 {catePg}。
	samples := []ai.Sample{
		{Label: "首页 https://a.com/", Content: `<html><body><a href="/list/2.html">电视剧</a><a class="stui-headers-x" href="/list/1.html">电影</a></body></html>`},
		{Label: "分类页 https://a.com/list/2.html", Content: `<html><body class="stui-headers"><div class="stui-vodlist__head"><li><a class="stui-vodlist__thumb" href="/v/1.html" title="片A"></a></li></div></body></html>`},
	}
	text := Analyze(samples)
	t.Logf("\n%s", text)
	if !strings.Contains(text, "必须补上 {catePg}") {
		t.Errorf("缺分页形态应警示 {catePg}，实际指纹：\n%s", text)
	}
	if !strings.Contains(text, "https://a.com/list/") {
		t.Errorf("相对分类形态应给出含域名的绝对地址示例，实际指纹：\n%s", text)
	}
}

// TestAnalyzePagingMeasuredWins 固化「分页实测优先」：样本里有实测通过的
// 「分页实测」结论时，指纹必须采用实测模板（还原 {cateId}/{catePg} 占位）、
// 标注【已实测】，并且不再输出"缺 {catePg}"的静态推断警示。
func TestAnalyzePagingMeasuredWins(t *testing.T) {
	stuiItems := ""
	for i := 1; i <= 6; i++ {
		stuiItems += `<a class="stui-vodlist__thumb" href="/v/` + strconv.Itoa(i) + `.html" title="片` + strconv.Itoa(i) + `"></a>`
	}
	samples := []ai.Sample{
		{Label: "首页 https://a.com/", Content: `<html><body class="stui-x"><a href="/list/2-1.html">电视剧</a></body></html>`},
		{Label: "分类页 https://a.com/list/2-1.html", Content: `<html><body class="stui-headers"><div>` + stuiItems + `</div></body></html>`},
		{Label: "分页实测", Content: "分页实测 结论：通过\n实测分类url模板：\"https://a.com/list/2-{catePg}.html\"\n第2页实测地址：https://a.com/list/2-2.html\n与第1页条目重合仅 0%（第1页 6 条 / 第2页 6 条），确认翻页生效。"},
	}
	text := Analyze(samples)
	t.Logf("\n%s", text)
	if !strings.Contains(text, "分页形态【已实测】") {
		t.Errorf("指纹应采用实测模板并标注已实测，实际：\n%s", text)
	}
	if !strings.Contains(text, `{cateId}-{catePg}`) {
		t.Errorf("实测模板应还原 {cateId} 占位（list/2-… → list/{cateId}-…），实际：\n%s", text)
	}
	// 实测通过时不应再出现静态推断的缺占位警示
	if strings.Contains(text, "⚠ 推断形态缺少分页占位") {
		t.Errorf("实测已通过，不应残留静态推断缺 {catePg} 警示")
	}
}

// TestNormalizePagingTemplate 固化实测模板的占位还原逻辑。
func TestNormalizePagingTemplate(t *testing.T) {
	cases := []struct {
		name     string
		guessed  string
		measured string
		want     string
	}{
		{"对齐还原", "https://a.com/list/{cateId}-{catePg}.html", "https://a.com/list/2-{catePg}.html", "https://a.com/list/{cateId}-{catePg}.html"},
		{"位置推断还原", "", "https://a.com/type/5/{catePg}/", "https://a.com/type/{cateId}/{catePg}/"},
		{"无 cateId 形态保持", "", "https://a.com/index.php/vod/show/page/{catePg}.html", "https://a.com/index.php/vod/show/page/{catePg}.html"},
		{"对齐失败退回位置法", "https://a.com/other/{cateId}.html", "https://a.com/list/3-{catePg}.html", "https://a.com/list/{cateId}-{catePg}.html"},
	}
	for _, c := range cases {
		if got := normalizePagingTemplate(c.guessed, c.measured); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestAnalyzeCategoryMeasuredWins 固化「分类实测优先」：样本里有「分类检测」
// 通过结论时，homeBlock 不再让 AI 照抄未验证的导航候选，templateBlock
// 采用实测 {cateId} 模板并显示分类检测原文。
func TestAnalyzeCategoryMeasuredWins(t *testing.T) {
	samples := []ai.Sample{
		{Label: "首页 https://a.com/", Content: `<html><body class="stui-x"><a href="/list/1.html">电影</a><a href="/list/2.html">电视剧</a><a href="/list/4.html">动漫</a></body></html>`},
		{Label: "分类检测", Content: "分类检测 结论：通过。\n已实测分类串：\"电影$1#电视剧$2\"（每条都真实抓取过且页面可提出 ≥3 个条目，分类字段从这里逐字取）\n实测失败分类：动漫(4)（抓取失败或页面无内容——不要放进分类字段）\n分类url 的 {cateId} 位置（实测）：路径段 \"list\" 中 \"1\" 一段（如 https://a.com/list/1.html）\n实测分类URL模板：\"https://a.com/list/{cateId}.html\""},
	}
	text := Analyze(samples)
	t.Logf("\n%s", text)
	if strings.Contains(text, "导航分类候选（名称$ID，顺序照抄）") {
		t.Errorf("分类实测通过后不应再让 AI 照抄未验证候选，实际：\n%s", text)
	}
	if !strings.Contains(text, "分类字段【已实测】") {
		t.Errorf("应提示改用实测分类串，实际：\n%s", text)
	}
	if !strings.Contains(text, "已实测分类串：\"电影$1#电视剧$2\"") {
		t.Errorf("模板块应原文显示分类检测结论，实际：\n%s", text)
	}
}

// TestTemplateBlockCategoryMergePaging 固化两路实测的合成：分类实测给出
// {cateId} 骨架、分页未实测时，形态 = 实测骨架 + 推断分页尾巴，并明确标注
// {catePg} 未实测。
func TestTemplateBlockCategoryMergePaging(t *testing.T) {
	samples := []ai.Sample{
		{Label: "首页 https://a.com/", Content: `<html><body class="stui-x"><a href="/list/2-1.html">电视剧</a></body></html>`},
		{Label: "分类检测", Content: "分类检测 结论：通过。\n已实测分类串：\"电影$1\"（略）\n实测分类URL模板：\"https://a.com/list/{cateId}.html\""},
	}
	text := Analyze(samples)
	t.Logf("\n%s", text)
	if !strings.Contains(text, `"/?https://a.com/list/{cateId}-{catePg}.html`) && !strings.Contains(text, `https://a.com/list/{cateId}-{catePg}.html`) {
		t.Errorf("应以实测骨架拼上推断分页尾巴，实际：\n%s", text)
	}
	if !strings.Contains(text, "分类url 形态") {
		t.Errorf("分类实测+分页未实测应走 categoryTplUsed 显示分支，实际：\n%s", text)
	}
}

// TestCheckFailedCategoryID 固化自检拦截：规则 分类 里写了实测失败分类的 ID → 报错。
func TestCheckFailedCategoryID(t *testing.T) {
	samples := []ai.Sample{
		{Label: "分类检测", Content: "分类检测 结论：部分通过。\n已实测分类串：\"电影$1\"（略）\n实测失败分类：动漫(4)（抓取失败或页面无内容——不要放进分类字段）\n实测分类URL模板：\"https://a.com/list/{cateId}.html\""},
	}
	ruleText := `{"主页url":"https://a.com","分类":"电影$1#动漫$4","分类url":"https://a.com/list/{cateId}/{catePg}/","数组":"<li>&&</li>"}`
	issues := Check(ruleText, samples)
	t.Logf("%v", issues)
	found := false
	for _, issue := range issues {
		if issue.Field == "分类" && strings.Contains(issue.Problem, "实测失败") {
			found = true
		}
	}
	if !found {
		t.Errorf("实测失败 ID 写进分类应被拦截，实际问题：%v", issues)
	}
	// 用实测通过的 ID 不应误报
	okRule := `{"主页url":"https://a.com","分类":"电影$1","分类url":"https://a.com/list/{cateId}/{catePg}/","数组":"<li>&&</li>"}`
	for _, issue := range Check(okRule, samples) {
		if issue.Field == "分类" {
			t.Errorf("通过的分类 ID 被误报：%v", issue)
		}
	}
}

package xbpq

import (
	"strings"
	"testing"
)

func TestParseRuleAcceptsCommonShapes(t *testing.T) {
	cases := map[string]bool{
		`{"主页url":"https://a.com"}`:            true,
		`{"首页url":"https://a.com"}`:            true,
		`{"请求":"a.com"}`:                       true,
		`[{"主页url":"https://a.com"}]`:          true,
		`{"主页url":"https://a.com",}`:           true,  // 尾部多余逗号
		`{"标题":"x"}`:                           false, // 没有主页字段
		`not json`:                             false,
		`{"主页url":"https://a.com"} extra junk`: false,
	}
	for raw, want := range cases {
		if _, ok := ParseRule(raw); ok != want {
			t.Errorf("ParseRule(%q) = %v, 期望 %v", raw, ok, want)
		}
	}
}

func TestParseRuleLongText(t *testing.T) {
	raw := `{"主页url":"https://a.com"}@long-text:"{\"分类\":\"电影$1\"}"`
	rule, ok := ParseRule(raw)
	if !ok {
		t.Fatal("带 @long-text 的规则应能解析")
	}
	if got := rule.Field("分类"); got != "电影$1" {
		t.Fatalf("未合并 long-text 字段，分类 = %q", got)
	}
}

func TestCutOnceSyntax(t *testing.T) {
	source := `<a href="/vod/1.html" title="剧一"><img data-original="/p/1.jpg"></a>`
	cases := []struct{ pattern, want string }{
		{`href="&&"`, "/vod/1.html"},
		{`title="&&"`, "剧一"},
		{`data-original="&&"`, "/p/1.jpg"},
		{`<a &&</a>`, `href="/vod/1.html" title="剧一"><img data-original="/p/1.jpg">`},
		{`不存在的锚点 &&`, ""},
		{`href="/nope/" || title="&&"`, "剧一"}, // || 备选
	}
	for _, one := range cases {
		if got := CutOnce(source, one.pattern); got != one.want {
			t.Errorf("CutOnce(%q) = %q, 期望 %q", one.pattern, got, one.want)
		}
	}
}

func TestCutOnceModifiers(t *testing.T) {
	source := `<li>苹果</li><li>香蕉</li><li>橙子</li>`
	// CutOnce 单步截取，修饰符不满足就返回空（不往后找下一个同名标签）
	if got := CutOnce(source, `<li>&&</li>[包含:苹果]`); got != "苹果" {
		t.Errorf("[包含] 未生效，得到 %q", got)
	}
	if got := CutOnce(source, `<li>&&</li>[包含:香蕉]`); got != "" {
		t.Errorf("CutOnce 不应跳过后续标签，得到 %q", got)
	}
	if got := CutOnce(source, `<li>&&</li>[不包含:香蕉]`); got != "苹果" {
		t.Errorf("[不包含] 未生效，得到 %q", got)
	}
	if got := CutOnce(source, `<li>&&</li>[替换:苹果>>西瓜]`); got != "西瓜" {
		t.Errorf("[替换] 未生效，得到 %q", got)
	}
	// List 会扫描并跳过不满足的条目
	if items := List(source, `<li>&&</li>[包含:香蕉]`); len(items) != 1 || items[0] != "香蕉" {
		t.Errorf("List + [包含] = %v, 期望 [香蕉]", items)
	}
}

func TestListExtractsAllItems(t *testing.T) {
	source := `<ul><li>A</li><li>B</li><li>C</li></ul>`
	items := List(source, `<li>&&</li>`)
	if len(items) != 3 || items[0] != "A" || items[2] != "C" {
		t.Fatalf("List = %v, 期望 [A B C]", items)
	}
}

func TestCategoriesAcceptsNumericAndSlug(t *testing.T) {
	rule, _ := ParseRule(`{"主页url":"https://a.com","分类":"电影$1#电视剧$2#综艺$3"}`)
	categories := rule.Categories()
	if len(categories) != 3 || categories[0].ID != "1" {
		t.Fatalf("数字分类解析异常: %+v", categories)
	}
	rule2, _ := ParseRule(`{"主页url":"https://a.com","分类":"电视剧$tv#电影$movie#动漫$cartoon"}`)
	categories2 := rule2.Categories()
	if len(categories2) != 3 || categories2[0].ID != "tv" {
		t.Fatalf("slug 分类被过滤掉了: %+v", categories2)
	}
}

func TestCategoriesSubEntries(t *testing.T) {
	rule, _ := ParseRule(`{"主页url":"https://a.com","分类":"电影$1#1--动作片$5#1--喜剧片$6"}`)
	categories := rule.Categories()
	if len(categories) != 3 {
		t.Fatalf("子分类未展开: %+v", categories)
	}
}

func TestRenderURLPlaceholders(t *testing.T) {
	rule, _ := ParseRule(`{"主页url":"https://a.com","分类url":"https://a.com/list/{cateId}-{catePg}.html"}`)
	got := rule.CategoryURL("https://a.com", "2", 3)
	if got != "https://a.com/list/2-3.html" {
		t.Fatalf("CategoryURL = %q", got)
	}
	// 换域名时以站源地址为准重写 host
	if got := rule.CategoryURL("https://mirror.com", "2", 1); got != "https://mirror.com/list/2-1.html" {
		t.Fatalf("镜像域名未重写: %q", got)
	}
}

func TestJoinLinkStyles(t *testing.T) {
	if got := JoinLink("https://a.com/x/", "/vod/1.html"); got != "https://a.com/vod/1.html" {
		t.Fatalf("相对路径补全错误: %q", got)
	}
	if got := JoinLink("https://a.com/x/", `"/vod"+"/1.html"`); got != "https://a.com/vod/1.html" {
		t.Fatalf("多线链接拼接错误: %q", got)
	}
	// 只剥 href= 前缀，不剥引号——引号本该由 && 边界排除（与原核心一致）
	if got := JoinLink("https://a.com/x/", `href=/vod/1.html`); got != "https://a.com/vod/1.html" {
		t.Fatalf("href= 前缀未处理: %q", got)
	}
}

func TestEpisodesSingleRoute(t *testing.T) {
	rule, _ := ParseRule(`{"主页url":"https://a.com","播放数组":"<ul class=\"list\">&&</ul>","播放列表":"#","播放链接":"href=\"&&\"","播放标题":">&&<"}`)
	body := `<ul class="list"><a href="/play/1-1-1.html">第01集</a>#<a href="/play/1-1-2.html">第02集</a></ul>`
	engine := &Engine{Rule: rule, Base: "https://a.com"}
	best, routes := engine.episodes(body, "https://a.com/vod/1.html")
	if len(best) != 2 {
		t.Fatalf("分集数 = %d, 期望 2", len(best))
	}
	if len(routes) != 1 {
		t.Fatalf("线路数 = %d, 期望 1", len(routes))
	}
	if !strings.HasSuffix(best[0].url, "/play/1-1-1.html") {
		t.Fatalf("分集地址未补全: %q", best[0].url)
	}
}

func TestEpisodesMultiRoute(t *testing.T) {
	// 线路数组让 6 个容器都被识别；未写时退回单次截取（只有 1 条线路）。
	body := ""
	for sid := 1; sid <= 6; sid++ {
		body += `<ul class="playlist">`
		for index := 1; index <= 4; index++ {
			body += `<a href="/play/1-` + itoa(sid) + `-` + itoa(index) + `.html">第0` + itoa(index) + `集</a>#`
		}
		body += `</ul>`
	}
	single, _ := ParseRule(`{"主页url":"https://a.com","播放数组":"<ul class=\"playlist\">&&</ul>","播放链接":"href=\"&&\"","播放标题":">&&<"}`)
	if _, routes := (&Engine{Rule: single}).episodes(body, "https://a.com/vod/1.html"); len(routes) != 1 {
		t.Fatalf("未写线路数组时应只有 1 条线路，实际 %d", len(routes))
	}
	multi, _ := ParseRule(`{"主页url":"https://a.com","播放数组":"<ul class=\"playlist\">&&</ul>","线路数组":"<ul class=\"playlist\">&&</ul>","播放链接":"href=\"&&\"","播放标题":">&&<"}`)
	best, routes := (&Engine{Rule: multi}).episodes(body, "https://a.com/vod/1.html")
	if len(routes) != 6 {
		t.Fatalf("写了线路数组应识别 6 条线路，实际 %d", len(routes))
	}
	if len(best) != 4 {
		t.Fatalf("主线路集数 = %d, 期望 4", len(best))
	}
	// 同一集在其它线路的地址应被收集成备选
	alternates := alternateRoutes(routes, 0, best[0], best[0].url, "https://a.com/vod/1.html")
	if len(alternates) != 5 {
		t.Fatalf("备选线路 = %d, 期望 5", len(alternates))
	}
}

func TestEpisodesTitleDollarLink(t *testing.T) {
	rule, _ := ParseRule(`{"主页url":"https://a.com","播放数组":"mac_url='&&'","播放列表":"#"}`)
	body := `mac_url='第01集$https://cdn/1.m3u8#第02集$https://cdn/2.m3u8'`
	best, _ := (&Engine{Rule: rule}).episodes(body, "https://a.com/vod/1.html")
	if len(best) != 2 {
		t.Fatalf("分集数 = %d, 期望 2", len(best))
	}
	if best[0].title != "第01集" || best[0].url != "https://cdn/1.m3u8" {
		t.Fatalf("标题$链接 解析错误: %+v", best[0])
	}
}

func TestSelectorExtract(t *testing.T) {
	html := `<div class="box"><a class="t" href="/vod/9.html">剧九</a></div>`
	if got := SelectorFirstString(html, `p:a.t[href]`); got != "/vod/9.html" {
		t.Fatalf("选择器取属性 = %q", got)
	}
	if got := SelectorFirstString(html, `p:a.t`); got != "剧九" {
		t.Fatalf("选择器取文本 = %q", got)
	}
	if got := SelectorFirstString(html, `p:div.box a.t`); got != "剧九" {
		t.Fatalf("后代选择器 = %q", got)
	}
}

func TestCleanText(t *testing.T) {
	if got := CleanText(`<span>简介：</span>正文&nbsp;内容`); got != "简介：正文 内容" {
		t.Fatalf("CleanText = %q", got)
	}
}

func TestPlayerURLFromPlayerAAAA(t *testing.T) {
	body := `<script>var player_aaaa={"url":"https:\/\/cdn.example.com\/2026\/abc\/index.m3u8","encrypt":0}</script>`
	if got := PlayerURL(body); got != "https://cdn.example.com/2026/abc/index.m3u8" {
		t.Fatalf("PlayerURL = %q", got)
	}
}

func TestPlayerURLFromArtplayer(t *testing.T) {
	body := `<script>new Artplayer({url: 'https://cdn.example.com/x/index.m3u8'})</script>`
	if got := PlayerURL(body); got != "https://cdn.example.com/x/index.m3u8" {
		t.Fatalf("PlayerURL = %q", got)
	}
}

func TestNormalizePlaybackURL(t *testing.T) {
	got := NormalizePlaybackURL(`https:\/\/cdn.com\/a\/index.m3u8`)
	if got != "https://cdn.com/a/index.m3u8" {
		t.Fatalf("转义未还原: %q", got)
	}
}

func TestDecodeBodyGBK(t *testing.T) {
	// GBK 编码的 "电影" 是 0xB5 0xE7 0xD3 0xB0
	raw := "<html><head><meta charset=\"gbk\"></head><body>" + string([]byte{0xB5, 0xE7, 0xD3, 0xB0}) + "</body></html>"
	if got := DecodeBody(raw); !strings.Contains(got, "电影") {
		t.Fatalf("GBK 未解码: %q", got)
	}
}

func itoa(value int) string {
	return string(rune('0' + value))
}

func TestCutWildcardAnchor(t *testing.T) {
	// 起始锚点里的 * 通配任意字符（一个字段仅一个通配符）。
	source := `<video controls="true" src="https://cdn/x.m3u8">`
	if got := CutOnce(source, `<video controls="true*src="&&"`); got != "https://cdn/x.m3u8" {
		t.Fatalf("通配符起始锚点失败: %q", got)
	}
	if got := CutOnce(`<h3 class="t">剧名</h3>`, `<h*>&&</h`); got != "剧名" {
		t.Fatalf("<h*> 通配符失败: %q", got)
	}
}

func TestCutEscape(t *testing.T) {
	// 笔记范例：截取 href="?cat&token=5543tdd57" 里的 token，
	// 转义写法 href="?cat\&&&"（\&& 中的首个 && 被 \ 吃掉一个 &，剩下 & 拼回字面）。
	source := `href="?cat&token=5543tdd57"`
	if got := CutOnce(source, `href="?cat\&&&"`); got != "token=5543tdd57" {
		t.Fatalf("转义 && 截取失败: %q", got)
	}
}

func TestCutPlusConcat(t *testing.T) {
	// 截取段 + 字面段 拼接：把 /vod/123.html 变成 /play/123-1-1.html
	source := `<a href="/vod/123.html">x</a>`
	if got := CutOnce(source, `/play/+href="/vod/&&.html+-1-1.html`); got != "/play/123-1-1.html" {
		t.Fatalf("+ 拼接失败: %q", got)
	}
	// 字面前缀 + 截取
	if got := CutOnce(source, `https://host+href="&&"`); got != "https://host/vod/123.html" {
		t.Fatalf("+ 前缀拼接失败: %q", got)
	}
}

func TestListOrderModifier(t *testing.T) {
	source := `<div class="line">腾腾线路</div><div class="line">自建蓝光</div><div class="line">优优线路</div>`
	items := List(source, `<div class="line">&&</div>[排序:自建蓝光>腾腾>优优]`)
	if len(items) != 3 {
		t.Fatalf("排序模式条目数 = %d", len(items))
	}
	if items[0] != "自建蓝光" || items[1] != "腾腾线路" || items[2] != "优优线路" {
		t.Fatalf("[排序:] 未生效: %v", items)
	}
}

func TestAbbreviatedRuleGetsTemplates(t *testing.T) {
	// 简写规则（笔记范例 + XBPQ.json 61% 实战形态）：只有 分类url+分类，无主页url。
	rule, ok := ParseRule(`{"分类url":"https://www.6789ysw.com/index.php/vod/show/area/{area}/id/{cateId}/page/{catePg}/year/{year}.html","分类":"电影$1#电视剧$2"}`)
	if !ok {
		t.Fatal("简写规则应被识别为 XBPQ 规则")
	}
	if rule.HomeURL() != "https://www.6789ysw.com" {
		t.Fatalf("简写规则主页应从分类url推导: %q", rule.HomeURL())
	}
	if rule.Field("数组") == "" || rule.Field("链接") == "" {
		t.Fatalf("模板未补齐核心字段: 数组=%q 链接=%q", rule.Field("数组"), rule.Field("链接"))
	}
	if rule.Field("播放数组") == "" {
		t.Fatal("模板未补齐 播放数组")
	}
	// 搜索默认值应填好 host
	if !strings.HasPrefix(rule.Field("搜索url"), "https://www.6789ysw.com/") {
		t.Fatalf("搜索url 模板未实例化 host: %q", rule.Field("搜索url"))
	}
	// 已写字段不被覆盖
	rule2, _ := ParseRule(`{"分类url":"https://a.com/vodshow/{cateId}---.html","数组":"自定义&&边界","主页url":"https://a.com"}`)
	if rule2.Field("数组") != "自定义&&边界" {
		t.Fatalf("模板覆盖了用户已写字段: %q", rule2.Field("数组"))
	}
	// 非模板形态不乱补
	rule3, _ := ParseRule(`{"主页url":"https://a.com","分类url":"https://a.com/weird/{cateId}.html"}`)
	if rule3.declaresField("数组") {
		t.Fatal("未知形态不应强补 数组")
	}
}

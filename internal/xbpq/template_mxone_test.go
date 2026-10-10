package xbpq

import (
	"strings"
	"testing"
)

// mxone 皮肤（苹果CMS新版，hanjuds.com 式）家族回归测试：
// 分类url 是 /{目录}/index-{cateId}-{catePg}.html 形态时命中 MacCMS新版mxone，
// 列表/播放锚点用 div.module-item 与 tab-list 容器（不是 stui/myui 的 <ul>），
// 且 搜索url 绝不能由模板瞎补（mxone 搜索 GET 路径与 MacCMS 完全不同）。
func TestTemplateMxoneFamily(t *testing.T) {
	name, fields := MatchTemplate("https://www.hanjuds.com/80s/index-{cateId}-{catePg}.html")
	if !strings.Contains(name, "MacCMS新版mxone") {
		t.Fatalf("index-{cateId} 形态应命中 mxone 家族，实际命中 %q", name)
	}
	for _, key := range []string{"数组", "播放数组", "线路数组", "搜索数组", "搜索标题"} {
		if fields[key] == "" {
			t.Fatalf("mxone 家族缺字段 %q", key)
		}
	}
	if !strings.Contains(fields["数组"], "module-item") {
		t.Fatalf("mxone 数组应锚 module-item，实际 %q", fields["数组"])
	}
	if !strings.Contains(fields["播放数组"], "tab-list") || !strings.Contains(fields["线路数组"], "tab-list") {
		t.Fatalf("mxone 播放/线路数组应同锚 tab-list 容器: 播放=%q 线路=%q", fields["播放数组"], fields["线路数组"])
	}
	if fields["播放列表"] != "</a>" {
		t.Fatalf("mxone 分集是 <a> 连排，播放列表 分隔符应为 </a>，实际 %q", fields["播放列表"])
	}
	// mxone 家族的 fields 里不能有 搜索url（这类站 GET 路径特殊，模板给了必 404）。
	if _, exists := fields["搜索url"]; exists {
		t.Fatalf("mxone 家族不应给 搜索url 默认: %q", fields["搜索url"])
	}

	// 简写规则：只写 分类/分类url/搜索url/播放列表，其余靠家族补齐；
	// declared 只算显式写的（搜索url 用户写的），自检逐字校验才不会误报。
	rule, ok := ParseRule(`{"分类url":"https://www.hanjuds.com/80s/index-{cateId}-{catePg}.html","分类":"电影$1","搜索url":"https://www.hanjuds.com/80s/list--------------.html?wd={wd}","播放列表":"</a>"}`)
	if !ok {
		t.Fatal("mxone 简写规则应被识别")
	}
	for _, key := range []string{"数组", "标题", "链接", "播放数组", "线路数组", "搜索数组"} {
		if rule.Field(key) == "" {
			t.Fatalf("模板未补齐 %q", key)
		}
	}
	if !strings.Contains(rule.Field("线路数组"), "tab-list") {
		t.Fatalf("线路数组 应来自 mxone 家族: %q", rule.Field("线路数组"))
	}
}

// mxone 播放页取流：真实 m3u8 是 base64 塞在 iframe src 的 url= 参数里。
// PlayerURL 必须解码；解码后逗号分隔多源取第一个 http 直链。
func TestIframeBase64Playback(t *testing.T) {
	body := `<div class="player"><iframe id="viframe" class="viframe" height="100%" width="100%" src="https://v3.158868.com/laocz/dp/url?url=aHR0cHM6Ly9zdmlwLmZlaWZlaS1wbGF5LmNvbS8yMDI2MDYxOS80NTUzNF9kZTZiNDBjNy9pbmRleC5tM3U4LGh0dHBzOi8vc3ZpcC5mZWlmZWktcGxheS5jb20vc2hhcmUvZGU2YjQwYzcxNTc3ZDQzZDAwMDFjYTJiMDk1YmFlMTg" frameborder="no"></iframe></div>`
	got := PlayerURL(body)
	want := "https://svip.feifei-play.com/20260619/45534_de6b40c7/index.m3u8"
	if got != want {
		t.Fatalf("iframe base64 解码错误:\n got %q\nwant %q", got, want)
	}
	if !LooksLikeMedia(got) {
		t.Fatal("解码结果应识别为媒体直链")
	}
	// 没有 base64 的普通页面不受影响
	if IframeBase64URL(`<iframe src="https://example.com/player.html">`) != "" {
		t.Fatal("无 url= 参数时不应解码")
	}
}

// mxone 分集切分回归：tab-list 容器段以 </a> 分隔、<span> 里是集名。
func TestEpisodesMxoneStyle(t *testing.T) {
	routeBlock := func(sid int) string {
		var b strings.Builder
		b.WriteString(`<div class="module-list module-player-list tab-list sort-list module-vod-list" id="glist-1">`)
		b.WriteString(`<div class="module-blocklist"><div class="scroll-content scroll-y scroll-play">`)
		for i := 1; i <= 8; i++ {
			b.WriteString(`<a href="/80s/play-156040-` + itoa(sid) + `-` + itoa(i) + `.html" title="播放第` + itoa(i) + `集"><span>第0` + itoa(i) + `集</span></a>`)
		}
		b.WriteString(`</div></div></div>`)
		return b.String()
	}
	body := routeBlock(1) + `<div class="netdisk"><a href="https://pan.quark.cn/s/x">夸克</a></div>` + routeBlock(0)
	rule, _ := ParseRule(`{"主页url":"https://www.hanjuds.com","播放数组":"class=\"module-list module-player-list tab-list sort-list&&</div>","线路数组":"class=\"module-list module-player-list tab-list sort-list&&</div>","播放列表":"</a>","播放标题":"<span>&&</span>","播放链接":"href=\"&&\""}`)
	best, routes := (&Engine{Rule: rule}).episodes(body, "https://www.hanjuds.com/80s/156040.html")
	if len(routes) != 2 {
		t.Fatalf("两条线路容器应识别 2 条线路，实际 %d", len(routes))
	}
	if len(best) != 8 {
		t.Fatalf("主线路应 8 集，实际 %d", len(best))
	}
	if best[0].title != "第01集" || !strings.Contains(best[0].url, "/80s/play-156040-1-1.html") {
		t.Fatalf("首集解析错误: %+v", best[0])
	}
}

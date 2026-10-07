package xbpq

import (
	"strings"
	"testing"
)

// 聚类统计里真实规则最高频的 跳转播放链接 形态（15 处），
// 必须能截出值并经 NormalizePlaybackURL 还原成可播直链。
func TestTopFrequencyJumpPatterns(t *testing.T) {
	page := `<html><script>var player_aaaa={"flag":"play","encrypt":0,"url":"https:\/\/cdn.example.com\/play\/1\/index.m3u8","urlnext":""};</script></html>`
	cut := CutOnce(page, `var player_*"url":"&&"`)
	if cut == "" {
		t.Fatal("通配符形态 var player_* 截不出值")
	}
	normalized := NormalizePlaybackURL(cut)
	if normalized != "https://cdn.example.com/play/1/index.m3u8" {
		t.Fatalf("还原 \\/ 转义失败: %q", normalized)
	}
	if !IsHTTPMediaURL(normalized) || !LooksLikeMedia(normalized) {
		t.Fatalf("规整后不是可播直链: %q", normalized)
	}
	// 高频形态二：<video controls="true*src="&&"（笔记原文范例）
	videoPage := `<video controls="true" preload="preload" src="https://v.example.com/a.mp4"></video>`
	if got := CutOnce(videoPage, `controls="true*src="&&"`); got != "https://v.example.com/a.mp4" {
		t.Fatalf("controls 通配形态 = %q", got)
	}
	// 高频形态三：链接 [替换:voddetail>>vodplay#.html>>-1-1.html]（6 处）
	detailLink := `<a href="/voddetail/8080.html" title="x">`
	if got := CutOnce(detailLink, `href="&&"[替换:voddetail>>vodplay#.html>>-1-1.html]`); got != "/vodplay/8080-1-1.html" {
		t.Fatalf("替换形态 = %q", got)
	}
}

// 线路数组 [排序:]（3 处）+ 副标题「字面+截取」拼接（7+5 处）组合形态。
func TestTopFrequencyRouteAndSubTitle(t *testing.T) {
	page := `<div class="item line">线路三 http://a/3</div><div class="item line">线路一 http://a/1</div><div class="item line">线路二 http://a/2</div>`
	rows := List(page, `<div class="item line">&&</div>[排序:线路一>线路二>线路三]`)
	if len(rows) != 3 {
		t.Fatalf("应截出 3 条, 实际 %d", len(rows))
	}
	if !strings.HasPrefix(rows[0], "线路一") {
		t.Fatalf("排序后首条 = %q", rows[0])
	}
	sub := `<a href="/x" title="剧"><span class="pic-text text-right">更新至8集</span></a>`
	// 实战最高频写法：单侧 text-right">&& 按笔记取到末尾，拼接结果带条目尾部；
	// 推荐双锚点 text-right">&&</span 形态（截值干净）。
	if got := CutOnce(sub, `菜鸟专属+text-right">&&</span`); got != "菜鸟专属更新至8集" {
		t.Fatalf("副标题 字面+截取 = %q", got)
	}
}

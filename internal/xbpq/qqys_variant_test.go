package xbpq

import (
	"strings"
	"testing"
)

// 555yx/qqys 式 module-poster 变体的引擎侧回归（离线，不联网）：
// ① player_data encrypt=2「urlencode 套 base64」双重解码；
// ② 紧贴详情形态 /voddetail12543.html 的数字 ID 提取（分页两页比对依赖它）。

// 播放页 player_data：url 是 base64(urlencode(真实 m3u8))，外层每个 % 编码成 %25。
const qqysPlaySample = `var player_data={"flag":"play","encrypt":2,"trysee":1,"points":0,"link":"\/vodplay\/12543-1-1.html","link_next":"","link_pre":"","url":"JTY4JTc0JTc0JTcwJTczJTNBJTJGJTJGJTc2JTJFJTZDJTY2JTc0JTY4JTY5JTcyJTc0JTc5JTc0JTc3JTZGJTJFJTYzJTZGJTZEJTJGJTMyJTMwJTMyJTM2JTMwJTM3JTMyJTMxJTJGJTM4JTM3JTM5JTM0JTVGJTY2JTY1JTM0JTM5JTY2JTYyJTY2JTY0JTJGJTY5JTZFJTY0JTY1JTc4JTJFJTZEJTMzJTc1JTM4","url_next":"","from":"lzm3u8","server":"no","note":"","sid":1,"nid":1}`

func TestPlayerDataEncrypt2Glued(t *testing.T) {
	fields := PlayerFields(qqysPlaySample)
	if fields["encrypt"] != "2" {
		t.Fatalf("player_data 裸数字标记 encrypt 应被解析，实际 %q（全部字段：%v）", fields["encrypt"], fields)
	}
	if fields["sid"] != "1" {
		t.Fatalf("裸数字 sid 应解析为 1，实际 %q", fields["sid"])
	}
	direct := PlayerURL(qqysPlaySample)
	if !strings.HasPrefix(direct, "https://") || !strings.Contains(direct, "/index.m3u8") {
		t.Fatalf("encrypt=2 双重编码应解出 m3u8 直链，实际 %q", direct)
	}
	if !LooksLikeMedia(direct) {
		t.Fatalf("解码结果应像媒体直链: %q", direct)
	}
}

func TestSourceIDGluedDetail(t *testing.T) {
	cases := map[string]string{
		"https://www.qqys01.com/voddetail12543.html": "12543",
		"https://www.qqys01.com/voddetail32.html":    "32",
		"https://www.qqys01.com/detail88.html":       "88",
	}
	for link, want := range cases {
		if got := SourceIDFromURL(link); got != want {
			t.Fatalf("%s 应提 ID %q，实际 %q", link, want, got)
		}
	}
	// 播放页紧贴形态不应被当详情 ID 误抢（vodplay 带连字符分段，非纯数字段）。
	if got := SourceIDFromURL("https://www.qqys01.com/vodplay/12543-1-1.html"); got != "" {
		t.Fatalf("vodplay 路径不应提成详情 ID，实际 %q", got)
	}
}

// 数组锚对 underscore 分类页（主列表+推荐位混排）应截得出、字段齐全。
func TestPosterCatalogGluedHrefFields(t *testing.T) {
	sample := `<div class="module-items module-poster-items-base ">` +
		`<a href="/voddetail12543.html" title="星球大战" class="module-poster-item module-item"><div class="module-item-cover"><div class="module-item-note">HD中字</div><div class="module-item-pic"><img data-original="https://img.a/1.jpg" alt="星球大战"></div></div></a>` +
		`<a href="/voddetail11738.html" title="凡人修仙传" class="module-poster-item module-item"><div class="module-item-cover"><div class="module-item-note">第383集</div><div class="module-item-pic"><img data-original="https://img.b/2.jpg" alt="凡人修仙传"></div></div></a>` +
		`<a href="/voddetail68214.html" title="法医秦明" class="module-poster-item module-item"><div class="module-item-cover"><div class="module-item-note">更新至04集</div><div class="module-item-pic"><img data-original="https://img.c/3.jpg" alt="法医秦明"></div></div></a>` +
		`</div>`
	items := List(sample, `<a &&</a>[包含:module-poster-item module-item]`)
	if len(items) != 3 {
		t.Fatalf("应截 3 段，实际 %d", len(items))
	}
	for i, it := range items {
		link := CutOnce(it, `href="&&"`)
		title := CutOnce(it, `title="&&"`)
		pic := CutOnce(it, `data-original="&&"`)
		note := CutOnce(it, `class="module-item-note">&&</div>`)
		if !strings.HasPrefix(link, "/voddetail") || title == "" || pic == "" || note == "" {
			t.Fatalf("条目 %d 字段缺失: 链接=%q 标题=%q 图片=%q 副标题=%q", i, link, title, pic, note)
		}
		// 紧贴详情链接必须能提出数字 ID（分页比对基石）。
		if SourceIDFromURL("https://x" + link) == "" {
			t.Fatalf("条目 %d 链接提不出 ID: %q", i, link)
		}
	}
}

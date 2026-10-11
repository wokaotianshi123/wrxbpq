package xbpq

import (
	"strings"
	"testing"
)

// mxone poster-items 变体（555yx.vip 式，/vodshow/{id}--------{pg}---.html dash 分类）：
// 命中 MacCMS 家族（非 mxone index- 家族），默认链必须能截出该变体的列表/播放/搜索结构。
// 关键：列表条目 <a class="module-poster-item module-item"> 的 href/title 在 class 之前，
// 旧候选 module-poster-item&&</a> 起始锚吃掉 href → 链接截不出、整页只剩 1 条；
// 修复锚 <a &&</a>[包含:module-poster-item module-item] 保留 href 在段内。

func TestPosterItemsFamilyHit(t *testing.T) {
	name, fields := MatchTemplate("https://555yx.vip/vodshow/{cateId}--------{catePg}---.html")
	if !strings.Contains(name, "MacCMS") {
		t.Fatalf("dash 分类形态应命中 MacCMS 家族，实际 %q", name)
	}
	if strings.Contains(name, "mxone") {
		t.Fatalf("dash 形态不应命中 mxone index- 家族: %q", name)
	}
	for _, key := range []string{"数组", "播放数组", "线路数组", "搜索数组", "搜索标题"} {
		if fields[key] == "" {
			t.Fatalf("MacCMS 家族缺字段 %q", key)
		}
	}
	if !strings.Contains(fields["数组"], "[包含:module-poster-item module-item]") {
		t.Fatalf("MacCMS 数组应含 poster-items 变体候选: %q", fields["数组"])
	}
	if !strings.Contains(fields["播放数组"], "module-play-list-content") ||
		!strings.Contains(fields["线路数组"], "module-play-list-content") {
		t.Fatalf("MacCMS 播放/线路数组应含 module-play-list-content 候选: 播放=%q 线路=%q", fields["播放数组"], fields["线路数组"])
	}
	if !strings.Contains(fields["搜索数组"], "module-card-item") {
		t.Fatalf("MacCMS 搜索数组应含 module-card-item 候选: %q", fields["搜索数组"])
	}
}

// 列表条目实测（内嵌 555yx 电影分类页真实片段）：数组候选截 3 条、标题/链接/图片齐全。
func TestPosterItemsCatalogCut(t *testing.T) {
	sample := `<div class="module-items module-poster-items-base">
<a href="/voddetail/452795.html" title="小士兵" class="module-poster-item module-item"><div class="module-item-cover"><div class="module-item-note">HD</div><div class="module-item-pic"><img class="lazy lazyload" data-original="https://img.a/1.jpg" alt="小士兵"></div></div><div class="module-poster-item-info"><div class="module-poster-item-title"><span>小士兵</span></div></div></a>
<a href="/voddetail/792246.html" title="末日地堡第三季" class="module-poster-item module-item"><div class="module-item-cover"><div class="module-item-note">第10集完结</div><div class="module-item-pic"><img class="lazy lazyload" data-original="https://img.b/2.webp" alt="末日地堡第三季"></div></div><div class="module-poster-item-info"><div class="module-poster-item-title"><span>末日地堡第三季</span></div></div></a>
<a href="/voddetail/519086.html" title="隐藏的面孔" class="module-poster-item module-item"><div class="module-item-cover"><div class="module-item-note">HD中字</div><div class="module-item-pic"><img class="lazy lazyload" data-original="https://img.c/3.jpg" alt="隐藏的面孔"></div></div><div class="module-poster-item-info"><div class="module-poster-item-title"><span>隐藏的面孔</span></div></div></a>
</div>`
	arr := `<a &&</a>[包含:module-poster-item module-item]`
	items := List(sample, arr)
	if len(items) != 3 {
		t.Fatalf("数组应截出 3 段，实际 %d", len(items))
	}
	for i, it := range items {
		title := CutOnce(it, `title="&&"`)
		link := CutOnce(it, `href="&&"`)
		pic := CutOnce(it, `data-original="&&"`)
		note := CutOnce(it, `class="module-item-note">&&</div>`)
		if title == "" || !strings.HasPrefix(link, "/voddetail/") || pic == "" || note == "" {
			t.Fatalf("条目 %d 字段缺失: 标题=%q 链接=%q 图片=%q 副标题=%q", i, title, link, pic, note)
		}
	}
	// 对照旧候选的教训：容器 class module-poster-items-base 含子串 module-poster-item，
	// 起始锚落在容器上、首段圈住第 1 条，其后条目截不全 —— 可用条目数少于真实条目数。
	old := `module-poster-item&&</a>`
	oldItems := List(sample, old)
	seen := map[string]bool{}
	for _, it := range oldItems {
		if link := CutOnce(it, `href="&&"`); strings.HasPrefix(link, "/voddetail/") {
			seen[link] = true
		}
	}
	if len(oldItems) == 3 && len(seen) == 3 {
		t.Fatalf("旧候选若能完整截出 3 条，说明本用例失去回归意义: 段=%d 链接=%d", len(oldItems), len(seen))
	}
}

// 多线路实测（555yx 剧集页真实片段）：线路数组 同锚切 2 条线路，分集数正确。
func TestPosterItemsEpisodesCut(t *testing.T) {
	sample := `<div class="module-list sort-list tab-list his-tab-list" id="panel1">
<div class="module-play-list"><div class="module-play-list-content module-play-list-base">
<a class="module-play-list-link" href="/vodplay/790001-1-1.html" title="播放问心2第1集"><span>第1集</span></a>
<a class="module-play-list-link" href="/vodplay/790001-1-2.html" title="播放问心2第2集"><span>第2集</span></a>
<a class="module-play-list-link" href="/vodplay/790001-1-3.html" title="播放问心2第3集"><span>第3集</span></a></div></div>
<div class="module-play-list"><div class="module-play-list-content module-play-list-larger">
<a class="module-play-list-link" href="/vodplay/790001-2-1.html" title="播放问心2第1集"><span>第1集</span></a>
<a class="module-play-list-link" href="/vodplay/790001-2-2.html" title="播放问心2第2集"><span>第2集</span></a></div></div>
</div>`
	rule, ok := ParseRule(`{"主页url":"https://555yx.vip","播放数组":"class=\"module-play-list-content&&</div>","线路数组":"class=\"module-play-list-content&&</div>","播放列表":"</a>","播放标题":"<span>&&</span>","播放链接":"href=\"&&\""}`)
	if !ok {
		t.Fatal("poster 线路规则应被识别")
	}
	engine := &Engine{Rule: rule}
	eps, routes := engine.episodes(sample, "https://555yx.vip/voddetail/790001.html")
	if len(routes) != 2 {
		t.Fatalf("应切出 2 条线路，实际 %d", len(routes))
	}
	if len(eps) != 3 || len(routes[0]) != 3 || len(routes[1]) != 2 {
		t.Fatalf("分集数不对: 主线路=%d 线路1=%d 线路2=%d", len(eps), len(routes[0]), len(routes[1]))
	}
	if eps[0].title != "第1集" || !strings.Contains(eps[0].url, "/vodplay/790001-1-1.html") {
		t.Fatalf("首集不对: %q %q", eps[0].title, eps[0].url)
	}
}

// 搜索卡片实测（555yx 搜索页真实片段）：module-card-item 卡片截 2 条、strong 标题可用。
func TestPosterItemsSearchCut(t *testing.T) {
	sample := `<div class="module-card-item module-item"><div class="module-card-item-class">连续剧</div>
<a href="/voddetail/486367.html" class="module-card-item-poster"><div class="module-item-cover"><div class="module-item-note">已完结</div>
<div class="module-item-pic"><img data-original="https://img.s/9.jpg" alt="非常人贩<em>电视剧</em>版第一季"></div></div></a>
<div class="module-card-item-info"><div class="module-card-item-title"><a href="/voddetail/486367.html"><strong>非常人贩<em>电视剧</em>版第一季</strong></a></div></div></div>
<div class="module-card-item module-item"><div class="module-card-item-class">综艺</div>
<a href="/voddetail/504036.html" class="module-card-item-poster"><div class="module-item-cover"><div class="module-item-pic"><img data-original="https://img.t/8.jpg" alt="吐槽大会"></div></div></a>
<div class="module-card-item-info"><div class="module-card-item-title"><a href="/voddetail/504036.html"><strong>吐槽大会</strong></a></div></div></div>`
	arr := `<div class="module-card-item module-item&&<div class="module-card-item module-item`
	items := List(sample, arr)
	// 引擎以「下一卡片锚」为段界，末张卡片无后续锚必然被丢：n 卡片 → n-1 段
	//（在线实测 18 卡片得 17 条与此一致）。
	if len(items) != 1 {
		t.Fatalf("搜索数组应截 1 段（2 卡片丢末段），实际 %d", len(items))
	}
	for i, it := range items {
		title := CutOnce(it, `<strong>&&</strong>`)
		link := CutOnce(it, `href="/voddetail/&&"`)
		pic := CutOnce(it, `data-original="&&"`)
		if title == "" || link == "" || pic == "" {
			t.Fatalf("搜索条目 %d 缺字段: 标题=%q 链接=%q 图片=%q", i, title, link, pic)
		}
	}
}

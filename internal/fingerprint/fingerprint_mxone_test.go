package fingerprint

import (
	"strings"
	"testing"

	"github.com/wrxbpq/wrxbpq/internal/ai"
)

// mxone 皮肤（hanjuds.com 式）三块回归：目录不锚 <li>、详情不被相关推荐带偏、
// 播放页认 iframe+base64。用真实页面关键片段（去冗）构造，不联网。
const mxoneCatalogSample = `<div class="module-items module-items-vod"><div class="module-item"><div class="module-item-cover"><div class="module-item-pic"><a href="/80s/100709.html" title="绑架游戏" ><i class="icon-play"></i></a><img class="lazy" data-src="https://img.bshyw.com/a.jpg"></div><div class="module-item-content"><div class="module-item-style video-name"><a href="/80s/play-100709-1-1.html" title="x在线观看">x</a></div></div></div><div class="module-item-titlebox"><a href="/80s/100709.html" class="module-item-title" title="绑架游戏">绑架游戏</a></div><div class="module-item-text"><span class="vod_score">9.3</span><span>2013</span></div></div>` +
	`<div class="module-item"><div class="module-item-cover"><div class="module-item-pic"><a href="/80s/155618.html" title="惩罚者" ><i class="icon-play"></i></a><img data-src="https://img.bshyw.com/b.jpg"></div><div class="module-item-content"><div class="module-item-style video-name"><a href="/80s/play-155618-1-1.html">y</a></div></div></div><div class="module-item-titlebox"><a href="/80s/155618.html" title="惩罚者">惩罚者</a></div><div class="module-item-text">1992</div></div>` +
	`<div class="module-item"><div class="module-item-cover"><div class="module-item-pic"><a href="/80s/156943.html" title="耳语者" ><i class="icon-play"></i></a><img data-src="https://img.bshyw.com/c.jpg"></div><div class="module-item-content"><div class="module-item-style video-name"><a href="/80s/play-156943-1-1.html">z</a></div></div></div><div class="module-item-titlebox"><a href="/80s/156943.html" title="耳语者">耳语者</a></div><div class="module-item-text">2026</div></div></div>`

const mxoneDetailSample = `<ul class="video-info-list"><li><a href="/80s/play-111-1-1.html">相关推荐A</a></li><li><a href="/80s/play-222-1-1.html">相关推荐B</a></li></ul>` +
	`<div class="module-tab module-player-tab"><div class="module-tab-items"><div class="module-tab-item tab-item" data-dropdown-value="线路F"><span>线路F</span><small>8</small></div><div class="module-tab-item tab-item" data-dropdown-value="vodpan"><span>在线云盘</span><small>5</small></div><div class="module-tab-item tab-item" data-dropdown-value="7-1"><span>线路L</span><small>8</small></div></div></div>` +
	`<div class="module-list module-player-list tab-list sort-list module-vod-list" id="glist-1"><div class="module-blocklist"><div class="scroll-content scroll-y scroll-play">` +
	`<a href="/80s/play-156040-1-1.html" title="播放谜探休格 第二季第01集"><span>第01集</span></a><a href="/80s/play-156040-1-2.html" title="播放第02集"><span>第02集</span></a><a href="/80s/play-156040-1-3.html" title="播放第03集"><span>第03集</span></a>` +
	`</div></div></div>` +
	`<div class="module-list module-player-list tab-list sort-list module-vod-list" id="glist-vodpan"><div class="module-blocklist"><div class="scroll-content scroll-y scroll-play">` +
	`<a class="netdisk-item" href="https://pan.quark.cn/s/abc" target="_blank"><span class="netdisk-name">夸克网盘</span><span class="netdisk-url">https://pan.quark.cn/s/abc</span></a>` +
	`</div></div></div>` +
	`<div class="module-list module-player-list tab-list sort-list module-vod-list" id="glist-2"><div class="module-blocklist"><div class="scroll-content scroll-y scroll-play">` +
	`<a href="/80s/play-156040-0-1.html" title="播放第01集"><span>第01集</span></a><a href="/80s/play-156040-0-2.html" title="播放第02集"><span>第02集</span></a><a href="/80s/play-156040-0-3.html" title="播放第03集"><span>第03集</span></a>` +
	`</div></div></div>`

const mxonePlaySample = `<script>var maccms={"path":"","mid":"1","url":"www.hanjuds.com"};</script>` +
	`<iframe id="viframe" class="viframe" src="https://v3.158868.com/laocz/dp/url?url=aHR0cHM6Ly9zdmlwLmZlaWZlaS1wbGF5LmNvbS8yMDI2MDYxOS80NTUzNF9kZTZiNDBjNy9pbmRleC5tM3U4LGh0dHBzOi8vc3ZpcC5mZWlmZWktcGxheS5jb20vc2hhcmUvZGU2YjQwYzcxNTc3ZDQzZDAwMDFjYTJiMDk1YmFlMTg"></iframe>`

func TestMxoneCatalogBlock(t *testing.T) {
	block := catalogBlock(mxoneCatalogSample + mxoneCatalogSample)
	if !strings.Contains(block, "mxone") {
		t.Fatalf("目录块应命中 mxone 分支，实际：\n%s", block)
	}
	if strings.Contains(block, "条目边界锚点：\"<li>\"") {
		t.Fatal("mxone 目录不应推荐 <li> 作为条目边界")
	}
}

func TestMxoneDetailBlock(t *testing.T) {
	block := detailBlock(mxoneDetailSample)
	if !strings.Contains(block, "tab-list sort-list&&") {
		t.Fatalf("详情块应推荐 tab-list 容器为 播放数组，实际：\n%s", block)
	}
	if !strings.Contains(block, "线路数组 与 播放数组 【同锚点】") {
		t.Fatalf("详情块应说明 线路数组 与 播放数组 同锚点，实际：\n%s", block)
	}
	if strings.Contains(block, `video-info-list`) {
		t.Fatal("详情块不应把相关推荐 <ul class=video-info-list> 误当选票容器")
	}
	if !strings.Contains(block, "3 条线路容器") {
		t.Fatalf("多线路计数应为 3，实际：\n%s", block)
	}
}

func TestMxonePlayBlock(t *testing.T) {
	block := playBlock(mxonePlaySample)
	if !strings.Contains(block, "iframe+base64") {
		t.Fatalf("播放块应识别 iframe+base64 取流，实际：\n%s", block)
	}
	if !strings.Contains(block, "省略不写") {
		t.Fatalf("播放块应建议 跳转播放链接 省略，实际：\n%s", block)
	}
	if !strings.Contains(block, "feifei-play.com/20260619/45534_de6b40c7/index.m3u8") {
		t.Fatalf("播放块应展示解码后的直链，实际：\n%s", block)
	}
}

func TestMxoneTemplateBlockFamily(t *testing.T) {
	samples := []ai.Sample{
		{Label: "首页 https://www.hanjuds.com", Content: mxoneCatalogSample},
		{Label: "分类页 https://www.hanjuds.com/80s/index-1.html", Content: mxoneCatalogSample},
		{Label: "详情页 https://www.hanjuds.com/80s/156040.html", Content: mxoneDetailSample},
		{Label: "播放页 https://www.hanjuds.com/80s/play-156040-1-1.html", Content: mxonePlaySample},
		{Label: "分页实测", Content: "分页实测 结论：通过\n实测分类url模板：\"https://www.hanjuds.com/80s/index-{cateId}-{catePg}.html\"\n第2页实测地址：https://www.hanjuds.com/80s/index-1-2.html\n与第1页条目重合仅 35%，确认翻页生效。"},
		{Label: "分类检测", Content: "分类检测 结论：部分通过。\n已实测分类串：\"成全电影$1#成全剧集$2\"\n分类url 的 {cateId} 位置（实测）：路径段 \"index-1.html\" 中 \"1\" 一段\n实测分类URL模板：\"https://www.hanjuds.com/80s/index-{cateId}.html\""},
	}
	out := Analyze(samples)
	if !strings.Contains(out, "命中内置模板家族：MacCMS新版mxone") {
		t.Fatalf("指纹应命中 mxone 家族，实际：\n%s", out)
	}
	// 搜索url 模板没给 → 不能进「可以省略不写」清单，且必须出现在"仍【必须写】"提示里。
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "可以省略不写") && strings.Contains(line, "搜索url") {
			t.Fatalf("搜索url 不应被列进可省略清单：%s", line)
		}
	}
}

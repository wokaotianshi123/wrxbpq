package fingerprint

import (
	"strings"
	"testing"
)

// mxone poster-items 变体（555yx.vip 式）两块回归：目录不被 <li> 通用边界带偏、
// 详情不被「我的观影记录」drop-content-items 带偏。真实页面关键片段（去冗），不联网。

const posterCatalogEntryFmt = `<a href="/voddetail/%d.html" title="片%d" class="module-poster-item module-item"><div class="module-item-cover"><div class="module-item-note">HD</div><div class="module-item-pic"><img class="lazy lazyload" data-original="https://img.a/%d.jpg" alt="片%d"></div></div><div class="module-poster-item-info"><div class="module-poster-item-title"><span>片%d</span></div></div></a>`

func posterCatalogSample(entries int) string {
	var sb strings.Builder
	sb.WriteString(`<div class="module-items module-poster-items-base">`)
	nav := `<ul class="module-page-nav"><li><a href="/vodtype/1.html">排行</a></li><li><a href="/vodtype/2.html">热门</a></li></ul>`
	sb.WriteString(nav)
	for i := 1; i <= entries; i++ {
		sb.WriteString(strings.Replace(strings.Replace(strings.Replace(posterCatalogEntryFmt, "%d", itoa(452700+i), 1), "%d", itoa(i), 1), "%d", itoa(i), 1))
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

func TestPosterCatalogBlock(t *testing.T) {
	block := catalogBlock(posterCatalogSample(7))
	if !strings.Contains(block, "poster-items 变体") {
		t.Fatalf("目录块应命中 poster-items 分支，实际：\n%s", block)
	}
	if !strings.Contains(block, "module-poster-item module-item]") {
		t.Fatalf("目录块应给出 [包含:] 门控锚点，实际：\n%s", block)
	}
	if !strings.Contains(block, "起始锚只写到") {
		t.Fatal("目录块应提醒起始锚只写到 <a（href/title 在 class 之前）")
	}
	if strings.Contains(block, "条目边界锚点") {
		t.Fatal("poster 目录不应落回通用 <li> 边界逻辑")
	}
}

// poster 详情页：线路容器 module-play-list-content ×2；页头「我的观影记录」
// drop-content-items 与真分集同挂 /vodplay/ 链接（通用投票会被带偏的元凶）。
const posterDetailSample = `<ul class="drop-content-items historical"><li><a href="/vodplay/111-1-1.html"><span>观影记录甲</span></a></li><li><a href="/vodplay/222-1-1.html"><span>观影记录乙</span></a></li><li><a href="/vodplay/333-1-1.html"><span>观影记录丙</span></a></li></ul>` +
	`<div class="module-play-list"><div class="module-play-list-content module-play-list-base">` +
	`<a class="module-play-list-link" href="/vodplay/790001-1-1.html" title="播放问心2第1集"><span>第1集</span></a>` +
	`<a class="module-play-list-link" href="/vodplay/790001-1-2.html" title="播放问心2第2集"><span>第2集</span></a>` +
	`<a class="module-play-list-link" href="/vodplay/790001-1-3.html" title="播放问心2第3集"><span>第3集</span></a></div></div>` +
	`<div class="module-play-list"><div class="module-play-list-content module-play-list-larger">` +
	`<a class="module-play-list-link" href="/vodplay/790001-2-1.html" title="播放问心2第1集"><span>第1集</span></a>` +
	`<a class="module-play-list-link" href="/vodplay/790001-2-2.html" title="播放问心2第2集"><span>第2集</span></a></div></div>`

func TestPosterDetailBlock(t *testing.T) {
	block := detailBlock(posterDetailSample)
	if !strings.Contains(block, "module-play-list-content&&</div>") {
		t.Fatalf("详情块应推荐 module-play-list-content 容器为 播放数组，实际：\n%s", block)
	}
	if !strings.Contains(block, "线路数组 与 播放数组 【同锚点】") {
		t.Fatalf("详情块应说明 线路数组 与 播放数组 同锚点，实际：\n%s", block)
	}
	if !strings.Contains(block, "2 条线路容器") {
		t.Fatalf("多线路计数应为 2，实际：\n%s", block)
	}
	if !strings.Contains(block, "留空即可") {
		t.Fatal("详情块应提示 线路标题 取不到时留空即可")
	}
	if !strings.Contains(block, "我的观影记录") {
		t.Fatal("详情块应警示 drop-content-items 观影记录容器")
	}
	// 固定分支跳过投票：不应出现通用投票文案
	if strings.Contains(block, "投票") {
		t.Fatalf("poster 详情不应走通用投票逻辑，实际：\n%s", block)
	}
}

// 反向护栏：hanjuds 式（tab-list）详情不得落进 poster 分支。
func TestPosterDetailNotHijackHanjuds(t *testing.T) {
	block := detailBlock(mxoneDetailSample)
	if strings.Contains(block, "poster-items") || strings.Contains(block, "module-play-list-content&&") {
		t.Fatalf("hanjuds tab-list 详情不应命中 poster 分支，实际：\n%s", block)
	}
}

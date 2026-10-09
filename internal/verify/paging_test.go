package verify

import "testing"

// TestBuildPagingTemplate 固化分页模板 diff：形态必须是真实 diff 出来的，
// 差异段不是 +1 数字（筛选值、不同分类）一律拒绝。
func TestBuildPagingTemplate(t *testing.T) {
	cases := []struct {
		name      string
		first     string
		second    string
		wantOK    bool
		wantTempl string
	}{
		{"路径式 /type/tv/2/", "https://a.com/type/tv/1/", "https://a.com/type/tv/2/", true, "https://a.com/type/tv/{catePg}/"},
		{"文件名式 list/2-1.html", "https://a.com/list/2-1.html", "https://a.com/list/2-2.html", true, "https://a.com/list/2-{catePg}.html"},
		{"query 式 ?pg=1", "https://a.com/vod/index.html?tid=3&pg=1", "https://a.com/vod/index.html?tid=3&pg=2", true, "https://a.com/vod/index.html?tid=3&pg={catePg}"},
		{"差异是筛选值(非数字)", "https://a.com/show/1.html", "https://a.com/show/hd.html", false, ""},
		{"差异数字非+1", "https://a.com/list/1-1.html", "https://a.com/list/1-3.html", false, ""},
		{"完全相同", "https://a.com/list/1.html", "https://a.com/list/1.html", false, ""},
	}
	for _, c := range cases {
		template, _, _, ok := buildPagingTemplate(c.first, c.second)
		if ok != c.wantOK {
			t.Errorf("%s: ok = %v, 期望 %v（template=%q）", c.name, ok, c.wantOK, template)
			continue
		}
		if ok && template != c.wantTempl {
			t.Errorf("%s: template = %q, 期望 %q", c.name, template, c.wantTempl)
		}
	}
}

// TestPagingCandidatesNextLabel 候选来源：下一页文字链接 + 数字+1 链接都要收，
// 普通详情链接不收；query 式分页（同 path 不同 query）不能被误过滤。
func TestPagingCandidates(t *testing.T) {
	body := `<a href="/list/2-2.html">2</a><a href="/list/2-3.html">3</a><a href="#">置顶</a>` +
		`<a href="/vod/55.html">某片</a><a href="/page/9.html">下一页</a>`
	got := pagingCandidates("https://a.com/list/2-1.html", body)
	if len(got) != 2 {
		t.Fatalf("候选 = %v，期望 2 个（数字+1 的 2-2 与文字下一页的 /page/9）", got)
	}
	queryBody := `<a href="?tid=3&pg=2">下一页</a>`
	if got := pagingCandidates("https://a.com/vod/index.html?tid=3&pg=1", queryBody); len(got) != 1 {
		t.Errorf("query 式分页候选 = %v，期望 1 个", got)
	}
}

// TestEntryOverlap 两页比对：翻页后重合度必须低，同页复制重合度必须高。
func TestEntryOverlap(t *testing.T) {
	first := entrySet(`<a href="/vod/1.html">A</a><a href="/vod/2.html">B</a><a href="/vod/3.html">C</a><a href="/vod/4.html">D</a>`)
	second := entrySet(`<a href="/vod/9.html">I</a><a href="/vod/8.html">H</a><a href="/vod/7.html">G</a><a href="/vod/6.html">F</a>`)
	if score := overlap(first, second); score > 20 {
		t.Errorf("翻页页重合度 = %d%%，应接近 0", score)
	}
	same := entrySet(`<a href="/vod/1.html">A</a><a href="/vod/2.html">B</a><a href="/vod/3.html">C</a><a href="/vod/4.html">D</a>`)
	if score := overlap(first, same); score < 95 {
		t.Errorf("同页重合度 = %d%%，应 ≥95", score)
	}
}

// TestDiffersByNumber 数字+1 差异判定（候选筛选的后备证据）。
func TestDiffersByNumber(t *testing.T) {
	if !differsByNumber("https://a.com/t/1/", "https://a.com/t/2/") {
		t.Errorf("/1/→/2/ 应判定为页码差异")
	}
	if differsByNumber("https://a.com/t/1/", "https://a.com/t/3/") {
		t.Errorf("1→3 不是 +1，不应判定为页码")
	}
	if differsByNumber("https://a.com/t/mov/", "https://a.com/t/tv/") {
		t.Errorf("slug 差异不是页码")
	}
}

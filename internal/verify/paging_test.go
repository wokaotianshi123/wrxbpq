package verify

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wrxbpq/wrxbpq/internal/xbpq"
)

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
	got := pagingCandidates("https://a.com/list/2-1.html", body, idPosition{})
	if len(got) != 2 {
		t.Fatalf("候选 = %v，期望 2 个（数字+1 的 2-2 与文字下一页的 /page/9）", got)
	}
	queryBody := `<a href="?tid=3&pg=2">下一页</a>`
	if got := pagingCandidates("https://a.com/vod/index.html?tid=3&pg=1", queryBody, idPosition{}); len(got) != 1 {
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

// TestBuildCombinedTemplate 固化锚定分类后的组合模板：{cateId} 与 {catePg}
// 必须落在各自真实位置，不再被当成同一槽位互相覆盖（55yss.com 式误判的根因）。
func TestBuildCombinedTemplate(t *testing.T) {
	// 模拟「分类检测」实测：分类 id 在路径第 2 段（list/<id>.html），id 值 "2"。
	idPos := idPosition{pathIndex: 2, prefix: 0, tokenLen: 1, exampleID: "2", examplePath: "https://a.com/list/2.html"}
	cases := []struct {
		name      string
		catalog   string
		candidate string
		want      string
	}{
		{"文件名追加页码 list/2-2.html", "https://a.com/list/2.html", "https://a.com/list/2-2.html", "https://a.com/list/{cateId}-{catePg}.html"},
		{"路径分段页码 list/2/2.html", "https://a.com/list/2.html", "https://a.com/list/2/2.html", "https://a.com/list/{cateId}/{catePg}.html"},
		{"query 页码 list/2.html?page=2", "https://a.com/list/2.html", "https://a.com/list/2.html?page=2", "https://a.com/list/{cateId}.html?page={catePg}"},
	}
	for _, c := range cases {
		got, ok := buildCombinedTemplate(c.catalog, c.candidate, idPos)
		if !ok {
			t.Errorf("%s: 未生成模板", c.name)
			continue
		}
		if got != c.want {
			t.Errorf("%s: 模板 = %q，期望 %q", c.name, got, c.want)
		}
	}
}

// TestProbePagingAnchoredToCategory 复现 55yss.com 式误判并验证修复：
// 分类页同时存在"下一类"链接（list/3.html，实为其它分类）与"下一页"链接（list/2-2.html）。
// 锚定分类 id=2 后，分页探针必须排除"下一类"、只认真正的下一页，
// 产出同时含 {cateId}+{catePg} 的组合模板，而不是把分类号误当页码。
func TestProbePagingAnchoredToCategory(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/list/2.html": // 电视剧（id=2）第 1 页
			page := ""
			for i := 1; i <= 12; i++ {
				page += fmt.Sprintf(`<a href="/p1/%d.html" title="视%d"></a>`, i, i)
			}
			// 含"下一类"（应为其它分类，必须排除）与"下一页"（真正的翻页）
			w.Write([]byte(`<html><body><div>` + page + `</div>
<a href="/list/3.html">综艺</a><a href="/list/2-2.html">下一页</a></body></html>`))
		case "/list/2-2.html": // 电视剧 第 2 页：条目与第 1 页不重合
			page := ""
			for i := 1; i <= 12; i++ {
				page += fmt.Sprintf(`<a href="/p2/%d.html" title="视下%d"></a>`, i, i)
			}
			w.Write([]byte(`<html><body><div>` + page + `</div></body></html>`))
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	fetcher := xbpq.NewFetcher("")
	// 模拟「分类检测」实测得到的 id 位置：list/<id>.html，id 值 2。
	idPos := idPosition{pathIndex: 2, prefix: 0, tokenLen: 1, exampleID: "2", examplePath: server.URL + "/list/2.html"}
	finding := probePaging(context.Background(), fetcher, server.URL+"/list/2.html",
		mustGet(t, fetcher, server.URL+"/list/2.html"), idPos)

	if !finding.Confirmed {
		t.Fatalf("分页实测未通过：\n%s", finding.Note)
	}
	if finding.Template != server.URL+"/list/{cateId}-{catePg}.html" {
		t.Errorf("组合模板 = %q，期望 %q（含 {cateId} 与 {catePg}，而非把分类号当页码）",
			finding.Template, server.URL+"/list/{cateId}-{catePg}.html")
	}
	if !strings.Contains(finding.Template, "{cateId}") || !strings.Contains(finding.Template, "{catePg}") {
		t.Errorf("模板必须同时含 {cateId} 与 {catePg}：%q", finding.Template)
	}
	if strings.Contains(finding.Note, "/list/3.html") {
		t.Errorf("不应把『下一类』链接 list/3.html 当成下一页：\n%s", finding.Note)
	}
}

func mustGet(t *testing.T, fetcher *xbpq.Fetcher, url string) string {
	t.Helper()
	body, err := fetcher.Get(context.Background(), url, "")
	if err != nil {
		t.Fatalf("抓取 %s 失败: %v", url, err)
	}
	return body
}

// TestPagingCandidatesSameCategory 固化锚定分类后的同分类过滤：
// 下一类链接（list/3.html）必须被排除，只保留真正的下一页（list/2-2.html）。
func TestPagingCandidatesSameCategory(t *testing.T) {
	body := `<a href="/list/3.html">综艺</a><a href="/list/2-2.html">下一页</a>`
	idPos := idPosition{pathIndex: 2, prefix: 0, tokenLen: 1, exampleID: "2", examplePath: "https://a.com/list/2.html"}
	got := pagingCandidates("https://a.com/list/2.html", body, idPos)
	if len(got) != 1 || got[0] != "https://a.com/list/2-2.html" {
		t.Errorf("同分类过滤候选 = %v，期望仅 [https://a.com/list/2-2.html]（排除 list/3.html 这个其它分类）", got)
	}
	// 反例：未锚定分类时不做同分类过滤，两个链接都收。
	all := pagingCandidates("https://a.com/list/2.html", body, idPosition{})
	if len(all) != 2 {
		t.Errorf("未锚定时候选 = %v，期望 2 个", all)
	}
}

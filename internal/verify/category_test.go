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

func TestDeriveIDPositionPathForm(t *testing.T) {
	cases := []struct {
		name        string
		a, b        string
		wantOK      bool
		wantID      string
		wantTplTail string // buildTemplate 结果必须以它结尾
	}{
		{"vodtype.html", "https://x.com/vodtype/1.html", "https://x.com/vodtype/2.html", true, "1", "/vodtype/{cateId}.html"},
		{"type slash", "https://x.com/type/1/", "https://x.com/type/2/", true, "1", "/type/{cateId}/"},
		{"dash list", "https://x.com/list/1.html", "https://x.com/list/2.html", true, "1", "/list/{cateId}.html"},
		{"macms placeholder", "https://x.com/list/2-----------.html", "https://x.com/list/3-----------.html", true, "2", "/list/{cateId}-----------.html"},
		{"slug id", "https://x.com/cate/movie/", "https://x.com/cate/tv/", true, "movie", "/cate/{cateId}/"},
		{"different words rejected", "https://x.com/movie1.html", "https://x.com/tv2.html", false, "", ""},
		{"two segments diff rejected", "https://x.com/a/1/b.html", "https://x.com/a/2/c.html", false, "", ""},
	}
	for _, c := range cases {
		position, ok := deriveIDPosition(c.a, c.b)
		if ok != c.wantOK {
			t.Errorf("%s: ok=%v want %v", c.name, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if got := position.extractID(c.a); got != c.wantID {
			t.Errorf("%s: extractID=%q want %q", c.name, got, c.wantID)
		}
		if got := position.extractID(c.b); got == c.wantID {
			t.Errorf("%s: both URLs extract the same id %q", c.name, got)
		}
		if tpl := position.buildTemplate(); !strings.HasSuffix(tpl, c.wantTplTail) {
			t.Errorf("%s: buildTemplate=%q want suffix %q", c.name, tpl, c.wantTplTail)
		}
	}
}

func TestDeriveIDPositionQueryForm(t *testing.T) {
	position, ok := deriveIDPosition("https://x.com/index.php?m=vod-list-id-1.html", "https://x.com/index.php?m=vod-list-id-2.html")
	_ = position
	// 上面这条其实同 path 不同 query 整串，走不了"恰好一个参数不同"——应当拒绝
	if ok {
		t.Logf("query-inside-value diff accepted: %+v", position)
	}
	position, ok = deriveIDPosition("https://x.com/list.php?typeid=1&pg=1", "https://x.com/list.php?typeid=2&pg=1")
	if !ok {
		t.Fatalf("single differing param should be accepted")
	}
	if position.pathIndex != -1 || position.queryKey != "typeid" || position.extractID("https://x.com/list.php?typeid=3&pg=1") != "3" {
		t.Fatalf("query form wrong: %+v", position)
	}
	tpl := position.buildTemplate()
	if !strings.Contains(tpl, "typeid={cateId}") || strings.Contains(tpl, "%7BcateId%7D") {
		t.Errorf("query template wrong: %q", tpl)
	}
	// 两个参数都不同 → 拒绝
	if _, ok := deriveIDPosition("https://x.com/list.php?typeid=1&pg=1", "https://x.com/list.php?typeid=2&pg=2"); ok {
		t.Errorf("two differing params should be rejected")
	}
}

func TestCategoryCandidatesBasics(t *testing.T) {
	body := `<nav>
<a href="/vodtype/1.html">电影</a><a href="/vodtype/2.html">电视剧</a>
<a href="/vodtype/3.html">综艺</a><a href="/">首页</a>
<a href="/about.html">关于我们关于我们关于</a>
<a href="javascript:;">搜索</a>
<a href="https://other.com/vodtype/9.html">外部</a>
</nav>`
	pairs := categoryCandidates("https://x.com", body)
	if len(pairs) != 3 {
		t.Fatalf("want 3 candidates, got %d: %+v", len(pairs), pairs)
	}
	if pairs[0].Name != "电影" || !strings.HasSuffix(pairs[0].URL, "/vodtype/1.html") {
		t.Errorf("first candidate wrong: %+v", pairs[0])
	}
	for _, p := range pairs {
		if strings.Contains(p.URL, "other.com") || p.Name == "首页" {
			t.Errorf("noise leaked: %+v", p)
		}
	}
}

func TestRenderCategoryNoteFormats(t *testing.T) {
	position := idPosition{pathIndex: 2, prefix: 0, tokenLen: 1, exampleID: "1", examplePath: "https://x.com/vodtype/1.html"}
	finding := CategoryFinding{
		Confirmed: []categoryPair{{Name: "电影", URL: "https://x.com/vodtype/1.html", ID: "1"}, {Name: "电视剧", URL: "https://x.com/vodtype/2.html", ID: "2"}},
		Failed:    []categoryPair{{Name: "动漫", URL: "https://x.com/vodtype/4.html", ID: "4"}},
		Template:  "https://x.com/vodtype/{cateId}.html",
	}
	note := renderCategoryNote(finding, position, false)
	for _, want := range []string{
		"分类检测 结论：部分通过",
		`已实测分类串："电影$1#电视剧$2"`,
		"实测失败分类：动漫(4)",
		`实测分类URL模板："https://x.com/vodtype/{cateId}.html"`,
		"分类url 的 {cateId} 位置（实测）",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note missing %q:\n%s", want, note)
		}
	}
	// suspectSame 警告
	note2 := renderCategoryNote(finding, position, true)
	if !strings.Contains(note2, "不同分类 ID 抓回的页面内容几乎一样") {
		t.Errorf("suspectSame warning missing:\n%s", note2)
	}
}

func TestSplitSegmentTokens(t *testing.T) {
	tokens, seps := splitSegmentTokens("2-----------.html")
	// "2-----------" 与 "html" 是 token，"." 是分隔串，首尾各一个空分隔串
	if strings.Join(seps, "|") != "|.|" {
		t.Fatalf("seps wrong: %q", seps)
	}
	if len(tokens) != 2 || tokens[0] != "2-----------" || tokens[1] != "html" {
		t.Fatalf("tokens wrong: %q", tokens)
	}
	tokens, seps = splitSegmentTokens("")
	if len(tokens) != 0 || len(seps) != 1 || seps[0] != "" {
		t.Fatalf("empty segment: tokens=%q seps=%q", tokens, seps)
	}
}

// TestProbeCategoriesEndToEnd 用本地 httptest 站走一遍真实抓取验证：
// 分类 1/2 页面各含 6 个条目（应通过），分类 4 是空页（应进实测失败）。
func TestProbeCategoriesEndToEnd(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte(`<html><body class="stui-x">
<a href="/list/1.html">电影</a><a href="/list/2.html">电视剧</a><a href="/list/4.html">动漫</a>
</body></html>`))
		case "/list/1.html":
			page := ""
			for i := 1; i <= 6; i++ {
				page += fmt.Sprintf(`<a href="/m/%d.html" title="电%d"></a>`, i, i)
			}
			w.Write([]byte(`<html><body class="stui-headers"><div>` + page + `</div></body></html>`))
		case "/list/2.html":
			page := ""
			for i := 1; i <= 6; i++ {
				page += fmt.Sprintf(`<a href="/s/%d.html" title="视%d"></a>`, i, i)
			}
			w.Write([]byte(`<html><body class="stui-headers"><div>` + page + `</div></body></html>`))
		case "/list/4.html":
			w.Write([]byte(`<html><body><p>本分类暂无内容</p></body></html>`))
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	fetcher := xbpq.NewFetcher("")
	home, err := fetcher.Get(context.Background(), server.URL+"/", "")
	if err != nil {
		t.Fatalf("首页抓取失败: %v", err)
	}
	finding := probeCategories(context.Background(), fetcher, server.URL, home)
	t.Logf("note:\n%s", finding.Note)
	if len(finding.Confirmed) != 2 {
		t.Errorf("want 2 confirmed, got %d", len(finding.Confirmed))
	}
	if len(finding.Failed) != 1 || finding.Failed[0].Name != "动漫" {
		t.Errorf("want 动漫 failed, got %+v", finding.Failed)
	}
	if !strings.Contains(finding.Note, `已实测分类串："电影$1#电视剧$2"`) {
		t.Errorf("note missing confirmed string:\n%s", finding.Note)
	}
	if !strings.Contains(finding.Note, `实测分类URL模板："`+server.URL+`/list/{cateId}.html"`) {
		t.Errorf("note missing template:\n%s", finding.Note)
	}
	if !strings.Contains(finding.Note, "实测失败分类：动漫(4)") {
		t.Errorf("note missing failed list:\n%s", finding.Note)
	}
	if strings.Contains(finding.Note, "内容几乎一样") {
		t.Errorf("两分类页内容不同，不应触发 id 未生效警告：\n%s", finding.Note)
	}
	if !strings.Contains(finding.Note, "结论：部分通过") {
		t.Errorf("有失败分类应判部分通过：\n%s", finding.Note)
	}
}

// TestProbeCategoriesSamePageSuspect 不同 ID 回包相同 → 应给出"id 可能没生效"警告。
func TestProbeCategoriesSamePageSuspect(t *testing.T) {
	items := ""
	for i := 1; i <= 6; i++ {
		items += fmt.Sprintf(`<a class="stui-vodlist__thumb" href="/v/%d.html" title="片%d"></a>`, i, i)
	}
	body := `<html><body class="stui-headers"><div>` + items + `</div></body></html>`
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`<html><body class="stui-x"><a href="/list/1.html">电影</a><a href="/list/2.html">电视剧</a></body></html>`))
			return
		}
		w.Write([]byte(body)) // 所有分类回同一页
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	fetcher := xbpq.NewFetcher("")
	home, _ := fetcher.Get(context.Background(), server.URL+"/", "")
	finding := probeCategories(context.Background(), fetcher, server.URL, home)
	if !strings.Contains(finding.Note, "不同分类 ID 抓回的页面内容几乎一样") {
		t.Errorf("suspectSame warning missing:\n%s", finding.Note)
	}
	if strings.Contains(finding.Note, "结论：通过。") {
		t.Errorf("id 未生效时不应判完全通过：\n%s", finding.Note)
	}
}

// TestCategoryCandidatesNoiseWords 复现 6789zz.com 问题2 第一层：
// 导航里的"热门电视剧/最新电影"等榜单/推荐位链接不是独立分类，
// 锚文本含 热门|最新|排行|推荐 等词应在候选阶段直接剔除。
func TestCategoryCandidatesNoiseWords(t *testing.T) {
	body := `<nav>
<a href="/list/2.html">电视剧</a><a href="/list/1.html">电影</a>
<a href="/show/2--hits---------.html">热门电视剧</a>
<a href="/show/1--time---------.html">最新电影</a>
<a href="/list/5.html">短剧</a>
</nav>`
	pairs := categoryCandidates("https://x.com", body)
	if len(pairs) != 3 {
		t.Fatalf("榜单/推荐位应被噪音词过滤，期望 3 个候选，实际 %d: %+v", len(pairs), pairs)
	}
	for _, p := range pairs {
		if strings.Contains(p.Name, "热门") || strings.Contains(p.Name, "最新") {
			t.Errorf("噪音分类混入候选：%+v", p)
		}
	}
}

// TestProbeCategoriesDedupSameID 复现 6789zz.com 问题2 第二层（兜底闸门）：
// 即便榜单链接锚文本没命中噪音词（如叫"热播好剧"），它与"电视剧"提取出同一个 ID，
// 按 ID 去重后分类串也不应出现重复 ID（电视剧$2#…#xxx$2）。
func TestProbeCategoriesDedupSameID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte(`<html><body><nav>
<a href="/list/1.html">电影</a><a href="/list/2.html">电视剧</a><a href="/list/2-x.html">剧集精选</a>
</nav></body></html>`))
		default:
			id := "0"
			for _, r := range r.URL.Path {
				if r >= '0' && r <= '9' {
					id = string(r)
					break
				}
			}
			page := ""
			for i := 1; i <= 6; i++ {
				page += fmt.Sprintf(`<a href="/v/%s/%d.html" title="片%d"></a>`, id, i, i)
			}
			w.Write([]byte(`<html><body class="stui-headers"><div>` + page + `</div></body></html>`))
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	fetcher := xbpq.NewFetcher("")
	home, _ := fetcher.Get(context.Background(), server.URL+"/", "")
	finding := probeCategories(context.Background(), fetcher, server.URL, home)
	t.Logf("note:\n%s", finding.Note)
	if !strings.Contains(finding.Note, `已实测分类串："电影$1#电视剧$2"`) {
		t.Errorf("同 ID 去重后分类串应只剩主分类，实际：\n%s", finding.Note)
	}
	if strings.Contains(finding.Note, "剧集精选") {
		t.Errorf("同 ID 榜单项不应进实测分类串：\n%s", finding.Note)
	}
	// Confirmed 里不得出现重复 ID
	seen := map[string]bool{}
	for _, pair := range finding.Confirmed {
		if seen[pair.ID] {
			t.Errorf("Confirmed 出现重复 ID %q：%+v", pair.ID, finding.Confirmed)
		}
		seen[pair.ID] = true
	}
}

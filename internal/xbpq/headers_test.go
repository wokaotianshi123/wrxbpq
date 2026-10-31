package xbpq

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 「头部集合 / 播放请求头」能力回归：解析、抓取送达、播放阶段覆盖 Referer。

func TestParseHeaderSet(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want map[string]string
	}{
		{"美元分隔", "Authorization$Bearer abc#X-Api-Key$k1", map[string]string{"Authorization": "Bearer abc", "X-Api-Key": "k1"}},
		{"冒号分隔", "Authorization: Bearer abc", map[string]string{"Authorization": "Bearer abc"}},
		{"等号分隔", "Referer=https://a.test/", map[string]string{"Referer": "https://a.test/"}},
		{"换行分隔", "Cookie$sid=1; theme=dark\nX-From$rule", map[string]string{"Cookie": "sid=1; theme=dark", "X-From": "rule"}},
		{"纯 UA 忽略", "Mozilla/5.0 (Windows NT 10.0)", nil},
		{"空值忽略", "Authorization$#X-A$", nil},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got := ParseHeaderSet(item.raw)
			if len(got) != len(item.want) {
				t.Fatalf("头数量不符：got=%v want=%v", got, item.want)
			}
			for name, value := range item.want {
				if got[name] != value {
					t.Fatalf("%s 取值不符：got=%q want=%q", name, got[name], value)
				}
			}
		})
	}
}

func TestRulePlayHeadersMerges(t *testing.T) {
	rule, ok := ParseRule(`{"主页url":"https://a.test","分类url":"https://a.test/list/{cateId}-{catePg}.html","分类":"电影$1",
		"头部集合":"Authorization$Bearer site#Referer$https://a.test/","播放请求头":"Referer$https://play.a.test/"}`)
	if !ok {
		t.Fatal("规则解析失败")
	}
	if got := rule.Headers()["Authorization"]; got != "Bearer site" {
		t.Fatalf("头部集合未解析：%q", got)
	}
	play := rule.PlayHeaders()
	if play["Authorization"] != "Bearer site" || play["Referer"] != "https://play.a.test/" {
		t.Fatalf("播放请求头合并/覆盖错误：%v", play)
	}
	if got := rule.PlayReferer("https://fallback.test/"); got != "https://play.a.test/" {
		t.Fatalf("PlayReferer 未取规则值：%q", got)
	}
}

func TestFetcherSendsRuleHeaders(t *testing.T) {
	var gotAuth, gotCustom, gotUA string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotAuth = request.Header.Get("Authorization")
		gotCustom = request.Header.Get("X-Api-Key")
		gotUA = request.Header.Get("User-Agent")
		_, _ = writer.Write([]byte("<html><body>ok</body></html>"))
	}))
	defer server.Close()

	fetcher := NewFetcher("RuleUA/1.0")
	fetcher.Headers = ParseHeaderSet("Authorization$Bearer tok#X-Api-Key$k9")
	if _, err := fetcher.Get(context.Background(), server.URL, ""); err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	if gotAuth != "Bearer tok" || gotCustom != "k9" {
		t.Fatalf("自定义头未送达：Authorization=%q X-Api-Key=%q", gotAuth, gotCustom)
	}
	if gotUA != "RuleUA/1.0" {
		t.Fatalf("UA 被意外覆盖：%q", gotUA)
	}
}

func TestResolveUsesPlayHeaders(t *testing.T) {
	var sawAuth, sawReferer string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		sawAuth = request.Header.Get("Authorization")
		sawReferer = request.Header.Get("Referer")
		_, _ = writer.Write([]byte(`<script>var player_aaaa={"url":"https://cdn.test/index.m3u8","encrypt":0}</script>`))
	}))
	defer server.Close()

	rule, ok := ParseRule(`{"主页url":"` + server.URL + `","分类url":"` + server.URL + `/list/{cateId}-{catePg}.html","分类":"电影$1",
		"头部集合":"Authorization$Bearer playtok","播放请求头":"Referer$https://www.example-site.test/"}`)
	if !ok {
		t.Fatal("规则解析失败")
	}
	engine := NewEngine(rule, server.URL)
	media, err := engine.Resolve(context.Background(), Chapter{PageURL: server.URL + "/play"})
	if err != nil {
		t.Fatalf("取直链失败: %v", err)
	}
	if sawAuth != "Bearer playtok" {
		t.Fatalf("取直链请求没带 Authorization: %q", sawAuth)
	}
	if sawReferer != "https://www.example-site.test/" {
		t.Fatalf("取直链请求的 Referer 应取 播放请求头: %q", sawReferer)
	}
	if media.Referer != "https://www.example-site.test/" {
		t.Fatalf("media.Referer 应取 播放请求头: %q", media.Referer)
	}
}

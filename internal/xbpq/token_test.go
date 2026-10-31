package xbpq

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestRefreshEndpoint(t *testing.T) {
	cases := []struct {
		name string
		rule string
		base string
		want string
	}{
		{"绝对地址", `{"主页url":"https://api.test/sorani-cms","刷新地址":"https://api.test/api/auth/refresh"}`, "https://api.test/sorani-cms", "https://api.test/api/auth/refresh"},
		{"相对路径", `{"主页url":"https://api.test/sorani-cms","刷新地址":"/api/auth/refresh"}`, "https://api.test/sorani-cms", "https://api.test/api/auth/refresh"},
		{"未配置", `{"主页url":"https://api.test"}`, "https://api.test", ""},
		{"非法值", `{"主页url":"https://api.test","刷新地址":"not-a-url"}`, "https://api.test", ""},
	}
	for _, tc := range cases {
		rule, ok := ParseRule(tc.rule)
		if !ok {
			t.Fatalf("%s: 规则解析失败", tc.name)
		}
		if got := rule.RefreshEndpoint(tc.base); got != tc.want {
			t.Fatalf("%s: 得到 %q，期望 %q", tc.name, got, tc.want)
		}
	}
}

func TestRefreshTokenFields(t *testing.T) {
	plain, _ := ParseRule(`{"主页url":"https://api.test"}`)
	if plain.RefreshToken() != "" {
		t.Fatalf("未配置时不应有 refresh token")
	}
	// 默认路径
	if plain.TokenAccessPath() != "data.token" || plain.TokenRefreshPath() != "data.refreshToken" {
		t.Fatalf("默认路径不符: %q %q", plain.TokenAccessPath(), plain.TokenRefreshPath())
	}
	// 自定义路径
	custom, _ := ParseRule(`{"主页url":"https://api.test","刷新令牌":"r1","令牌路径":"j:result.access","刷新令牌路径":"result.refresh"}`)
	if custom.RefreshToken() != "r1" {
		t.Fatalf("refresh token 未取到: %q", custom.RefreshToken())
	}
	if custom.TokenAccessPath() != "j:result.access" || custom.TokenRefreshPath() != "result.refresh" {
		t.Fatalf("自定义路径未生效: %q %q", custom.TokenAccessPath(), custom.TokenRefreshPath())
	}
}

func TestWithTokenKeepsOtherHeaders(t *testing.T) {
	rule, _ := ParseRule(`{"主页url":"https://api.test","头部集合":"Authorization$Bearer OLD#X-Api-Key$k1","刷新令牌":"R1","分类":"电影$1"}`)
	next := rule.WithToken("NEW", "R2")

	headers := next.PlayHeaders()
	if headers["Authorization"] != "Bearer NEW" {
		t.Fatalf("Authorization 未替换: %q", headers["Authorization"])
	}
	if headers["X-Api-Key"] != "k1" {
		t.Fatalf("其它头被丢弃: %q", headers)
	}
	if next.RefreshToken() != "R2" {
		t.Fatalf("refresh token 未轮换: %q", next.RefreshToken())
	}
	if next.Field("分类") != "电影$1" {
		t.Fatalf("无关字段被破坏: %q", next.Field("分类"))
	}
	// 原规则不受影响
	if rule.RefreshToken() != "R1" {
		t.Fatalf("原规则被就地修改")
	}
}

func TestFormatHeaderSetRoundTrip(t *testing.T) {
	raw := "Authorization$Bearer X#Referer$https://a.test/"
	again := FormatHeaderSet(ParseHeaderSet(raw))
	back := ParseHeaderSet(again)
	if back["Authorization"] != "Bearer X" || back["Referer"] != "https://a.test/" {
		t.Fatalf("往返丢字段: %q -> %q", raw, again)
	}
}

// 全链路：旧令牌被拒 -> 自动刷新 -> 带新令牌重试成功。
func TestEngineRefreshesTokenAndRetries(t *testing.T) {
	var mu sync.Mutex
	var playAuths []string

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/refresh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("刷新应使用 POST，得到 %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"token":"NEW_ACCESS","refreshToken":"NEW_REFRESH"}}`))
	})
	mux.HandleFunc("/play", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		playAuths = append(playAuths, auth)
		mu.Unlock()
		if auth != "Bearer NEW_ACCESS" {
			_, _ = w.Write([]byte(`{"data":{"canPlay":false,"playUrl":null}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"canPlay":true,"playUrl":"` + srv.URL + `/video/index.m3u8"}}`))
	})

	rule, ok := ParseRule(`{
		"主页url":"` + srv.URL + `",
		"头部集合":"Authorization$Bearer OLD",
		"刷新地址":"` + srv.URL + `/refresh",
		"刷新令牌":"R1",
		"跳转播放链接":"j:data.playUrl"
	}`)
	if !ok {
		t.Fatalf("规则解析失败")
	}
	engine := NewEngine(rule, srv.URL)
	engine.Fetcher.Client = srv.Client()

	media, err := engine.Resolve(context.Background(), Chapter{PageURL: srv.URL + "/play"})
	if err != nil {
		t.Fatalf("刷新后应能取到直链: %v", err)
	}
	if media.URL != srv.URL+"/video/index.m3u8" {
		t.Fatalf("直链不符: %q", media.URL)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(playAuths) != 2 {
		t.Fatalf("应请求 2 次播放页，实际 %d: %v", len(playAuths), playAuths)
	}
	if playAuths[0] != "Bearer OLD" || playAuths[1] != "Bearer NEW_ACCESS" {
		t.Fatalf("令牌使用顺序不符: %v", playAuths)
	}
}

func TestEngineNoRefreshWhenNotConfigured(t *testing.T) {
	var mu sync.Mutex
	plays := 0
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/play", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		plays++
		mu.Unlock()
		_, _ = w.Write([]byte(`{"data":{}}`))
	})

	rule, _ := ParseRule(`{"主页url":"` + srv.URL + `","跳转播放链接":"j:data.playUrl"}`)
	engine := NewEngine(rule, srv.URL)
	engine.Fetcher.Client = srv.Client()

	if _, err := engine.Resolve(context.Background(), Chapter{PageURL: srv.URL + "/play"}); err == nil {
		t.Fatalf("没有播放地址时应报错")
	}
	mu.Lock()
	defer mu.Unlock()
	if plays != 1 {
		t.Fatalf("未配置刷新令牌时不应重试，实际 %d 次", plays)
	}
}

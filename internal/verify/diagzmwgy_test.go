package verify

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestDetailIDPositionForms 固化「详情url 模板」样本的反推逻辑：数字 ID 在路径段（含 .html 尾巴）、
// 目录段、query 参数三种形态都要反推成功，且回填比对与原地址逐字一致；非数字 ID 不硬猜。
func TestDetailIDPositionForms(t *testing.T) {
	cases := []struct {
		address  string
		wantTemp string // 空 = 应反推失败
		wantID   string
	}{
		{"https://zmwgy.net/voddetail/120200.html", "https://zmwgy.net/voddetail/{id}.html", "120200"},
		{"https://a.com/detail/138557/", "https://a.com/detail/{id}/", "138557"},
		{"https://a.com/vod/55.html", "https://a.com/vod/{id}.html", "55"},
		{"https://a.com/index.php?m=vod&c=detail&id=9527", "https://a.com/index.php?m=vod&c=detail&id={id}", "9527"},
		{"https://a.com/movie/sc-2026.html", "", ""}, // 非数字 ID：不猜
	}
	for _, c := range cases {
		template, id, ok := detailIDPosition(c.address)
		if c.wantTemp == "" {
			if ok {
				t.Errorf("%s: 不应反推成功，实际模板 %q id %q", c.address, template, id)
			}
			continue
		}
		if !ok || template != c.wantTemp || id != c.wantID {
			t.Errorf("%s: 反推 = (%q,%q,%v)，期望 (%q,%q)", c.address, template, id, ok, c.wantTemp, c.wantID)
		}
	}
}

// TestPickFirstHrefMacCMSNames 固化 MacCMS 命名的详情/播放链接识别（zmwgy.net 教训：
// 旧 detailHints 只认 /vod/…、playHints 只认 /play/…，/voddetail/ 与 /vodplay/ 整段不匹配，
// 链式探测在详情环节断链 → 详情页、播放页样本双双缺失，AI 写源没有播放页参考）。
func TestPickFirstHrefMacCMSNames(t *testing.T) {
	body := `<a href="/voddetail/120200.html">某剧</a><a href="/vodplay/120200-1-1.html">立即播放</a>` +
		`<a href="/vodshow/4-----------.html">电影</a><a href="/index.php/vod/detail/id/55.html">x</a>`
	if got := pickFirstHref(body, detailHints); got == "" || !strings.Contains(got, "voddetail") {
		t.Errorf("detailHints 应命中 /voddetail/120200.html，实际 %q", got)
	}
	if got := pickFirstHref(body, playHints); got == "" || !strings.Contains(got, "vodplay") {
		t.Errorf("playHints 应命中 /vodplay/120200-1-1.html，实际 %q", got)
	}
	// 旧形态必须继续兼容（防回归）。
	oldForms := `<a href="/play/55388-1-1.html">播</a><a href="/vod/55569.html">详</a><a href="/video/x/1">v</a><a href="/bofang/1-1/">b</a>`
	if got := pickFirstHref(oldForms, playHints); got == "" {
		t.Errorf("playHints 旧形态 /play/ 失配：%q", got)
	}
	if got := pickFirstHref(oldForms, detailHints); got == "" {
		t.Errorf("detailHints 旧形态 /vod/ 失配：%q", got)
	}
}

// TestDiagZmwgyProbe 联网诊断：跑完整 Probe 验证 zmwgy.net 能抓到 详情页+播放页 样本。
// 只在联网环境跑（go test -run TestDiagZmwgyProbe -v）。
func TestDiagZmwgyProbe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	samples, err := Probe(ctx, "https://zmwgy.net", 24000)
	if err != nil {
		t.Fatalf("Probe 失败: %v", err)
	}
	var labels []string
	hasDetail, hasPlay := false, false
	for _, s := range samples {
		labels = append(labels, s.Label)
		if strings.HasPrefix(s.Label, "详情页") {
			hasDetail = true
		}
		if strings.HasPrefix(s.Label, "播放页") {
			hasPlay = true
			// 播放页里应能找到直链线索（m3u8）
			if !strings.Contains(s.Content, "m3u8") && !strings.Contains(s.Content, ".mp4") {
				t.Logf("⚠ 播放页样本里没有 m3u8/mp4 字样（可能直链在 JS 变量里）")
			}
		}
		switch {
		case strings.HasPrefix(s.Label, "分类检测"),
			strings.HasPrefix(s.Label, "分页实测"),
			strings.HasPrefix(s.Label, "播放页提示"):
			t.Logf("----- 样本[%s] -----\n%s", s.Label, s.Content)
		}
	}
	t.Logf("样本序列: %s", strings.Join(labels, " → "))
	if !hasDetail {
		t.Errorf("缺详情页样本（链式探测仍在详情环节断链）")
	}
	if !hasPlay {
		t.Errorf("缺播放页样本（AI 写源无 内容进播放页链接/url_after 参考）")
	}
}

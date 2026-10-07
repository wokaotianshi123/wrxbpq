package fingerprint

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wrxbpq/wrxbpq/internal/ai"
	"github.com/wrxbpq/wrxbpq/internal/verify"
)

// TestAnalyzeFFV1RealSite 对真实站点跑一遍 Probe + Analyze，肉眼校验指纹质量。
// 需要网络；默认跳过，设 WRXBQP_ONLINE=1 时执行：go test ./internal/fingerprint/ -run FFV1 -v
func TestAnalyzeFFV1RealSite(t *testing.T) {
	if os.Getenv("WRXBQP_ONLINE") == "" {
		t.Skip("设 WRXBQP_ONLINE=1 才跑联网指纹测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	samples, err := verify.Probe(ctx, "https://www.ffv1.com", 0)
	if err != nil {
		t.Fatalf("Probe 失败: %v", err)
	}
	for _, sample := range samples {
		t.Logf("样本 %s — %d 字符", sample.Label, len(sample.Content))
	}
	text := Analyze(samples)
	t.Logf("\n%s", text)

	// 关键锚点必须出现在指纹里（这些正是 AI 曾经抄错的地方）
	musts := []string{"条目边界锚点", "播放数组 建议", "代表条目原文"}
	for _, token := range musts {
		if !strings.Contains(text, token) {
			t.Errorf("指纹缺少关键行 %q", token)
		}
	}
	// 播放数组建议必须是去尾 > 的形态
	if strings.Contains(text, `播放数组 建议 "<div class=\"row\">&&`) {
		t.Errorf("播放数组 建议带了闭合 >（AI 踩过的坑没被纠正）")
	}
	// 指纹建议的播放数组锚点应能匹配真实页面（与已验证规则等价的前缀形态）
	if line := fingerprintPlayAnchor(t, text); line != "" {
		if !strings.Contains(line, `class=\"row\"`) || strings.Contains(line, "style") {
			t.Errorf("播放数组 建议锚点不理想: %s", line)
		}
	}
}

// fingerprintPlayAnchor 从指纹文本里取出 播放数组 建议 的 pattern。
func fingerprintPlayAnchor(t *testing.T, text string) string {
	t.Helper()
	marker := `播放数组 建议 "`
	at := strings.Index(text, marker)
	if at < 0 {
		return ""
	}
	rest := text[at+len(marker):]
	end := strings.Index(rest, "\"——")
	if end < 0 {
		end = strings.Index(rest, "\n")
	}
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// TestCheckBadRule 用用户 AI 真实产出的坏规则验证格式自检能抓到问题。
func TestCheckBadRule(t *testing.T) {
	samples := []ai.Sample{{
		Label:   "分类页 https://www.ffv1.com/type/tv/1/",
		Content: `<ul class="stui-vodlist__media"><li class="active"><a href="/detail/123/" title="片名">片名</a><span class="pic"><a data-original="https://img/x.jpg"></a></span></li></ul>`,
	}, {
		Label:   "详情页 https://www.ffv1.com/detail/123/",
		Content: `<div class="row" style="display: block;"><ul class="list16"><li><a href="/play/123-1-1/">第01集</a></li></ul></div>`,
	}}
	bad := `{
"主页url": "https://www.ffv1.com",
"数组": "<li class=\"videoItem\" id=\"\">&&</li>",
"标题": "title=\\\"&&\\\"",
"播放数组": "<div class=\"row\">&&</div>",
"线路数组": "<div class=\"row\">&&</div>",
"播放列表": "<li>",
"跳转播放链接": "url: '&&'"
}`
	issues := Check(bad, samples)
	if len(issues) == 0 {
		t.Fatalf("坏规则应被检出问题，实际 0 条")
	}
	found := map[string]bool{}
	for _, issue := range issues {
		t.Logf("检出 [%s] %s", issue.Field, issue.Problem)
		found[issue.Field] = true
	}
	// 数组锚点样本里没有 → 应检出；播放数组带 > → 锚点在样本里一字未现（真实带 style=）→ 应检出；
	// 标题 含字面 \" → 应检出；线路数组=播放数组 → 应检出。
	for _, want := range []string{"数组", "标题", "播放数组", "线路数组"} {
		if !found[want] {
			t.Errorf("未检出字段 %q", want)
		}
	}
}

// TestCheckGoodRule 已验证规则应零误报。
func TestCheckGoodRule(t *testing.T) {
	samples := []ai.Sample{{
		Label:   "分类页",
		Content: `<div class="search container"><ul><li><a href="/detail/1/" title="A"><img data-original="https://i/a.jpg"></a><p>更新至1集</p></li><li><a href="/detail/2/" title="B"><img data-original="https://i/b.jpg"></a><p>更新至2集</p></li><li><a href="/detail/3/" title="C"><img data-original="https://i/c.jpg"></a><p>更新至3集</p></li><li><a href="/detail/4/" title="D"><img data-original="https://i/d.jpg"></a><p>更新至4集</p></li><li><a href="/detail/5/" title="E"><img data-original="https://i/e.jpg"></a><p>更新至5集</p></li><li><a href="/detail/6/" title="F"><img data-original="https://i/f.jpg"></a><p>更新至6集</p></li></ul></div>`,
	}, {
		Label:   "详情页",
		Content: `<div class="row" style="display: block;"><ul class="list16"><li><a href="/play/1-1-1/">第01集</a></li><li><a href="/play/1-1-2/">第02集</a></li><li><a href="/play/1-1-3/">第03集</a></li><li><a href="/play/1-1-4/">第04集</a></li><li><a href="/play/1-1-5/">第05集</a></li><li><a href="/play/1-1-6/">第06集</a></li></ul></div>`,
	}, {
		Label: "播放页",
		Content: `<script type="text/javascript">var player_aaaa={"flag":"play","encrypt":0}; const config = {url: 'https://cdn.example.com/2026/index.m3u8',autoplay: true}</script>`,
	}}
	good := `{
"主页url": "https://www.ffv1.com",
"二次截取": "<div class=\"search container\">&&",
"数组": "<li>&&</li>",
"标题": "title=\"&&\"",
"链接": "href=\"&&\"",
"列表图片": "data-original=\"&&\"",
"副标题": "<p>&&</p>",
"播放数组": "<div class=\"row\"&&</div>",
"播放列表": "<li>",
"播放标题": ">&&</a>",
"播放链接": "href=\"&&\"",
"跳转播放链接": "url: '&&'"
}`
	issues := Check(good, samples)
	for _, issue := range issues {
		t.Errorf("好规则被误报: [%s] %s", issue.Field, issue.Problem)
	}
	fp := Analyze(samples)
	t.Logf("\n%s", fp)
	if !containsStr(fp, "第01集") {
		t.Errorf("指纹未包含分集条目原文")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

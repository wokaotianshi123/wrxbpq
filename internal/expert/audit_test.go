package expert

import (
	"context"
	"strings"
	"testing"

	"github.com/wrxbpq/wrxbpq/internal/ai"
)

// cleanRule 是一份在 stui 测试样本上应全部实测通过的规则。
const cleanRule = `{
  "主页url": "https://demo.com",
  "请求头": "Mozilla/5.0",
  "分类": "电影$1#电视剧$2",
  "分类url": "https://demo.com/type/{cateId}/{catePg}.html",
  "数组": "<li>&&</li>",
  "标题": "title=\"&&\"",
  "链接": "href=\"&&\"",
  "列表图片": "data-original=\"&&\"",
  "副标题": "<span>&&</span>",
  "简介": "<p class=\"col-pd\">&&</p>",
  "线路数组": "<ul class=\"stui-content__playlist clearfix\"&&</ul>",
  "播放数组": "<ul class=\"stui-content__playlist clearfix\"&&</ul>",
  "播放列表": "</li>",
  "播放标题": "\">&&</a>",
  "播放链接": "href=\"&&\""
}`

// withField 在规则末尾字段前追加一个字段（测试用，构造含 跳转播放链接 的变体）。
func withField(rule, field, value string) string {
	index := strings.LastIndex(rule, "}")
	if index < 0 {
		return rule
	}
	return rule[:index] + ",\n  \"" + field + "\": \"" + value + "\"\n}"
}

func issueByField(issues []Issue, field string) (Issue, bool) {
	for _, issue := range issues {
		if issue.Field == field {
			return issue, true
		}
	}
	return Issue{}, false
}

func TestAuditCleanRule(t *testing.T) {
	samples := goodSamples()
	issues := Audit(cleanRule, samples)
	if failures := FailureMessages(issues); len(failures) != 0 {
		var list []string
		for _, f := range failures {
			list = append(list, f.Field+": "+f.Problem)
		}
		t.Fatalf("干净规则不应有实测失败字段，实际失败：%s", strings.Join(list, "；"))
	}
	if len(issues) == 0 {
		t.Fatal("实测通过也应输出逐字段证据（Clean 报告）")
	}
	evidence := EvidenceText(issues)
	for _, want := range []string{"✓ 数组", "✓ 播放列表", "✓ 播放标题"} {
		if !strings.Contains(evidence, want) {
			t.Errorf("逐字段报告缺少 %q，实际：\n%s", want, evidence)
		}
	}
}

func TestAuditBadAnchors(t *testing.T) {
	samples := goodSamples()
	// 数组框错到懒加载占位 span、标题锚点是编造的、播放列表分隔符写成截取串。
	bad := `{
  "主页url": "https://demo.com",
  "分类url": "https://demo.com/type/{cateId}/{catePg}.html",
  "数组": "<span class=\"play hidden-xs\">&&</span>",
  "标题": "data-fake-title=\"&&\"",
  "链接": "href=\"&&\"",
  "播放列表": "<div class=\"row\">&&</div>"
}`
	issues := Audit(bad, samples)
	for _, field := range []string{"数组", "标题"} {
		issue, found := issueByField(issues, field)
		if !found || issue.OK() {
			t.Errorf("字段「%s」应判失败（锚点截不出条目/编造锚点），实际 %+v", field, issue)
		}
	}
	if failures := FailureMessages(issues); len(failures) == 0 {
		t.Error("坏规则必须产出回喂证据")
	} else if failures[0].Field == "" || failures[0].Problem == "" {
		t.Errorf("回喂证据字段/问题不能为空：%+v", failures[0])
	}
}

func TestAuditMissingPlayListDivider(t *testing.T) {
	samples := goodSamples()
	// stui 分集 <li> 连排段内无 # ——没写 播放列表 时真 jar 会把整段当 1 集（55ys9 教训）。
	noDivider := `{
  "主页url": "https://demo.com",
  "分类url": "https://demo.com/type/{cateId}/{catePg}.html",
  "数组": "<li>&&</li>",
  "播放数组": "<ul class=\"stui-content__playlist clearfix\"&&</ul>",
  "播放标题": "\">&&</a>",
  "播放链接": "href=\"&&\""
}`
	issues := Audit(noDivider, samples)
	found := false
	for _, issue := range issues {
		if strings.HasPrefix(issue.Field, "播放列表") && !issue.OK() {
			found = true
			if !strings.Contains(issue.Problem+issue.Detail, "1 集") {
				t.Errorf("播放列表缺失证据应说明塌缩成 1 集，实际：%s %s", issue.Problem, issue.Detail)
			}
		}
	}
	if !found {
		t.Error("缺 播放列表 的分段塌缩未被实测捕获")
	}
}

func TestAuditPlayDirectLink(t *testing.T) {
	samples := append(goodSamples(), ai.Sample{
		Label:   "播放页 https://demo.com/play/100-1-1.html",
		Content: "<html><body><script>var url = 'https://cdn.x.com/a/index.m3u8';</script></body></html>",
	})
	good := withField(cleanRule, "跳转播放链接", "url = '&&'")
	if issue, found := issueByField(Audit(good, samples), "跳转播放链接"); !found || !issue.OK() {
		t.Errorf("播放页能截出 m3u8 的 跳转播放链接 应实测通过，实际 %+v", issue)
	}
	bad := withField(cleanRule, "跳转播放链接", "absentVar = '&&'")
	if issue, found := issueByField(Audit(bad, samples), "跳转播放链接"); !found || issue.OK() {
		t.Errorf("锚点不存在的 跳转播放链接 应判失败，实际 %+v", issue)
	}
}

func TestExtractJSON(t *testing.T) {
	got := ExtractJSON("说明文字\n```json\n{\"主页url\":\"https://a.com\"}\n```\n结尾废话")
	if !strings.HasPrefix(got, "{") || !strings.HasSuffix(got, "}") {
		t.Errorf("应剥离代码块取回 JSON，实际：%q", got)
	}
	if ExtractJSON("彻底没有 JSON") != "" {
		t.Error("无 JSON 时应返回空串")
	}
}

func TestRefineAlreadyClean(t *testing.T) {
	result := Refine(context.Background(), ai.Config{BaseURL: "http://127.0.0.1:1/v1", Model: "none"},
		"https://demo.com", goodSamples(), "", "", cleanRule, 3)
	if !result.Clean {
		t.Errorf("干净规则不应触发回喂轮，应直接 Clean：%+v", result)
	}
	if result.Rounds != 0 {
		t.Errorf("零失败时不应调用 AI，Rounds=%d", result.Rounds)
	}
	if result.Rule != cleanRule {
		t.Error("干净规则应原样返回")
	}
}

func TestRefineStopsOnAIError(t *testing.T) {
	dirty := `{"主页url":"https://demo.com","分类url":"https://demo.com/type/{cateId}/{catePg}.html","标题":"data-fake=\"&&\""}`
	result := Refine(context.Background(), ai.Config{BaseURL: "http://127.0.0.1:1/v1", Model: "none", Timeout: 3},
		"https://demo.com", goodSamples(), "", "", dirty, 3)
	if result.Clean {
		t.Error("坏规则 + AI 不可达不应 Clean")
	}
	if result.Rule != dirty {
		t.Error("AI 不可达时应保留原候选规则（最优版兜底）")
	}
}

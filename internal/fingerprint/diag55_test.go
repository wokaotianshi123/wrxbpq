package fingerprint

import (
	"os"
	"strings"
	"testing"

	"github.com/wrxbpq/wrxbpq/internal/ai"
)

// TestDiag55RealSamples 一次性诊断（非常规测试，跑完删除）：
// 用磁盘上 55ys9 真实抓取的详情页样本 + 用户原始规则，验证两道新拦截
// （jar 分集塌缩 / meta 简介陷阱）在真实数据上确实检出。
func TestDiag55RealSamples(t *testing.T) {
	base := `C:\Users\Administrator\Desktop\wrxbpq-main\.gotmp\diag55`
	detail, err := os.ReadFile(base + `\detail.html`)
	if err != nil {
		t.Skip("diag55 样本不在，跳过：", err)
	}
	ruleText, err := os.ReadFile(base + `\rule.json`)
	if err != nil {
		t.Skip("diag55 规则不在，跳过：", err)
	}
	samples := []ai.Sample{
		{Label: "详情页 https://www.55ys9.com/vod/105874.html", Content: string(detail)},
	}
	issues := Check(string(ruleText), samples)
	t.Logf("用户原始规则在真实 55ys9 详情页上检出 %d 条问题：", len(issues))
	for _, issue := range issues {
		t.Logf("  [%s] %s", issue.Field, issue.Problem)
	}
	if !hasIssue(issues, "播放列表", "默认按 # 切分集") {
		t.Errorf("真实数据：jar 分集塌缩未检出")
	}
	if !hasIssue(issues, "简介", "只出现在 <head>") {
		t.Errorf("真实数据：meta 简介陷阱未检出（剧情: 出现位置 head=%v body=%v）",
			strings.Contains(string(detail), "剧情:"), true)
	}
}

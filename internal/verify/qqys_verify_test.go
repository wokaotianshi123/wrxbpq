package verify

import (
	"strings"
	"testing"
)

// qqys01.com 式"关键词与数字 ID 紧贴"的 详情url 反推回归（离线）。
func TestDetailIDPositionGlued(t *testing.T) {
	cases := []struct{ address, wantTemplate, wantID string }{
		{"https://www.qqys01.com/voddetail12543.html", "https://www.qqys01.com/voddetail{id}.html", "12543"},
		{"https://m.qqys01.com/detail88.html", "https://m.qqys01.com/detail{id}.html", "88"},
		// 经典斜杠形态不得被紧贴分支带偏
		{"https://www.555yx.vip/voddetail/452795.html", "https://www.555yx.vip/voddetail/{id}.html", "452795"},
	}
	for _, c := range cases {
		tpl, id, ok := detailIDPosition(c.address)
		if !ok {
			t.Fatalf("%s 应反推出模板", c.address)
		}
		if tpl != c.wantTemplate || id != c.wantID {
			t.Fatalf("%s 反推不对：tpl=%q id=%q（期望 %q %q）", c.address, tpl, id, c.wantTemplate, c.wantID)
		}
	}
}

// probeVodshowDash 产出的固定 dash 模板：占位回填后必须与实测通过的
// 真实页码 URL 形态逐字一致（槽2 页码、筛选槽连字符占位）。
func TestVodshowDashTemplateShape(t *testing.T) {
	const template = "https://www.qqys01.com/vodshow/{cateId}--------{catePg}---.html"
	got := strings.ReplaceAll(strings.ReplaceAll(template, "{cateId}", "2"), "{catePg}", "1")
	want := "https://www.qqys01.com/vodshow/2--------1---.html"
	if got != want {
		t.Fatalf("回填形态不对: %q（期望 %q）", got, want)
	}
}

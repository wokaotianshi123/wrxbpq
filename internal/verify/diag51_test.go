package verify

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 诊断：跑完整 Probe，复现 51shumai.cn 产出错误分页模板的过程。
func TestDiag51ShuMaiProbe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	samples, err := Probe(ctx, "https://www.51shumai.cn", 24000)
	if err != nil {
		t.Fatalf("Probe 失败: %v", err)
	}
	for _, s := range samples {
		t.Logf("----- 样本[%s] %d 字符 -----", s.Label, len([]rune(s.Content)))
		switch {
		case strings.HasPrefix(s.Label, "分页实测"),
			strings.HasPrefix(s.Label, "分类检测"),
			strings.HasPrefix(s.Label, "分类页"),
			strings.HasPrefix(s.Label, "首页"):
			t.Logf("%s", s.Content)
		}
	}
}

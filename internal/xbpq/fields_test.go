package xbpq

import (
	"strings"
	"testing"
)

// TestGenerateJSONKeepsCommonChars 确保规则里的 && 分隔符、</ul> 这类常见字符
// 以原始形态写入源，而不被标准库转成 \u0026\u003c 等 Unicode 转义。
func TestGenerateJSONKeepsCommonChars(t *testing.T) {
	fields := map[string]string{
		"数组":   "ul&&</ul>",
		"标题":   `href="&&"`,
		"列表图片": "img&&<img>",
	}
	out := GenerateJSON(fields)
	if strings.Contains(out, `\u0026`) {
		t.Fatalf("GenerateJSON 仍包含 \\u0026 转义，实际输出:\n%s", out)
	}
	if strings.Contains(out, `\u003c`) {
		t.Fatalf("GenerateJSON 仍包含 \\u003c 转义，实际输出:\n%s", out)
	}
	if !strings.Contains(out, "&&") {
		t.Fatalf("GenerateJSON 缺少 && 分隔符，实际输出:\n%s", out)
	}
	if !strings.Contains(out, "</ul>") {
		t.Fatalf("GenerateJSON 缺少 </ul> 锚点，实际输出:\n%s", out)
	}
}

// TestMarshalNoEscape 直接验证关闭 HTML 转义的行为。
func TestMarshalNoEscape(t *testing.T) {
	got := MarshalNoEscape("a&&b <c>")
	if got != `"a&&b <c>"` {
		t.Fatalf("MarshalNoEscape 输出异常: %s", got)
	}
}

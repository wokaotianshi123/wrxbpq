package verify

import (
	"net/http"
	"testing"
)

func TestClassifyProbe(t *testing.T) {
	cases := []struct {
		name   string
		status int
		head   string
		want   probeOutcome
	}{
		{"m3u8 正常", http.StatusPartialContent, "#EXTM3U\n#EXT-X-STREAM-INF:800000\n3000k/hls/mixed.m3u8", outcomeMedia},
		{"mp4 正常", http.StatusOK, "\x00\x00\x00\x18ftypmp42", outcomeMedia},
		{"用户实测的 403 地域拒绝页", http.StatusForbidden, `<!DOCTYPE html>
<html lang="en">
<head>
<title>403 Forbidden</title>
</head>
<body>
<h1>403 Forbidden</h1>
<p>The region has been denied.</p>
</body>
</html>`, outcomeBlocked},
		{"451", 451, "<html>blocked by law</html>", outcomeBlocked},
		{"正文含 denied 但状态 200", http.StatusOK, "<html>Access denied</html>", outcomeBlocked},
		{"中文禁止页", http.StatusForbidden, "<h1>访问被禁止</h1>", outcomeBlocked},
		{"普通 500 错误页仍算失败", http.StatusInternalServerError, "<html>Internal Server Error</html>", outcomeBad},
		{"空回包", http.StatusOK, "", outcomeBad},
	}
	for _, one := range cases {
		if got := classifyProbe(one.status, one.head); got != one.want {
			t.Errorf("%s: classifyProbe(%d) = %v, want %v", one.name, one.status, got, one.want)
		}
	}
}

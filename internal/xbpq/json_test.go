package xbpq

import (
	"encoding/base64"
	"testing"
)

const jsonSample = `{
  "code": 20000,
  "data": {
    "total": 3,
    "list": [
      {"name": "剧一", "vid": "v1", "pic": "/p/1.jpg"},
      {"name": "剧二", "vid": "v2", "pic": "/p/2.jpg"},
      {"name": "剧三", "vid": "v3", "pic": "/p/3.jpg"}
    ],
    "seriesInfo": {"title": "信息", "urls": [{"cdnUrl": "https://cdn/a.m3u8"}, {"cdnUrl": "https://cdn/b.m3u8"}]}
  }
}`

func TestJSONPathValue(t *testing.T) {
	cases := []struct{ path, want string }{
		{"j:data.list[0].name", "剧一"},            // 0-based（实测规则 data.list[0]）
		{"data.list[0].name", "剧一"},              // 无前缀点路径
		{"j:data.list[2].vid", "v3"},              // 越界内下标
		{"j:data.list[9].name", ""},               // 越界 → 空
		{"j:data.seriesInfo.urls[0].cdnUrl", "https://cdn/a.m3u8"}, // 真实规则形态
		{"j:data.total", "3"},                     // 数字标量
		{"j:code", "20000"},
		{"j:nope.x", ""},                          // 不存在的键 → 空
	}
	for _, one := range cases {
		if got := CutOnce(jsonSample, one.path); got != one.want {
			t.Errorf("CutOnce(%q) = %q, 期望 %q", one.path, got, one.want)
		}
	}
	// 大小写无关键
	if got := CutOnce(jsonSample, "j:data.list[1].NAME"); got != "剧二" {
		t.Errorf("大小写无关取值失败：%q", got)
	}
}

func TestJSONListIteration(t *testing.T) {
	if got := List(jsonSample, "j:data.list"); len(got) != 3 {
		t.Fatalf("j:data.list 应迭代出 3 条，得到 %d", len(got))
	}
	first := List(jsonSample, "j:data.list")[0]
	if got := CutOnce(first, "j:name"); got != "剧一" {
		t.Fatalf("条目内 j:name = %q", got)
	}
	// [1,] 切片：不含第 0 个
	if got := List(jsonSample, "j:data.list[1,]"); len(got) != 2 {
		t.Fatalf("[1,] 切片应 2 条，得到 %d", len(got))
	}
	// 非数组路径返回单条（SVPI 线路数组=j:data.seriesInfo）
	if got := List(jsonSample, "j:data.seriesInfo"); len(got) != 1 {
		t.Fatalf("对象路径应为 1 条，得到 %d", len(got))
	}
	if got := CutOnce(List(jsonSample, "j:data.seriesInfo")[0], "j:title"); got != "信息" {
		t.Fatalf("对象条目内取值失败：%q", got)
	}
}

func TestJSONConcatPattern(t *testing.T) {
	// 真实规则形态：字面 URL + j: 取值（链接字段）
	item := `{"shortId": "abc123", "upStatus": "更新至12集"}`
	if got := CutOnce(item, "https://m.svipys.cn/my1/book/+j:shortId"); got != "https://m.svipys.cn/my1/book/abc123" {
		t.Errorf("URL+j: 拼接 = %q", got)
	}
	// 字面前缀 + j: 取值（副标题字段）
	if got := CutOnce(item, "菜鸟专属+j:upStatus"); got != "菜鸟专属更新至12集" {
		t.Errorf("字面+j: 拼接 = %q", got)
	}
	// 笔记 item 4：json 模式内单引号表示字面段
	if got := CutOnce(item, "'/play/'+j:shortId+'-1-1.html'"); got != "/play/abc123-1-1.html" {
		t.Errorf("单引号拼接 = %q", got)
	}
}

func TestBase64Syntax(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("hello-xbpq"))
	// 二次截取填 Base64：整段只解码
	if got := CutOnce(encoded, "Base64"); got != "hello-xbpq" {
		t.Errorf("Base64 整段解码 = %q", got)
	}
	// Base64(a&&b)：截取后再解码
	source := `<div>b64="` + encoded + `"</div>`
	if got := CutOnce(source, `Base64(b64="&&")`); got != "hello-xbpq" {
		t.Errorf("Base64() 包裹截取 = %q", got)
	}
}

func TestJSONPatternGuard(t *testing.T) {
	// HTML 源不应被点路径 pattern 误伤
	htmlSource := `<a href="/url.mp4">x</a>`
	if got := CutOnce(htmlSource, "url.mp4"); got != "" {
		t.Errorf("非 JSON 源上点路径应返回空，得到 %q", got)
	}
	// 含引号/&& 的 pattern 不应被当成 json 路径
	if got := CutOnce(htmlSource, `href="&&"`); got != "/url.mp4" {
		t.Errorf("常规截取被破坏：%q", got)
	}
	if isJSONPattern(`href="&&"`) || isJSONPattern("title") {
		t.Error("常规 pattern 误判为 json 路径")
	}
	if !isJSONPattern("data.list[0].name") || !isJSONPattern("j:title") {
		t.Error("json pattern 未识别")
	}
	// 笔记 item 1：不含 && 的字段值是「指定字符串」字面量（固定标题/线路标题）
	if got := CutOnce(htmlSource, "正片"); got != "正片" {
		t.Errorf("字面量回退失败：%q", got)
	}
}

func TestEngineItemsJSONMode(t *testing.T) {
	rule, ok := ParseRule(`{"主页url":"https://m.example.cn","数组":"j:data.list","标题":"j:name","链接":"https://m.example.cn/book/+j:vid","图片":"j:pic"}`)
	if !ok {
		t.Fatal("规则解析失败")
	}
	if rule.Field("数组") != "j:data.list" {
		t.Fatalf("数组字段被模板改写: %q", rule.Field("数组"))
	}
	// 直接在整页上迭代
	if got := List(jsonSample, "j:data.list"); len(got) != 3 {
		t.Fatalf("整页迭代应 3 条")
	}
	first := List(jsonSample, "j:data.list")[0]
	if CutOnce(first, "j:name") != "剧一" || CutOnce(first, "https://m.example.cn/book/+j:vid") != "https://m.example.cn/book/v1" {
		t.Fatal("条目内 json 取值/拼接失败")
	}
}

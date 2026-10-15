package verify

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wrxbpq/wrxbpq/internal/ai"
)

const offlineCatalog = `<html><body>
<ul>
<li class="item"><a href="/v/1.html" title="片名一"><img data-original="/p1.jpg"/><span class="note">更新至4集</span></a></li>
<li class="item"><a href="/v/2.html" title="片名二"><img data-original="/p2.jpg"/><span class="note">完结</span></a></li>
<li class="item"><a href="/v/3.html" title="片名三"><img data-original="/p3.jpg"/><span class="note">更新至8集</span></a></li>
<li class="item"><a href="/v/4.html" title="片名四"><img data-original="/p4.jpg"/><span class="note">预告</span></a></li>
</ul></body></html>`

func ruleJSON(fields map[string]string) string {
	body, _ := json.Marshal(fields)
	return string(body)
}

// 正常链路：数组/标题/链接/列表图片/副标题 全部应在样本上截出合格值。
func TestCheckFieldsOfflinePass(t *testing.T) {
	rule := ruleJSON(map[string]string{
		"主页url":  "https://demo.example",
		"数组":    `<li class="item"&&</li>`,
		"标题":    `title="&&"`,
		"链接":    `href="&&"`,
		"列表图片":  `data-original="&&"`,
		"副标题":   `class="note">&&</span>`,
	})
	findings := CheckFields(context.Background(), FieldOptions{
		Rule:    rule,
		Site:    "https://demo.example",
		Samples: []ai.Sample{{Label: "分类页 https://demo.example/vodtype/1.html", Content: offlineCatalog}},
	}, nil)
	statuses := map[string]string{}
	for _, f := range findings {
		statuses[f.Field] = f.Status
	}
	for _, name := range []string{"数组", "标题", "链接", "列表图片", "副标题"} {
		if statuses[name] != "pass" {
			var msg string
			for _, f := range findings {
				if f.Field == name {
					msg = f.Message
				}
			}
			t.Errorf("字段 %s 应 pass，实际 %s（%s）", name, statuses[name], msg)
		}
	}
}

// 坏锚点：起始锚点一字未现 → fail。
func TestCheckFieldsOfflineBadAnchor(t *testing.T) {
	rule := ruleJSON(map[string]string{
		"主页url": "https://demo.example",
		"数组":    `<div class="nowhere"&&</div>`,
	})
	findings := CheckFields(context.Background(), FieldOptions{
		Rule:    rule,
		Samples: []ai.Sample{{Label: "分类页 x", Content: offlineCatalog}},
	}, []string{"数组"})
	if len(findings) != 1 || findings[0].Status != "fail" {
		t.Fatalf("坏锚点应 fail: %+v", findings)
	}
}

// 裸 ID 链接：截出 12345 这类值应 fail（防路径前缀被锚点吃掉）。
func TestCheckFieldsOfflineBareID(t *testing.T) {
	rule := ruleJSON(map[string]string{
		"主页url": "https://demo.example",
		"数组":    `<li class="item"&&</li>`,
		"链接":    `/v/&&.html`,
	})
	findings := CheckFields(context.Background(), FieldOptions{
		Rule:    rule,
		Samples: []ai.Sample{{Label: "分类页 x", Content: offlineCatalog}},
	}, []string{"链接"})
	if len(findings) != 1 || findings[0].Status != "fail" {
		t.Fatalf("裸 ID 链接应 fail: %+v", findings)
	}
}

// 模板兜底字段（未显式声明）应 skip 而不是误报。
func TestCheckFieldsOfflineTemplateSkip(t *testing.T) {
	// mxone 模板命中形态；规则只写 分类url+分类，数组等由模板补齐。
	rule := ruleJSON(map[string]string{
		"分类url": `https://demo.example/vodlist/index-{cateId}-{catePg}.html`,
		"分类":    `电影$1#电视剧$2`,
	})
	findings := CheckFields(context.Background(), FieldOptions{
		Rule:    rule,
		Samples: []ai.Sample{{Label: "分类页 x", Content: offlineCatalog}},
	}, nil)
	for _, f := range findings {
		if f.Field == "数组" && f.Status != "skip" {
			t.Errorf("模板补齐的 数组 应 skip，实际 %+v", f)
		}
	}
}

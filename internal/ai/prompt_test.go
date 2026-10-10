package ai

import (
	"strings"
	"testing"
)

// TestNormalizeRuleVersion 版本串归一：完整版认 full/中文变体，其它一律回简写版（历史默认）。
func TestNormalizeRuleVersion(t *testing.T) {
	full := []string{"full", "FULL", "  full  ", "完整版", "完整", "完整规则"}
	simple := []string{"", "simple", "SIMPLE", "简写版", "随便什么", "  "}
	for _, value := range full {
		if got := NormalizeRuleVersion(value); got != RuleVersionFull {
			t.Errorf("NormalizeRuleVersion(%q) = %q, 期望 %q", value, got, RuleVersionFull)
		}
	}
	for _, value := range simple {
		if got := NormalizeRuleVersion(value); got != RuleVersionSimple {
			t.Errorf("NormalizeRuleVersion(%q) = %q, 期望 %q", value, got, RuleVersionSimple)
		}
	}
}

// TestGenerateMessagesFullVersion 完整版：消息里必须出现"完整版/显式写全"指令，
// 且指纹段不能再有"直接省略交给兜底"的简写话术。
func TestGenerateMessagesFullVersion(t *testing.T) {
	samples := []Sample{{Label: "首页", Content: "<html>...</html>"}}
	fp := "命中模板家族：MacCMS\n可省略字段：数组、标题、链接"
	messages := BuildGenerateMessages("https://demo.com", samples, "", fp, RuleVersionFull)
	user := messages[len(messages)-1].Content
	for _, want := range []string{"本次输出：完整版", "显式写全", "主页url 在完整版里【必须写】"} {
		if !strings.Contains(user, want) {
			t.Errorf("完整版生成消息缺少指令片段 %q", want)
		}
	}
	// 完整版下指纹段不能说"可省略字段直接省略交给兜底"。
	if strings.Contains(user, "直接省略，交给引擎模板兜底") {
		t.Errorf("完整版生成消息残留简写话术：直接省略交给兜底")
	}
}

// TestGenerateMessagesSimpleVersion 简写版（含空版本回落）：走"命中模板可省略"话术，不出现完整版指令。
func TestGenerateMessagesSimpleVersion(t *testing.T) {
	samples := []Sample{{Label: "首页", Content: "<html>...</html>"}}
	fp := "命中模板家族：MacCMS\n可省略字段：数组、标题、链接"
	for _, version := range []string{"", RuleVersionSimple, "乱填"} {
		messages := BuildGenerateMessages("https://demo.com", samples, "", fp, version)
		user := messages[len(messages)-1].Content
		if !strings.Contains(user, "本次输出：简写版") {
			t.Errorf("版本 %q 应走简写指令，实际消息缺少「本次输出：简写版」", version)
		}
		if strings.Contains(user, "本次输出：完整版") {
			t.Errorf("版本 %q 不应出现完整版指令", version)
		}
	}
}

// TestFixMessagesVersionRequirement 修复消息按版本注入差异化要求，并显式标注本轮版本。
func TestFixMessagesVersionRequirement(t *testing.T) {
	samples := []Sample{{Label: "首页", Content: "<html>"}}
	fullMessages := BuildFixMessages("https://demo.com", `{"主页url":"x"}`, []string{"翻页失败"}, samples, "", "", RuleVersionFull)
	fullUser := fullMessages[len(fullMessages)-1].Content
	if !strings.Contains(fullUser, "本轮是完整版") || !strings.Contains(fullUser, "不允许退化成简写") {
		t.Errorf("完整版修复消息缺少「保持全写不退化」要求")
	}
	if !strings.Contains(fullUser, "本轮版本：【完整版】") {
		t.Errorf("完整版修复消息未标注本轮版本")
	}

	simpleMessages := BuildFixMessages("https://demo.com", `{"主页url":"x"}`, []string{"翻页失败"}, samples, "", "", RuleVersionSimple)
	simpleUser := simpleMessages[len(simpleMessages)-1].Content
	if !strings.Contains(simpleUser, "本轮是简写版") || !strings.Contains(simpleUser, "其它可省字段继续省") {
		t.Errorf("简写版修复消息缺少「只补坏字段继续省」要求")
	}
	if strings.Contains(simpleUser, "不允许退化成简写") {
		t.Errorf("简写版修复消息误注入完整版要求")
	}
}

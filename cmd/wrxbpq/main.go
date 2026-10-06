// Command wrxbpq 是 XBPQ 规则编写与验证工具。
//
// 用法：
//
//	wrxbpq probe   --site https://example.com
//	wrxbpq generate --site https://example.com --base-url https://api.deepseek.com/v1 --model deepseek-chat --api-key sk-xxx
//	wrxbpq verify  --rule rule.json [--site https://example.com] [--steps parse,catalog,play]
//	wrxbpq serve   --port 8080
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/wrxbpq/wrxbpq/internal/ai"
	"github.com/wrxbpq/wrxbpq/internal/httpapi"
	"github.com/wrxbpq/wrxbpq/internal/verify"
	"github.com/wrxbpq/wrxbpq/internal/xbpq"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	command := os.Args[1]
	args := os.Args[2:]
	var err error
	switch command {
	case "probe":
		err = runProbe(args)
	case "generate":
		err = runGenerate(args)
	case "verify":
		err = runVerify(args)
	case "serve":
		err = runServe(args)
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "未知命令：%s\n\n", command)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误："+err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`wrxbpq — XBPQ 规则编写与验证工具

命令：
  probe    抓取站点页面样本（供 AI 分析）
  generate 调用 OpenAI 兼容接口生成规则 JSON
  verify   按步骤验证规则是否可用（含真实播放校验）
  serve    启动本地 Web 服务（含前端界面）

示例：
  wrxbpq probe --site https://www.55yss.com
  wrxbpq generate --site https://www.55yss.com \
      --base-url https://api.deepseek.com/v1 --model deepseek-chat --api-key sk-xxx \
      --out 55yss.json
  wrxbpq verify --rule 55yss.json --steps parse,catalog,paging,search,detail,play
  wrxbpq serve --port 8080
`)
}

func runProbe(args []string) error {
	set := flag.NewFlagSet("probe", flag.ExitOnError)
	site := set.String("site", "", "站点首页地址")
	limit := set.Int("limit", 24000, "每份样本的字符上限")
	out := set.String("out", "", "输出文件（默认打印摘要）")
	_ = set.Parse(args)
	if *site == "" {
		return fmt.Errorf("需要 --site")
	}
	samples, err := verify.Probe(nil, *site, *limit)
	if err != nil {
		return err
	}
	if *out != "" {
		payload, _ := json.MarshalIndent(samples, "", "  ")
		return os.WriteFile(*out, payload, 0o644)
	}
	for _, sample := range samples {
		fmt.Printf("--- %s （%d 字符）\n", sample.Label, len([]rune(sample.Content)))
	}
	return nil
}

func runGenerate(args []string) error {
	set := flag.NewFlagSet("generate", flag.ExitOnError)
	site := set.String("site", "", "站点首页地址")
	baseURL := set.String("base-url", os.Getenv("AI_BASE_URL"), "OpenAI 兼容接口 BaseURL")
	apiKey := set.String("api-key", os.Getenv("AI_API_KEY"), "API Key")
	model := set.String("model", os.Getenv("AI_MODEL"), "模型名")
	rule := set.String("rule", "", "已有规则（给了则进入修复模式）")
	timeout := set.Int("timeout", 180, "AI 调用超时（秒）")
	out := set.String("out", "", "规则输出文件（默认打印到标准输出）")
	_ = set.Parse(args)
	if *site == "" {
		return fmt.Errorf("需要 --site")
	}
	if *baseURL == "" || *model == "" {
		return fmt.Errorf("需要 --base-url 与 --model（或设 AI_BASE_URL / AI_MODEL 环境变量）")
	}
	fmt.Println("正在抓取站点样本…")
	samples, err := verify.Probe(nil, *site, 0)
	if err != nil {
		return err
	}
	for _, sample := range samples {
		fmt.Printf("  ✓ %s\n", sample.Label)
	}
	var messages []ai.Message
	if strings.TrimSpace(*rule) != "" {
		raw, readErr := os.ReadFile(*rule)
		if readErr != nil {
			return readErr
		}
		messages = ai.BuildFixMessages(*site, string(raw), nil, samples)
	} else {
		messages = ai.BuildGenerateMessages(*site, samples)
	}
	fmt.Println("正在调用 AI…")
	content, err := ai.Chat(ai.Config{BaseURL: *baseURL, APIKey: *apiKey, Model: *model, Timeout: *timeout}, messages)
	if err != nil {
		return err
	}
	payload := httpapi.ExtractRule(content)
	if payload == "" {
		fmt.Println("AI 原始回复：\n" + content)
		return fmt.Errorf("未从回复中提取到 JSON")
	}
	if _, ok := xbpq.ParseRule(payload); !ok {
		fmt.Println(payload)
		return fmt.Errorf("AI 产出无法识别为 XBPQ 规则")
	}
	if *out != "" {
		if err := os.WriteFile(*out, []byte(payload), 0o644); err != nil {
			return err
		}
		fmt.Println("已写入 " + *out)
		return nil
	}
	fmt.Println(payload)
	return nil
}

func runVerify(args []string) error {
	set := flag.NewFlagSet("verify", flag.ExitOnError)
	rulePath := set.String("rule", "", "规则 JSON 文件路径")
	ruleText := set.String("rule-text", "", "规则 JSON 文本（与 --rule 二选一）")
	site := set.String("site", "", "覆盖规则里的站点地址")
	keyword := set.String("keyword", "爱", "搜索测试关键词")
	detailID := set.String("detail-id", "", "指定详情 ID")
	episode := set.Int("episode", 1, "播放测试第几集")
	stepsFlag := set.String("steps", strings.Join(httpapi.AllSteps, ","), "验证步骤，逗号分隔")
	jsonOut := set.Bool("json", false, "以 JSON 输出")
	_ = set.Parse(args)
	text := *ruleText
	if text == "" {
		if *rulePath == "" {
			return fmt.Errorf("需要 --rule 或 --rule-text")
		}
		raw, err := os.ReadFile(*rulePath)
		if err != nil {
			return err
		}
		text = string(raw)
	}
	steps := []string{}
	for _, step := range strings.Split(*stepsFlag, ",") {
		if trimmed := strings.TrimSpace(step); trimmed != "" {
			steps = append(steps, trimmed)
		}
	}
	if len(steps) == 0 {
		steps = httpapi.AllSteps
	}
	results := verify.RunSteps(nil, verify.Options{
		Rule: text, Site: *site, Keyword: *keyword, DetailID: *detailID, Episode: *episode, ProbeM3U8: true,
	}, steps)
	if *jsonOut {
		payload, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(payload))
	} else {
		printResults(results)
	}
	for _, result := range results {
		if !result.OK {
			os.Exit(1)
		}
	}
	return nil
}

func printResults(results []verify.StepResult) {
	fmt.Println()
	for _, result := range results {
		mark := "✓"
		if !result.OK {
			mark = "✗"
		}
		fmt.Printf("%s %-8s %s\n", mark, result.Step, result.Message)
		if result.Error != "" {
			fmt.Printf("          错误：%s\n", result.Error)
		}
		for key, value := range result.Details {
			fmt.Printf("          %s = %v\n", key, value)
		}
		for _, row := range result.Rows {
			payload, _ := json.Marshal(row)
			fmt.Printf("          · %s\n", string(payload))
		}
	}
	fmt.Println()
}

func runServe(args []string) error {
	set := flag.NewFlagSet("serve", flag.ExitOnError)
	port := set.Int("port", 8080, "监听端口")
	_ = set.Parse(args)
	if _, err := os.Stat("public"); err != nil {
		fmt.Println("提示：未找到 public 目录，前端静态资源不可用（API 仍可用）")
	}
	fmt.Printf("wrxbpq 本地服务已启动： http://127.0.0.1:%d\n", *port)
	server := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", *port),
		Handler:           httpapi.NewMux(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return server.ListenAndServe()
}

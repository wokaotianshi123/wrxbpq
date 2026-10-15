package expert

import (
	"context"
	"time"

	"github.com/wrxbpq/wrxbpq/internal/ai"
	"github.com/wrxbpq/wrxbpq/internal/xbpq"
)

// Result 是一次专家写源逐字段验证循环的产出。
type Result struct {
	Rule     string // 最终规则（已通过全部实测或已达轮数上限的最优版）
	Rounds   int    // 实际执行的「实测→回喂→再修」轮数
	Evidence string // 最后一轮逐字段实测报告（供前端展示）
	Clean    bool   // 是否所有字段都通过了实测
}

// DefaultMaxRounds 逐字段实测回喂再修的最大轮数，防止慢模型/坏样本无限循环。
const DefaultMaxRounds = 3

// Refine 从给定规则出发做「逐字段实测 → 失败证据回喂 AI 再修 → 再实测」的闭环，
// 直到所有字段实测通过或达到轮数上限。startRule 应是刚生成/修复出的候选规则。
// 每轮都保留失败数最少的版本作为最终产出（坏候选不会覆盖好候选）。
// ctx 带 Deadline 时按剩余预算收紧每轮 AI 超时，预算不足直接收工。
func Refine(ctx context.Context, cfg ai.Config, site string, samples []ai.Sample, notes, fingerprintText, startRule string, maxRounds int) Result {
	if maxRounds <= 0 {
		maxRounds = DefaultMaxRounds
	}
	rule := startRule
	audit := Audit(rule, samples)
	failures := FailureMessages(audit)
	best, bestFail := rule, len(failures)
	evidence := EvidenceText(audit)
	rounds := 0
	for len(failures) > 0 && rounds < maxRounds {
		if err := ctx.Err(); err != nil {
			break
		}
		roundCfg := cfg
		if deadline, ok := ctx.Deadline(); ok {
			remaining := int(time.Until(deadline).Seconds())
			if remaining < 20 {
				break
			}
			if roundCfg.Timeout <= 0 || roundCfg.Timeout > remaining {
				roundCfg.Timeout = remaining
			}
		}
		rounds++
		messages := ai.BuildExpertAuditMessages(site, rule, failures, samples, notes, fingerprintText)
		// 用外层 ctx（带 270 秒预算）发起：Chat 内部对 408/429/5xx 的退避重试
		// 也受同一预算约束，慢模型多轮回喂不会把函数时长撞穿。
		content, err := ai.ChatCtx(ctx, roundCfg, messages)
		if err != nil {
			break
		}
		retried := ExtractJSON(content)
		if retried == "" {
			continue
		}
		if _, ok := xbpq.ParseRule(retried); !ok {
			continue
		}
		retryAudit := Audit(retried, samples)
		retryFailures := FailureMessages(retryAudit)
		if len(retryFailures) < bestFail {
			best, bestFail = retried, len(retryFailures)
			evidence = EvidenceText(retryAudit)
		}
		rule, failures = retried, retryFailures
	}
	return Result{Rule: best, Rounds: rounds, Evidence: evidence, Clean: bestFail == 0}
}

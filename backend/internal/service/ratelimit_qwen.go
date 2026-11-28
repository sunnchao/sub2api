package service

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	qwenTokenPlanExhaustedExtraKey       = "qwen_token_plan_exhausted"
	qwenTokenPlanExhaustedAtExtraKey     = "qwen_token_plan_exhausted_at"
	qwenTokenPlanExhaustedReasonExtraKey = "qwen_token_plan_exhausted_reason"
)

var (
	qwenResetTimestampISO = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?)\b`)
	qwenBeijingLocation   = time.FixedZone("CST", 8*3600)
)

// IsQwen 报告账号是否属于 Qwen / 百炼 / 千问 AI 平台。
func (a *Account) IsQwen() bool {
	return a != nil && a.Platform == PlatformQwen
}

// QwenAccountMode 返回 Qwen 账号的接入模式（缺失时回落默认模式 token_cn）。
func (a *Account) QwenAccountMode() string {
	if !a.IsQwen() {
		return ""
	}
	mode := strings.TrimSpace(a.GetCredential("account_mode"))
	if mode == "" {
		if profile := LookupProviderProfile(PlatformQwen); profile != nil {
			return profile.DefaultMode
		}
	}
	return mode
}

// IsQwenTokenPlan 报告账号是否为 Qwen Token Plan（百炼或千问 AI 平台 / QwenCloud）。
func (a *Account) IsQwenTokenPlan() bool {
	switch a.QwenAccountMode() {
	case "token_cn", "token_sg", "qwencloud_token_cn", "qwencloud_token_sg":
		return true
	default:
		return false
	}
}

// IsQwenCodingPlan 报告账号是否为百炼 Coding Plan。
func (a *Account) IsQwenCodingPlan() bool {
	switch a.QwenAccountMode() {
	case "coding_cn", "coding_sg":
		return true
	default:
		return false
	}
}

// IsQwenPayG 报告账号是否为按量计费模式（百炼或千问 AI 平台 / QwenCloud）。
func (a *Account) IsQwenPayG() bool {
	switch a.QwenAccountMode() {
	case "payg_cn", "payg_sg", "qwencloud_payg_cn", "qwencloud_payg_sg":
		return true
	default:
		return false
	}
}

// normalizeQwenAnthropicThinkingEffortConflict 解决阿里云百炼 / MaaS Anthropic 兼容层
// 参数冲突：当请求同时携带 thinking（budget_tokens 或 type=enabled）与 output_config.effort
// 时，上游将二者分别映射为 thinking_budget 和 reasoning_effort，而 Qwen 引擎互斥报 400。
// 保留显式 thinking 配置，移除冲突的 output_config.effort（若 output_config 变空则一并移除）。
func normalizeQwenAnthropicThinkingEffortConflict(account *Account, body []byte) ([]byte, bool) {
	if account == nil || account.Platform != PlatformQwen || len(body) == 0 {
		return body, false
	}
	if !gjson.GetBytes(body, "output_config.effort").Exists() {
		return body, false
	}
	hasBudget := gjson.GetBytes(body, "thinking.budget_tokens").Exists()
	thinkingEnabled := strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()), "enabled")
	if !hasBudget && !thinkingEnabled {
		return body, false
	}

	updated, err := sjson.DeleteBytes(body, "output_config.effort")
	if err != nil {
		return body, false
	}
	if outCfg := gjson.GetBytes(updated, "output_config"); outCfg.IsObject() && len(outCfg.Map()) == 0 {
		if cleaned, cleanErr := sjson.DeleteBytes(updated, "output_config"); cleanErr == nil {
			updated = cleaned
		}
	}
	return updated, true
}

// handleQwenUpstreamError 处理 Qwen 平台的 402 / 429 响应式限流与额度耗尽逻辑：
//   - 402：按量计费余额不足/欠费为可恢复临时停调，不落入通用 402 永久置 status=error。
//   - 429 瞬时限频（Requests rate limit / rate increased too quickly 等）：不拦截，走默认短冷却。
//   - 429 Token Plan 额度耗尽：带未来重置时间则 SetRateLimited 到点自动恢复；无重置时间
//     （一次性额度耗尽）则写入 qwen_token_plan_exhausted* 并 SetSchedulable(false) 永久停调。
//   - 429 Coding Plan 额度超限：滚动窗口不永久停调，带重置时间则冷却至重置点，否则走默认冷却。
//   - 429 PAYG 余额不足/欠费：按可恢复临时停调处理。
func (s *RateLimitService) handleQwenUpstreamError(
	ctx context.Context,
	account *Account,
	statusCode int,
	responseBody []byte,
	upstreamMsg string,
) (handled bool, shouldDisable bool) {
	if !account.IsQwen() {
		return false, false
	}

	if statusCode == http.StatusPaymentRequired {
		s.handleCNProviderInsufficientBalance(ctx, account, upstreamMsg)
		return true, false
	}
	if statusCode != http.StatusTooManyRequests {
		return false, false
	}

	if qwenTransientRateLimit(responseBody) {
		return false, false
	}

	if account.IsQwenTokenPlan() && qwenTokenPlanQuotaExhausted(responseBody) {
		if resetAt := parseQwenQuotaResetTime(responseBody, time.Now()); resetAt != nil {
			s.notifyAccountSchedulingBlocked(account, *resetAt, "qwen_token_plan_quota_reset")
			if err := s.accountRepo.SetRateLimited(ctx, account.ID, *resetAt); err != nil {
				slog.Warn("qwen_token_plan_rate_limit_set_failed", "account_id", account.ID, "error", err)
				return false, false
			}
			slog.Info("qwen_token_plan_rate_limited",
				"account_id", account.ID,
				"reset_at", resetAt.UTC(),
			)
			return true, false
		}
		s.handleQwenTokenPlanExhausted(ctx, account, responseBody)
		return true, true
	}

	if account.IsQwenCodingPlan() && qwenTokenPlanQuotaExhausted(responseBody) {
		if resetAt := parseQwenQuotaResetTime(responseBody, time.Now()); resetAt != nil {
			s.notifyAccountSchedulingBlocked(account, *resetAt, "qwen_coding_plan_quota_reset")
			if err := s.accountRepo.SetRateLimited(ctx, account.ID, *resetAt); err != nil {
				slog.Warn("qwen_coding_plan_rate_limit_set_failed", "account_id", account.ID, "error", err)
				return false, false
			}
			slog.Info("qwen_coding_plan_rate_limited",
				"account_id", account.ID,
				"reset_at", resetAt.UTC(),
			)
			return true, false
		}
		return false, false
	}

	if account.IsQwenPayG() && (cnProviderResponseIndicatesInsufficientBalance(responseBody) || qwenPayGIndicatesArrearage(responseBody)) {
		s.handleCNProviderInsufficientBalance(ctx, account, upstreamMsg)
		return true, false
	}

	return false, false
}

// qwenTransientRateLimit 识别与配额无关的瞬时限频/突增保护，避免误触发永久停调。
func qwenTransientRateLimit(responseBody []byte) bool {
	if len(responseBody) == 0 {
		return false
	}
	message := strings.ToLower(string(responseBody))
	if strings.Contains(message, "requests rate limit") ||
		strings.Contains(message, "request rate increased too quickly") ||
		strings.Contains(message, "api-key requests rate limit") {
		return true
	}
	if strings.Contains(message, "rate limit exceeded") &&
		!strings.Contains(message, "quota") &&
		!strings.Contains(message, "allocation") &&
		!strings.Contains(message, "token-plan") {
		return true
	}
	return false
}

// qwenTokenPlanQuotaExhausted 识别阿里云百炼 / 千问 AI 平台的配额耗尽文案。
func qwenTokenPlanQuotaExhausted(responseBody []byte) bool {
	if len(responseBody) == 0 {
		return false
	}
	if qwenTransientRateLimit(responseBody) {
		return false
	}
	message := strings.ToLower(string(responseBody))
	if strings.Contains(message, "token-plan") &&
		(strings.Contains(message, "quota") || strings.Contains(message, "exhausted") || strings.Contains(message, "exceeded")) {
		return true
	}
	if strings.Contains(message, "quota") && strings.Contains(message, "exhausted") {
		return true
	}
	if strings.Contains(message, "allocated quota exceeded") {
		return true
	}
	if strings.Contains(message, "insufficient_quota") || strings.Contains(message, "exceeded your current quota") {
		return true
	}
	if (strings.Contains(message, "额度") || strings.Contains(message, "配额") || strings.Contains(message, "限额")) &&
		(strings.Contains(message, "用尽") || strings.Contains(message, "耗尽")) {
		return true
	}
	hasQuota := strings.Contains(message, "quota")
	hasExhaustion := strings.Contains(message, "exhausted") || strings.Contains(message, "exceeded")
	hasReset := strings.Contains(message, "will reset") || strings.Contains(message, "reset at")
	return hasQuota && hasExhaustion && hasReset
}

func qwenPayGIndicatesArrearage(responseBody []byte) bool {
	if len(responseBody) == 0 {
		return false
	}
	message := strings.ToLower(string(responseBody))
	return strings.Contains(message, "arrearage") ||
		strings.Contains(message, "欠费") ||
		strings.Contains(message, "overdue")
}

// parseQwenQuotaResetTime 从上游报错文案（如 "The quota will reset at 2026-09-14 00:00:00"）
// 提取未来重置时间点；无时间戳或已过期返回 nil。
func parseQwenQuotaResetTime(responseBody []byte, now time.Time) *time.Time {
	if len(responseBody) == 0 {
		return nil
	}
	text := string(responseBody)
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "reset") && !strings.Contains(text, "重置") && !strings.Contains(text, "恢复") {
		return nil
	}
	match := qwenResetTimestampISO.FindStringSubmatch(text)
	if len(match) < 2 {
		return nil
	}
	raw := match[1]
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05-0700",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			if parsed.After(now) {
				utc := parsed.UTC()
				return &utc
			}
			return nil
		}
	}
	// 阿里云文档中无时区后缀的重置时间（如 2026-09-14 00:00:00）默认按 UTC+8 解释。
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, raw, qwenBeijingLocation); err == nil {
			if parsed.After(now) {
				utc := parsed.UTC()
				return &utc
			}
			return nil
		}
	}
	return nil
}

// handleQwenTokenPlanExhausted 将一次性额度耗尽的 Token Plan 账号永久移出调度池。
func (s *RateLimitService) handleQwenTokenPlanExhausted(ctx context.Context, account *Account, responseBody []byte) {
	now := time.Now().UTC()
	reason := qwenTokenPlanExhaustionReason(responseBody)
	updates := map[string]any{
		qwenTokenPlanExhaustedExtraKey:       true,
		qwenTokenPlanExhaustedAtExtraKey:     now.Format(time.RFC3339),
		qwenTokenPlanExhaustedReasonExtraKey: reason,
	}
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, updates); err != nil {
		slog.Warn("qwen_token_plan_exhaustion_mark_failed", "account_id", account.ID, "error", err)
	}

	s.notifyAccountSchedulingBlocked(account, time.Time{}, "qwen_token_plan_exhausted")
	if err := s.accountRepo.SetSchedulable(ctx, account.ID, false); err != nil {
		slog.Warn("qwen_token_plan_pause_failed", "account_id", account.ID, "error", err)
		return
	}
	slog.Warn("qwen_token_plan_exhausted", "account_id", account.ID)
}

func qwenTokenPlanExhaustionReason(responseBody []byte) string {
	message := strings.TrimSpace(extractUpstreamErrorMessage(responseBody))
	if message == "" {
		message = strings.TrimSpace(string(responseBody))
	}
	message = strings.Join(strings.Fields(message), " ")
	message = sanitizeUpstreamErrorMessage(message)
	message = truncateForLog([]byte(message), 512)
	if message == "" {
		return "Qwen Token Plan quota exhausted"
	}
	return "Qwen Token Plan quota exhausted: " + message
}

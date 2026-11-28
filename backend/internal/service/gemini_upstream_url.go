package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

type trustedMappedGeminiModelContextKey struct{}

// WithTrustedMappedGeminiModel 标记当前请求的 Gemini 模型名已经由管理员配置
// （Composite 路由或渠道模型映射）改写，构造 AI Studio 上游 URL 时可放行
// 第三方中转常用的非路径分隔字符（如 `[AIS]gemini-3.8-flash`）并做路径转义。
func WithTrustedMappedGeminiModel(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, trustedMappedGeminiModelContextKey{}, true)
}

func isTrustedMappedGeminiModel(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	if v, _ := ctx.Value(trustedMappedGeminiModelContextKey{}).(bool); v {
		return true
	}
	if resolved, ok := ResolvedUpstreamModelFromContext(ctx); ok && strings.TrimSpace(resolved) != "" {
		if requested, okReq := RequestedPublicModelFromContext(ctx); okReq && strings.TrimSpace(requested) != "" && strings.TrimSpace(resolved) != strings.TrimSpace(requested) {
			return true
		}
	}
	return false
}

// geminiAIStudioActions 是允许出现在上游 URL 里的 action 集合，与 ForwardNative
// 的入站校验保持一致，同时避免 compat 侧把可变字符串直接拼进 path。
var geminiAIStudioActions = map[string]struct{}{
	"generateContent":       {},
	"streamGenerateContent": {},
	"countTokens":           {},
}

// buildGeminiAIStudioModelActionURL 组装 AI Studio 的
// /v1beta/models/{model}:{action} 上游 URL。
//
// model 是客户端可控的（native 路由取自 URL 片段，compat 路由取自请求体的 model
// 字段，之后可能再经渠道映射），因此必须先过路径片段护栏才能拼进 path，
// 见 upstream_path_guard.go。新增 AI Studio 端点请一律走本函数。
func buildGeminiAIStudioModelActionURL(baseURL, model, action string, stream bool) (string, error) {
	return buildGeminiAIStudioMappedModelActionURL(baseURL, model, action, stream, false)
}

// buildGeminiAIStudioMappedModelActionURL 组装 AI Studio 的
// /v1beta/models/{model}:{action} 上游 URL。
//
// trustedMapped 为 false 时按严格的客户端输入白名单（[A-Za-z0-9_.-]）校验；
// trustedMapped 为 true（模型名来自管理员配置的账号/渠道/Composite 映射）时，
// 拒绝路径结构与控制字符后经 url.PathEscape 转义拼入单个 path 片段，以兼容
// 第三方中转模型名（如 `[AIS]gemini-3.8-flash`、`[gcli转] gemini-3.1-pro-preview`）。
func buildGeminiAIStudioMappedModelActionURL(baseURL, model, action string, stream bool, trustedMapped bool) (string, error) {
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBase == "" {
		return "", errors.New("gemini base url is required")
	}
	trimmedModel := strings.TrimSpace(model)
	if trimmedModel == "" {
		return "", errors.New("gemini model is required")
	}
	var pathModel string
	if trustedMapped {
		if err := validateMappedGeminiModelPathSegment(trimmedModel); err != nil {
			return "", err
		}
		pathModel = url.PathEscape(trimmedModel)
	} else {
		if err := validateUpstreamPathSegment("gemini model", trimmedModel); err != nil {
			return "", err
		}
		pathModel = trimmedModel
	}
	trimmedAction := strings.TrimSpace(action)
	if _, ok := geminiAIStudioActions[trimmedAction]; !ok {
		return "", fmt.Errorf("unsupported gemini action: %s", trimmedAction)
	}

	fullURL := fmt.Sprintf("%s/v1beta/models/%s:%s", trimmedBase, pathModel, trimmedAction)
	if stream {
		fullURL += "?alt=sse"
	}
	return fullURL, nil
}

// buildGeminiAIStudioMappedModelPath 组装 AI Studio 的 /v1beta/models/{model} 路径后缀。
func buildGeminiAIStudioMappedModelPath(model string, trustedMapped bool) (string, error) {
	trimmedModel := strings.TrimSpace(model)
	if trimmedModel == "" {
		return "", errors.New("gemini model is required")
	}
	if trustedMapped {
		if err := validateMappedGeminiModelPathSegment(trimmedModel); err != nil {
			return "", err
		}
		return "/v1beta/models/" + url.PathEscape(trimmedModel), nil
	}
	if err := validateUpstreamPathSegment("gemini model", trimmedModel); err != nil {
		return "", err
	}
	return "/v1beta/models/" + trimmedModel, nil
}

// isSafeMappedGeminiModelRune 是管理员映射产出的 Gemini 上游模型名闭集允许清单（默认拒绝）：
// 允许 ASCII 字母/数字、_、-、.、Unicode 字母/数字（如中文通道名）、ASCII 普通空格
// 以及第三方中转常见括弧/符号 [ ] ( ) 【 】 +；其余字符（含 *、/、\、?、#、%、:、
// 全角斜杠 ／、NBSP、控制字符等）一律拒绝，且调用方必须经 url.PathEscape 编码后拼入 path。
func isSafeMappedGeminiModelRune(r rune) bool {
	if r < 0x80 {
		if isSafeUpstreamPathSegmentByte(byte(r)) {
			return true
		}
		switch r {
		case ' ', '[', ']', '(', ')', '+':
			return true
		default:
			return false
		}
	}
	switch r {
	case '【', '】':
		return true
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// validateMappedGeminiModelPathSegment 按闭集白名单校验管理员映射产出的 Gemini 上游模型名。
func validateMappedGeminiModelPathSegment(segment string) error {
	if segment == "" || len(segment) > maxUpstreamPathSegmentLen || !utf8.ValidString(segment) {
		return errors.New("invalid gemini model for upstream url path")
	}
	dotsOnly := true
	for _, r := range segment {
		if !isSafeMappedGeminiModelRune(r) {
			return errors.New("invalid gemini model for upstream url path")
		}
		if r != '.' {
			dotsOnly = false
		}
	}
	if dotsOnly {
		return errors.New("invalid gemini model for upstream url path")
	}
	return nil
}

// IsSafeGeminiModelPathSegment 供 handler 层在解析出 URL 里的模型名后立刻校验，
// 让客户端拿到明确的 400，而不是等到构造上游请求时才报错。
func IsSafeGeminiModelPathSegment(model string) bool {
	return isSafeUpstreamPathSegment(strings.TrimSpace(model))
}

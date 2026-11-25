package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const ginNoWrittenSize = -1

type multiGroupDispatcher struct {
	resolver *service.CompositeRouteResolver
	isGemini bool
}

func newMultiGroupDispatcher(resolver *service.CompositeRouteResolver, isGemini bool) *multiGroupDispatcher {
	if resolver == nil {
		resolver = service.NewCompositeRouteResolver(nil)
	}
	return &multiGroupDispatcher{
		resolver: resolver,
		isGemini: isGemini,
	}
}

type capturedAttempt struct {
	status int
	header http.Header
	body   []byte
	keys   map[string]any
	rank   int
}

// multiGroupRetryWriter buffers pre-commit error responses (>= 400) so failed
// group attempts can fall back cleanly to the next candidate without corrupting
// the client socket or leaking headers. Any < 400 response or flush commits
// immediately to the underlying ResponseWriter in pass-through mode.
type multiGroupRetryWriter struct {
	gin.ResponseWriter
	original      gin.ResponseWriter
	bufHeader     http.Header
	bufBody       bytes.Buffer
	status        int
	size          int
	headerWritten bool
	passthrough   bool
}

func newMultiGroupRetryWriter(original gin.ResponseWriter) *multiGroupRetryWriter {
	return &multiGroupRetryWriter{
		ResponseWriter: original,
		original:       original,
		bufHeader:      make(http.Header),
		status:         http.StatusOK,
		size:           ginNoWrittenSize,
	}
}

func (w *multiGroupRetryWriter) Header() http.Header {
	if w.passthrough {
		return w.original.Header()
	}
	return w.bufHeader
}

func (w *multiGroupRetryWriter) WriteHeader(code int) {
	if code <= 0 {
		code = http.StatusOK
	}
	if w.passthrough {
		if code != w.status {
			w.status = code
			w.original.WriteHeader(code)
		}
		return
	}
	if w.headerWritten && code == w.status {
		return
	}
	w.status = code
	w.headerWritten = true
	if code < http.StatusBadRequest {
		w.commitPassthrough()
	}
}

func (w *multiGroupRetryWriter) WriteHeaderNow() {
	if !w.headerWritten {
		w.WriteHeader(w.status)
	}
	if w.passthrough {
		w.original.WriteHeaderNow()
	}
}

func (w *multiGroupRetryWriter) Write(p []byte) (int, error) {
	w.WriteHeaderNow()
	if w.size == ginNoWrittenSize {
		w.size = 0
	}
	if w.passthrough {
		n, err := w.original.Write(p)
		w.size += n
		return n, err
	}
	w.size += len(p)
	return w.bufBody.Write(p)
}

func (w *multiGroupRetryWriter) WriteString(s string) (int, error) {
	w.WriteHeaderNow()
	if w.size == ginNoWrittenSize {
		w.size = 0
	}
	if w.passthrough {
		n, err := w.original.WriteString(s)
		w.size += n
		return n, err
	}
	w.size += len(s)
	return w.bufBody.WriteString(s)
}

func (w *multiGroupRetryWriter) Flush() {
	if !w.headerWritten {
		w.WriteHeader(w.status)
	}
	w.commitPassthrough()
	w.original.Flush()
}

func (w *multiGroupRetryWriter) Status() int {
	if w.passthrough {
		return w.original.Status()
	}
	return w.status
}

func (w *multiGroupRetryWriter) Size() int {
	if w.passthrough {
		return w.original.Size()
	}
	return w.size
}

func (w *multiGroupRetryWriter) Written() bool {
	if w.passthrough {
		return w.original.Written()
	}
	return w.size != ginNoWrittenSize
}

func (w *multiGroupRetryWriter) commitPassthrough() {
	if w.passthrough {
		return
	}
	w.passthrough = true
	copyHeaderMap(w.original.Header(), w.bufHeader)
	w.original.WriteHeader(w.status)
	if w.bufBody.Len() > 0 {
		_, _ = w.original.Write(w.bufBody.Bytes())
		w.bufBody.Reset()
	}
}

func (w *multiGroupRetryWriter) replayToOriginal() {
	if w.passthrough {
		return
	}
	copyHeaderMap(w.original.Header(), w.bufHeader)
	status := w.status
	if status <= 0 {
		status = http.StatusOK
	}
	w.original.WriteHeader(status)
	if w.bufBody.Len() > 0 {
		_, _ = w.original.Write(w.bufBody.Bytes())
	}
}

func copyHeaderMap(dst, src http.Header) {
	if dst == nil || src == nil {
		return
	}
	for k, vals := range src {
		dst[k] = append([]string(nil), vals...)
	}
}

func cloneGinKeys(keys map[string]any) map[string]any {
	if len(keys) == 0 {
		return make(map[string]any)
	}
	cloned := make(map[string]any, len(keys))
	for k, v := range keys {
		if s, ok := v.([]service.OpsUpstreamErrorEvent); ok {
			copied := make([]service.OpsUpstreamErrorEvent, len(s))
			copy(copied, s)
			cloned[k] = copied
			continue
		}
		cloned[k] = v
	}
	return cloned
}

func restoreGinKeys(c *gin.Context, snapshot map[string]any) {
	if c == nil {
		return
	}
	c.Keys = cloneGinKeys(snapshot)
}

func methodHasBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}

func (d *multiGroupDispatcher) prepareOriginalBody(c *gin.Context) ([]byte, bool) {
	if c.Request == nil || c.Request.Body == nil || c.Request.Body == http.NoBody || !methodHasBody(c.Request.Method) {
		return nil, true
	}
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if middleware.RejectRequestBodyReadError(c, "routes.multi_group", err) {
			return nil, false
		}
		middleware.AbortWithRequestBodyReadFailure(c, "routes.multi_group", err)
		return nil, false
	}
	requestmodel.ResetRequestBody(c.Request, body)
	return body, true
}

// Wrap executes handler across bound groups in user-configured order when the
// authenticated API key has multiple candidate groups. Single-group keys invoke
// handler directly with zero overhead.
func (d *multiGroupDispatcher) Wrap(handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		candidates, ok := middleware.GetGroupCandidatesFromContext(c)
		if !ok || len(candidates) <= 1 {
			handler(c)
			return
		}
		baseAPIKey, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || baseAPIKey == nil || c.Request == nil {
			handler(c)
			return
		}

		originalBody, bodyOK := d.prepareOriginalBody(c)
		if !bodyOK {
			return
		}

		baseCtx := service.WithMultiGroupUserRPM(c.Request.Context())
		baseHeaders := c.Request.Header.Clone()
		baseKeys := cloneGinKeys(c.Keys)
		originalWriter := c.Writer

		var bestFailure *capturedAttempt
		var subscriptionOnly bool
		for idx, cand := range candidates {
			if baseCtx.Err() != nil {
				break
			}
			if subscriptionOnly && !hasActiveGroupSubscription(cand) {
				continue
			}

			restoreGinKeys(c, baseKeys)
			c.Errors = c.Errors[:0]
			c.Request = c.Request.WithContext(baseCtx)
			c.Request.Header = baseHeaders.Clone()
			if methodHasBody(c.Request.Method) && (len(originalBody) > 0 || c.Request.Body != nil) {
				attemptBody := originalBody
				if idx < len(candidates)-1 && len(originalBody) > 0 && len(originalBody) <= 4<<20 {
					attemptBody = bytes.Clone(originalBody)
				}
				requestmodel.ResetRequestBody(c.Request, attemptBody)
			}

			middleware.SetActiveGroupCandidate(c, baseAPIKey, cand)

			rw := d.invokeCandidate(c, originalWriter, handler)
			if rw.passthrough {
				return
			}
			if !rw.Written() && !rw.headerWritten {
				return
			}

			bodyBytes := append([]byte(nil), rw.bufBody.Bytes()...)
			reqCtxErr := c.Request.Context().Err()
			if baseCtx.Err() != nil {
				reqCtxErr = baseCtx.Err()
			}
			decision := middleware.GetGroupRetryDecision(c)
			if !shouldRetryNextGroup(rw.status, bodyBytes, hasRemainingSubscription(candidates[idx+1:]), reqCtxErr, decision) {
				rw.replayToOriginal()
				return
			}
			if decision == middleware.GroupRetrySubscriptionOnly {
				subscriptionOnly = true
			}

			rank := failurePriorityRank(rw.status, bodyBytes)
			if bestFailure == nil || rank > bestFailure.rank {
				bestFailure = &capturedAttempt{
					status: rw.status,
					header: rw.bufHeader.Clone(),
					body:   bodyBytes,
					keys:   cloneGinKeys(c.Keys),
					rank:   rank,
				}
			}

			logger.FromContext(baseCtx).Warn("gateway.multi_group_retry_next",
				zap.Int64("api_key_id", baseAPIKey.ID),
				zap.Int("attempt_index", idx),
				zap.Int64("failed_group_id", cand.Group.ID),
				zap.String("failed_platform", cand.Group.Platform),
				zap.Int("status", rw.status),
				zap.Int("remaining_groups", len(candidates)-idx-1),
			)
		}

		if bestFailure != nil {
			restoreGinKeys(c, bestFailure.keys)
			copyHeaderMap(originalWriter.Header(), bestFailure.header)
			status := bestFailure.status
			if status <= 0 {
				status = http.StatusBadGateway
			}
			originalWriter.WriteHeader(status)
			if len(bestFailure.body) > 0 {
				_, _ = originalWriter.Write(bestFailure.body)
			}
		}
	}
}

func (d *multiGroupDispatcher) invokeCandidate(
	c *gin.Context,
	originalWriter gin.ResponseWriter,
	handler gin.HandlerFunc,
) *multiGroupRetryWriter {
	rw := newMultiGroupRetryWriter(originalWriter)
	c.Writer = rw
	defer func() {
		c.Writer = originalWriter
	}()

	if !middleware.CheckGroupModelAllowlist(c) {
		return rw
	}
	if _, forced := middleware.GetForcePlatformFromContext(c); !forced {
		if d.isGemini {
			if !applyCompositeGeminiTargetPlatform(c, d.resolver) {
				return rw
			}
		} else {
			if !applyCompositeTargetPlatform(c, d.resolver) {
				return rw
			}
		}
	}
	handler(c)
	return rw
}

// WrapModels aggregates /v1/models and /v1beta/models listings across all bound
// candidate groups in priority order (deduplicating model IDs). Single-model
// retrieval and Codex manifest queries fall back to sequential ordered execution.
func (d *multiGroupDispatcher) WrapModels(handler gin.HandlerFunc) gin.HandlerFunc {
	fallback := d.Wrap(handler)
	return func(c *gin.Context) {
		candidates, ok := middleware.GetGroupCandidatesFromContext(c)
		if !ok || len(candidates) <= 1 || strings.TrimSpace(c.Param("model")) != "" || strings.TrimSpace(c.Query("client_version")) != "" {
			fallback(c)
			return
		}
		baseAPIKey, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || baseAPIKey == nil || c.Request == nil {
			fallback(c)
			return
		}

		baseCtx := c.Request.Context()
		baseHeaders := c.Request.Header.Clone()
		baseKeys := cloneGinKeys(c.Keys)
		originalWriter := c.Writer

		var successBodies [][]byte
		var bestFailure *capturedAttempt
		for _, cand := range candidates {
			if baseCtx.Err() != nil {
				break
			}
			restoreGinKeys(c, baseKeys)
			c.Errors = c.Errors[:0]
			c.Request = c.Request.WithContext(baseCtx)
			c.Request.Header = baseHeaders.Clone()
			middleware.SetActiveGroupCandidate(c, baseAPIKey, cand)

			rec := newModelsBufferWriter(originalWriter)
			func() {
				c.Writer = rec
				defer func() { c.Writer = originalWriter }()
				if middleware.CheckGroupModelAllowlist(c) {
					handler(c)
				}
			}()

			if rec.status >= http.StatusOK && rec.status < http.StatusMultipleChoices && rec.bufBody.Len() > 0 {
				successBodies = append(successBodies, append([]byte(nil), rec.bufBody.Bytes()...))
				continue
			}

			bodyBytes := append([]byte(nil), rec.bufBody.Bytes()...)
			rank := failurePriorityRank(rec.status, bodyBytes)
			if bestFailure == nil || rank > bestFailure.rank {
				bestFailure = &capturedAttempt{
					status: rec.status,
					header: rec.bufHeader.Clone(),
					body:   bodyBytes,
					keys:   cloneGinKeys(c.Keys),
					rank:   rank,
				}
			}
		}

		if len(successBodies) > 0 {
			if merged, mergedOK := mergeModelsResponses(successBodies, d.isGemini); mergedOK {
				c.Data(http.StatusOK, "application/json; charset=utf-8", merged)
				return
			}
			c.Data(http.StatusOK, "application/json; charset=utf-8", successBodies[0])
			return
		}

		if bestFailure != nil {
			restoreGinKeys(c, bestFailure.keys)
			copyHeaderMap(originalWriter.Header(), bestFailure.header)
			status := bestFailure.status
			if status <= 0 {
				status = http.StatusBadGateway
			}
			originalWriter.WriteHeader(status)
			if len(bestFailure.body) > 0 {
				_, _ = originalWriter.Write(bestFailure.body)
			}
		}
	}
}

// SelectForWebSocket activates the first bound group candidate matching platform
// capabilities without wrapping ResponseWriter (preserving http.Hijacker).
func (d *multiGroupDispatcher) SelectForWebSocket(match func(platform string) bool, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		candidates, ok := middleware.GetGroupCandidatesFromContext(c)
		if !ok || len(candidates) <= 1 {
			handler(c)
			return
		}
		baseAPIKey, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || baseAPIKey == nil {
			handler(c)
			return
		}

		var baseCtx context.Context
		if c.Request != nil {
			baseCtx = c.Request.Context()
		}
		baseKeys := cloneGinKeys(c.Keys)
		originalWriter := c.Writer

		var matchedAny bool
		var bestFailure *capturedAttempt
		for _, cand := range candidates {
			if cand.Group == nil {
				continue
			}
			if match != nil && !match(cand.Group.Platform) {
				continue
			}
			matchedAny = true

			restoreGinKeys(c, baseKeys)
			c.Errors = c.Errors[:0]
			if c.Request != nil && baseCtx != nil {
				c.Request = c.Request.WithContext(baseCtx)
			}
			middleware.SetActiveGroupCandidate(c, baseAPIKey, cand)

			rw := newMultiGroupRetryWriter(originalWriter)
			c.Writer = rw
			allowed := middleware.CheckGroupModelAllowlist(c)
			c.Writer = originalWriter
			if allowed {
				handler(c)
				return
			}

			if rw.passthrough {
				return
			}
			if rw.Written() || rw.headerWritten {
				bodyBytes := append([]byte(nil), rw.bufBody.Bytes()...)
				rank := failurePriorityRank(rw.status, bodyBytes)
				if bestFailure == nil || rank > bestFailure.rank {
					bestFailure = &capturedAttempt{
						status: rw.status,
						header: rw.bufHeader.Clone(),
						body:   bodyBytes,
						keys:   cloneGinKeys(c.Keys),
						rank:   rank,
					}
				}
			}
		}

		if matchedAny {
			if bestFailure != nil {
				restoreGinKeys(c, bestFailure.keys)
				copyHeaderMap(originalWriter.Header(), bestFailure.header)
				status := bestFailure.status
				if status <= 0 {
					status = http.StatusNotFound
				}
				originalWriter.WriteHeader(status)
				if len(bestFailure.body) > 0 {
					_, _ = originalWriter.Write(bestFailure.body)
				}
			}
			c.Abort()
			return
		}
		// No candidate matched the WebSocket platform; invoke handler on active primary
		// group so it emits its native platform rejection.
		handler(c)
	}
}

// modelsBufferWriter buffers a single candidate's /models response even for 200 OK
// so multiple groups' model lists can be merged and deduplicated cleanly.
type modelsBufferWriter struct {
	gin.ResponseWriter
	bufHeader     http.Header
	bufBody       bytes.Buffer
	status        int
	size          int
	headerWritten bool
}

func newModelsBufferWriter(original gin.ResponseWriter) *modelsBufferWriter {
	return &modelsBufferWriter{
		ResponseWriter: original,
		bufHeader:      make(http.Header),
		status:         http.StatusOK,
		size:           ginNoWrittenSize,
	}
}

func (w *modelsBufferWriter) Header() http.Header { return w.bufHeader }
func (w *modelsBufferWriter) WriteHeader(code int) {
	if code > 0 {
		w.status = code
	}
	w.headerWritten = true
}
func (w *modelsBufferWriter) WriteHeaderNow() { w.headerWritten = true }
func (w *modelsBufferWriter) Write(p []byte) (int, error) {
	w.headerWritten = true
	if w.size == ginNoWrittenSize {
		w.size = 0
	}
	w.size += len(p)
	return w.bufBody.Write(p)
}
func (w *modelsBufferWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}
func (w *modelsBufferWriter) Flush()        {}
func (w *modelsBufferWriter) Status() int   { return w.status }
func (w *modelsBufferWriter) Size() int     { return w.size }
func (w *modelsBufferWriter) Written() bool { return w.size != ginNoWrittenSize }

func mergeModelsResponses(bodies [][]byte, isGemini bool) ([]byte, bool) {
	field := "data"
	idKey := "id"
	if isGemini {
		field = "models"
		idKey = "name"
	}

	seen := make(map[string]struct{})
	merged := make([]json.RawMessage, 0)
	for _, body := range bodies {
		arr := gjson.GetBytes(body, field)
		if !arr.Exists() || !arr.IsArray() {
			continue
		}
		for _, item := range arr.Array() {
			modelID := strings.TrimSpace(item.Get(idKey).String())
			if modelID != "" {
				if _, exists := seen[modelID]; exists {
					continue
				}
				seen[modelID] = struct{}{}
			}
			merged = append(merged, json.RawMessage(item.Raw))
		}
	}
	if len(merged) == 0 && len(seen) == 0 {
		// Preserve valid empty list if any response parsed with array field.
		hasValidArray := false
		for _, body := range bodies {
			if arr := gjson.GetBytes(body, field); arr.Exists() && arr.IsArray() {
				hasValidArray = true
				break
			}
		}
		if !hasValidArray {
			return nil, false
		}
	}

	if isGemini {
		out, err := json.Marshal(struct {
			Models []json.RawMessage `json:"models"`
		}{Models: merged})
		return out, err == nil
	}
	out, err := json.Marshal(struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
	}{Object: "list", Data: merged})
	return out, err == nil
}

func hasRemainingSubscription(remaining []middleware.GroupCandidate) bool {
	for _, cand := range remaining {
		if hasActiveGroupSubscription(cand) {
			return true
		}
	}
	return false
}

func hasActiveGroupSubscription(candidate middleware.GroupCandidate) bool {
	return candidate.Subscription != nil && candidate.Group != nil && candidate.Group.IsSubscriptionType()
}

func extractJSONErrorCodeAndMessage(body []byte) (string, string) {
	if !gjson.ValidBytes(body) {
		return "", strings.ToLower(string(body))
	}
	code := strings.TrimSpace(gjson.GetBytes(body, "error.code").String())
	if code == "" {
		code = strings.TrimSpace(gjson.GetBytes(body, "code").String())
	}
	msg := strings.TrimSpace(gjson.GetBytes(body, "error.message").String())
	if msg == "" {
		msg = strings.TrimSpace(gjson.GetBytes(body, "message").String())
	}
	return strings.ToLower(code), strings.ToLower(msg)
}

func isSecurityAuditRejection(code, lowerBody string) bool {
	if code == "content_policy_violation" ||
		code == strings.ToLower(securityaudit.ErrorCodeBlocked) ||
		code == strings.ToLower(securityaudit.ErrorCodeUnavailable) ||
		code == strings.ToLower(securityaudit.ErrorCodeInvalidResponse) {
		return true
	}
	return strings.Contains(lowerBody, "content_policy_violation") ||
		strings.Contains(lowerBody, strings.ToLower(securityaudit.ErrorCodeBlocked))
}

func shouldRetryNextGroup(status int, body []byte, hasRemainingSub bool, reqCtxErr error, decision middleware.GroupRetryDecision) bool {
	if reqCtxErr != nil && errors.Is(reqCtxErr, context.Canceled) {
		return false
	}
	if reqCtxErr != nil || status <= 0 || status == 499 || status < http.StatusBadRequest {
		return false
	}
	if status == http.StatusUnauthorized {
		return false
	}
	switch decision {
	case middleware.GroupRetryStop:
		return false
	case middleware.GroupRetryNext:
		return true
	case middleware.GroupRetrySubscriptionOnly:
		return hasRemainingSub
	}

	code, msg := extractJSONErrorCodeAndMessage(body)
	lowerBody := strings.ToLower(string(body))
	if isSecurityAuditRejection(code, lowerBody) {
		return false
	}

	// Never fail over key-level or user-global hard limits.
	switch code {
	case "api_key_quota_exhausted",
		"api_key_expired",
		"api_key_rate_5h_exceeded",
		"api_key_rate_1d_exceeded",
		"api_key_rate_7d_exceeded",
		"user_rpm_exceeded",
		"access_denied",
		"insufficient_quota":
		return false
	}
	if strings.Contains(msg, "concurrency limit exceeded for user") ||
		strings.Contains(lowerBody, "concurrency limit exceeded for user") {
		return false
	}

	switch status {
	case http.StatusNotFound,
		http.StatusTooManyRequests,
		http.StatusPaymentRequired,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
		529:
		return true

	case http.StatusBadRequest:
		// Retry only group/platform/model capability mismatches; reject genuine
		// client syntax / malformed body errors immediately.
		if service.IsOpenAICompatibleModelNotFound400(body) {
			return true
		}
		for _, pattern := range []string{
			"api key group platform is not gemini",
			"is not supported for this platform",
			"not supported by composite groups",
			"not supported by this openai-compatible endpoint for composite groups",
			strings.ToLower(service.ImageGenerationPermissionMessage()),
			"previous_response_id requires an openai api-key account",
			"reasoning effort",
		} {
			if strings.Contains(msg, pattern) || strings.Contains(lowerBody, pattern) {
				return true
			}
		}
		return false

	case http.StatusForbidden:
		if strings.Contains(msg, "insufficient account balance") || code == "insufficient_balance" {
			return hasRemainingSub
		}
		for _, pattern := range []string{
			"claude code",
			"messages dispatch",
			"allow_messages_dispatch",
			"no active subscription",
			"subscription_not_found",
			"subscription_invalid",
			"reasoning effort",
		} {
			if strings.Contains(msg, pattern) || strings.Contains(code, pattern) || strings.Contains(lowerBody, pattern) {
				return true
			}
		}
		return false

	case http.StatusRequestEntityTooLarge:
		return strings.Contains(msg, strings.ToLower(service.OpenAIRequestBodyTooLargeClientMessage)) ||
			strings.Contains(lowerBody, strings.ToLower(service.OpenAIRequestBodyTooLargeClientMessage))

	default:
		return false
	}
}

func failurePriorityRank(status int, body []byte) int {
	switch {
	case status == http.StatusTooManyRequests ||
		status == http.StatusPaymentRequired ||
		status >= http.StatusInternalServerError:
		return 3
	case status == http.StatusForbidden:
		return 2
	case status == http.StatusBadRequest:
		if service.IsOpenAICompatibleModelNotFound400(body) ||
			strings.Contains(strings.ToLower(string(body)), "is not supported for this platform") ||
			strings.Contains(strings.ToLower(string(body)), "api key group platform is not gemini") {
			return 1
		}
		return 2
	case status == http.StatusNotFound:
		return 1
	default:
		return 0
	}
}

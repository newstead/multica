package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultHindsightTimeout          = 8 * time.Second
	defaultHindsightMaxAttempts      = 3
	defaultHindsightRetryBackoff     = 100 * time.Millisecond
	defaultHindsightMaxRequestBytes  = 1 << 20
	defaultHindsightMaxResponseBytes = 1 << 20
	defaultHindsightRecallTokens     = 1500
	maxHindsightRecallTokens         = 3000
)

var (
	ErrHindsightConfig           = errors.New("hindsight provider config invalid")
	ErrHindsightResponseTooLarge = errors.New("hindsight response exceeds size limit")
)

// HindsightConfig configures the narrow server-side HTTP adapter. Multica uses
// only the memory endpoints it needs instead of importing Hindsight's generated
// client surface.
type HindsightConfig struct {
	BaseURL          string
	APIKey           string
	HTTPClient       *http.Client
	RequestTimeout   time.Duration
	MaxAttempts      int
	RetryBackoff     time.Duration
	MaxRequestBytes  int64
	MaxResponseBytes int64
	RecallMaxTokens  int
	// AggregateTelemetryPath is a private, aggregate-only provider endpoint.
	// It must return only storage_bytes and variable_cost_usd_total; it is not
	// a memory listing endpoint and is intentionally optional.
	AggregateTelemetryPath string

	// Sleep is injectable so retry tests do not pay wall-clock backoff.
	Sleep func(context.Context, time.Duration) error
}

type HindsightProvider struct {
	baseURL                string
	apiKey                 string
	client                 *http.Client
	requestTimeout         time.Duration
	maxAttempts            int
	retryBackoff           time.Duration
	maxRequestBytes        int64
	maxResponseBytes       int64
	recallMaxTokens        int
	aggregateTelemetryPath string
	sleep                  func(context.Context, time.Duration) error
}

var _ MemoryProvider = (*HindsightProvider)(nil)

type HindsightHTTPError struct {
	Operation  string
	StatusCode int
	Retryable  bool
}

func (e *HindsightHTTPError) Error() string {
	return fmt.Sprintf("hindsight %s returned HTTP %d", e.Operation, e.StatusCode)
}

func NewHindsightProvider(cfg HindsightConfig) (*HindsightProvider, error) {
	rawBaseURL := strings.TrimSpace(cfg.BaseURL)
	parsed, err := url.Parse(rawBaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("%w: base_url must be an absolute http(s) URL", ErrHindsightConfig)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%w: base_url must not contain userinfo, query, or fragment", ErrHindsightConfig)
	}
	aggregateTelemetryPath, err := aggregateTelemetryPath(cfg.AggregateTelemetryPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrHindsightConfig, err)
	}

	requestTimeout := cfg.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultHindsightTimeout
	}
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultHindsightMaxAttempts
	}
	retryBackoff := cfg.RetryBackoff
	if retryBackoff <= 0 {
		retryBackoff = defaultHindsightRetryBackoff
	}
	maxRequestBytes := cfg.MaxRequestBytes
	if maxRequestBytes <= 0 {
		maxRequestBytes = defaultHindsightMaxRequestBytes
	}
	maxResponseBytes := cfg.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultHindsightMaxResponseBytes
	}
	recallMaxTokens := cfg.RecallMaxTokens
	if recallMaxTokens <= 0 {
		recallMaxTokens = defaultHindsightRecallTokens
	}
	if recallMaxTokens > maxHindsightRecallTokens {
		recallMaxTokens = maxHindsightRecallTokens
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = sleepWithContext
	}

	return &HindsightProvider{
		baseURL:                strings.TrimRight(parsed.String(), "/"),
		apiKey:                 strings.TrimSpace(cfg.APIKey),
		client:                 client,
		requestTimeout:         requestTimeout,
		maxAttempts:            maxAttempts,
		retryBackoff:           retryBackoff,
		maxRequestBytes:        maxRequestBytes,
		maxResponseBytes:       maxResponseBytes,
		recallMaxTokens:        recallMaxTokens,
		aggregateTelemetryPath: aggregateTelemetryPath,
		sleep:                  sleep,
	}, nil
}

func (p *HindsightProvider) AggregateTelemetry(ctx context.Context) (MemoryProviderAggregateTelemetry, error) {
	if p.aggregateTelemetryPath == "" {
		return MemoryProviderAggregateTelemetry{}, errors.New("aggregate telemetry source unavailable")
	}
	raw, _, err := p.doJSON(ctx, "aggregate telemetry", http.MethodGet, p.aggregateTelemetryPath, nil, nil)
	if err != nil {
		return MemoryProviderAggregateTelemetry{}, err
	}
	return parseProviderAggregateTelemetry(raw)
}

func (p *HindsightProvider) Name() string {
	return "hindsight"
}

func (p *HindsightProvider) Retain(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	content, err := parseHindsightEventContent(event.Content)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	if strings.TrimSpace(content.Text) == "" {
		return MemoryProviderResult{}, fmt.Errorf("%w: retain content.text is required", ErrHindsightConfig)
	}
	documentID := hindsightDocumentID(event, content)
	return p.retainDocument(ctx, event, content, documentID, "retain")
}

func (p *HindsightProvider) Recall(ctx context.Context, req MemoryRecallRequest) (MemoryRecallResult, error) {
	tags, workspaceID, err := hindsightScopeTagsFromRequest(req.Scope)
	if err != nil {
		return MemoryRecallResult{}, err
	}
	if strings.TrimSpace(req.Query) == "" {
		return MemoryRecallResult{}, fmt.Errorf("%w: recall query is required", ErrHindsightConfig)
	}

	maxTokens := p.recallMaxTokens
	if req.Limit > 0 {
		maxTokens = int(req.Limit)
	}
	if maxTokens > p.recallMaxTokens {
		maxTokens = p.recallMaxTokens
	}
	if maxTokens > maxHindsightRecallTokens {
		maxTokens = maxHindsightRecallTokens
	}
	if maxTokens <= 0 {
		maxTokens = defaultHindsightRecallTokens
	}

	requestBody := hindsightRecallRequest{
		Query:     strings.TrimSpace(req.Query),
		Budget:    "mid",
		MaxTokens: maxTokens,
		Trace:     false,
		Tags:      tags,
		TagsMatch: "exact",
		Include: hindsightRecallInclude{
			Entities: nil,
		},
	}
	raw, _, err := p.doJSON(
		ctx,
		"recall",
		http.MethodPost,
		hindsightMemoriesPath(workspaceID)+"/recall",
		requestBody,
		nil,
	)
	if err != nil {
		return MemoryRecallResult{}, err
	}

	var response hindsightRecallResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return MemoryRecallResult{}, fmt.Errorf("decode hindsight recall response: %w", err)
	}
	filtered := make([]json.RawMessage, 0, len(response.Results))
	provenanceItems := make([]hindsightRecallProvenanceItem, 0, len(response.Results))
	for _, itemRaw := range response.Results {
		var item hindsightRecallResult
		if err := json.Unmarshal(itemRaw, &item); err != nil {
			return MemoryRecallResult{}, fmt.Errorf("decode hindsight recall result: %w", err)
		}
		// Exact provider-side matching is mandatory, and this second check keeps
		// a buggy or compromised provider response from crossing scope locally.
		if !sameStringSet(item.Tags, tags) {
			continue
		}
		filtered = append(filtered, itemRaw)
		provenanceItems = append(provenanceItems, hindsightRecallProvenanceItem{
			ProviderRecordID: item.ID,
			DocumentID:       item.DocumentID,
			Score:            item.finalScore(),
			Rank:             len(provenanceItems) + 1,
		})
	}

	resultsJSON, err := json.Marshal(filtered)
	if err != nil {
		return MemoryRecallResult{}, fmt.Errorf("encode hindsight recall results: %w", err)
	}
	provenanceJSON, err := json.Marshal(hindsightRecallProvenance{
		Provider:  p.Name(),
		ScopeTags: tags,
		TagsMatch: "exact",
		MaxTokens: maxTokens,
		Items:     provenanceItems,
	})
	if err != nil {
		return MemoryRecallResult{}, fmt.Errorf("encode hindsight recall provenance: %w", err)
	}
	return MemoryRecallResult{
		Provider:   p.Name(),
		Results:    resultsJSON,
		Provenance: provenanceJSON,
	}, nil
}

func (p *HindsightProvider) Update(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	content, err := parseHindsightEventContent(event.Content)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	if memoryID := hindsightMemoryID(event, content); memoryID != "" {
		requestBody := hindsightUpdateMemoryRequest{
			Text:          optionalNonEmptyString(content.Text),
			Context:       content.Context,
			OccurredStart: content.OccurredStart,
			OccurredEnd:   content.OccurredEnd,
			FactType:      content.FactType,
			Entities:      optionalStringSlice(content.Entities),
			State:         content.State,
			Reason:        content.Reason,
		}
		if requestBody.empty() {
			return MemoryProviderResult{}, fmt.Errorf("%w: update requires a correction or curation state", ErrHindsightConfig)
		}
		return p.curateMemory(ctx, event, memoryID, requestBody, "update")
	}

	documentID := hindsightExplicitDocumentID(event, content)
	if documentID == "" {
		return MemoryProviderResult{}, fmt.Errorf("%w: update requires memory_id or document_id", ErrHindsightConfig)
	}
	if strings.TrimSpace(content.Text) == "" {
		return MemoryProviderResult{}, fmt.Errorf("%w: document update content.text is required", ErrHindsightConfig)
	}
	return p.retainDocument(ctx, event, content, documentID, "update")
}

func (p *HindsightProvider) Invalidate(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	content, err := parseHindsightEventContent(event.Content)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	state := strings.TrimSpace(content.State)
	if state == "" {
		state = "invalidated"
	}
	if state != "invalidated" && state != "valid" {
		return MemoryProviderResult{}, fmt.Errorf("%w: curation state must be invalidated or valid", ErrHindsightConfig)
	}
	memoryID := hindsightMemoryID(event, content)
	if memoryID == "" {
		return MemoryProviderResult{}, fmt.Errorf("%w: invalidate requires memory_id", ErrHindsightConfig)
	}
	return p.curateMemory(ctx, event, memoryID, hindsightUpdateMemoryRequest{
		State:  state,
		Reason: content.Reason,
	}, "invalidate")
}

// Restore reverses a prior invalidation. The provider-neutral interface routes
// restore events through Invalidate with state=valid, while direct callers can
// use this explicit helper.
func (p *HindsightProvider) Restore(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	content, err := parseHindsightEventContent(event.Content)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	memoryID := hindsightMemoryID(event, content)
	if memoryID == "" {
		return MemoryProviderResult{}, fmt.Errorf("%w: restore requires memory_id", ErrHindsightConfig)
	}
	return p.curateMemory(ctx, event, memoryID, hindsightUpdateMemoryRequest{
		State:  "valid",
		Reason: content.Reason,
	}, "restore")
}

func (p *HindsightProvider) Delete(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	content, err := parseHindsightEventContent(event.Content)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	documentID := hindsightExplicitDocumentID(event, content)
	if documentID == "" {
		return MemoryProviderResult{}, fmt.Errorf("%w: delete requires document_id or provider_memory_id", ErrHindsightConfig)
	}
	_, workspaceID, err := hindsightScopeTags(event.Scope)
	if err != nil {
		return MemoryProviderResult{}, err
	}

	raw, status, err := p.doJSON(
		ctx,
		"delete",
		http.MethodDelete,
		hindsightDocumentsPath(workspaceID)+"/"+url.PathEscape(documentID),
		nil,
		map[int]bool{http.StatusNotFound: true},
	)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	if status == http.StatusNotFound {
		raw = json.RawMessage(`{"success":true,"already_deleted":true}`)
	} else {
		var response struct {
			Success bool `json:"success"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return MemoryProviderResult{}, fmt.Errorf("decode hindsight delete response: %w", err)
		}
		if !response.Success {
			return MemoryProviderResult{}, errors.New("hindsight delete response reported failure")
		}
	}
	return MemoryProviderResult{ProviderMemoryID: documentID, Response: raw}, nil
}

func (p *HindsightProvider) Health(ctx context.Context) (MemoryProviderHealth, error) {
	raw, _, err := p.doJSON(ctx, "health", http.MethodGet, "/health", nil, nil)
	if err != nil {
		return MemoryProviderHealth{Provider: p.Name(), OK: false}, err
	}
	details := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &details)
	}
	return MemoryProviderHealth{Provider: p.Name(), OK: true, Details: details}, nil
}

func (p *HindsightProvider) retainDocument(
	ctx context.Context,
	event MemoryEventEnvelope,
	content hindsightEventContent,
	documentID string,
	operation string,
) (MemoryProviderResult, error) {
	tags, workspaceID, err := hindsightScopeTags(event.Scope)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	operationID := hindsightOperationID(event, documentID, operation)
	requestBody := hindsightRetainRequest{
		Items: []hindsightMemoryItem{{
			Content:           strings.TrimSpace(content.Text),
			Timestamp:         content.Timestamp,
			Context:           content.Context,
			Metadata:          hindsightMetadata(event),
			DocumentID:        documentID,
			Tags:              tags,
			ObservationScopes: "combined",
			UpdateMode:        "replace",
		}},
		Async:       true,
		OperationID: operationID,
	}
	raw, _, err := p.doJSON(
		ctx,
		operation,
		http.MethodPost,
		hindsightMemoriesPath(workspaceID),
		requestBody,
		nil,
	)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	var response struct {
		Success     bool   `json:"success"`
		OperationID string `json:"operation_id"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return MemoryProviderResult{}, fmt.Errorf("decode hindsight %s response: %w", operation, err)
	}
	if !response.Success {
		return MemoryProviderResult{}, fmt.Errorf("hindsight %s response reported failure", operation)
	}
	return MemoryProviderResult{ProviderMemoryID: documentID, Response: raw}, nil
}

func (p *HindsightProvider) curateMemory(
	ctx context.Context,
	event MemoryEventEnvelope,
	memoryID string,
	requestBody hindsightUpdateMemoryRequest,
	operation string,
) (MemoryProviderResult, error) {
	_, workspaceID, err := hindsightScopeTags(event.Scope)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	raw, _, err := p.doJSON(
		ctx,
		operation,
		http.MethodPatch,
		hindsightMemoriesPath(workspaceID)+"/"+url.PathEscape(memoryID),
		requestBody,
		nil,
	)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	if len(raw) == 0 || !json.Valid(raw) {
		return MemoryProviderResult{}, fmt.Errorf("decode hindsight %s response: invalid JSON", operation)
	}
	return MemoryProviderResult{ProviderMemoryID: memoryID, Response: raw}, nil
}

func (p *HindsightProvider) doJSON(
	parent context.Context,
	operation string,
	method string,
	path string,
	body any,
	acceptedStatus map[int]bool,
) (json.RawMessage, int, error) {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("encode hindsight %s request: %w", operation, err)
		}
		if int64(len(payload)) > p.maxRequestBytes {
			return nil, 0, fmt.Errorf("%w: request is %d bytes", ErrHindsightConfig, len(payload))
		}
	}

	ctx, cancel := context.WithTimeout(parent, p.requestTimeout)
	defer cancel()

	for attempt := 1; attempt <= p.maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, bytes.NewReader(payload))
		if err != nil {
			return nil, 0, fmt.Errorf("build hindsight %s request: %w", operation, err)
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if p.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+p.apiKey)
		}

		resp, err := p.client.Do(req)
		if err != nil {
			if resp != nil && resp.Body != nil {
				resp.Body.Close()
			}
			if ctx.Err() != nil {
				return nil, 0, fmt.Errorf("hindsight %s: %w", operation, ctx.Err())
			}
			if !hindsightRetryableTransport(err) || attempt == p.maxAttempts {
				return nil, 0, fmt.Errorf("hindsight %s transport: %w", operation, err)
			}
			if err := p.sleep(ctx, retryDelay(p.retryBackoff, attempt, "")); err != nil {
				return nil, 0, fmt.Errorf("hindsight %s: %w", operation, err)
			}
			continue
		}

		raw, readErr := readHindsightResponse(resp.Body, p.maxResponseBytes)
		resp.Body.Close()
		if readErr != nil {
			return nil, resp.StatusCode, fmt.Errorf("hindsight %s: %w", operation, readErr)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 || acceptedStatus[resp.StatusCode] {
			return raw, resp.StatusCode, nil
		}

		retryable := hindsightRetryableStatus(resp.StatusCode)
		httpErr := &HindsightHTTPError{
			Operation:  operation,
			StatusCode: resp.StatusCode,
			Retryable:  retryable,
		}
		if !retryable || attempt == p.maxAttempts {
			return nil, resp.StatusCode, httpErr
		}
		if err := p.sleep(ctx, retryDelay(p.retryBackoff, attempt, resp.Header.Get("Retry-After"))); err != nil {
			return nil, resp.StatusCode, fmt.Errorf("hindsight %s: %w", operation, err)
		}
	}
	return nil, 0, fmt.Errorf("hindsight %s exhausted retries", operation)
}

func readHindsightResponse(body io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, ErrHindsightResponseTooLarge
	}
	return raw, nil
}

func hindsightRetryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func hindsightRetryableTransport(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError)
}

func retryDelay(base time.Duration, attempt int, retryAfter string) time.Duration {
	if retryAfter != "" {
		if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
		if when, err := http.ParseTime(retryAfter); err == nil {
			if delay := time.Until(when); delay > 0 {
				return delay
			}
			return 0
		}
	}
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 5 {
		shift = 5
	}
	return base * time.Duration(1<<shift)
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type hindsightEventContent struct {
	Text             string   `json:"text"`
	Summary          string   `json:"summary"`
	Timestamp        string   `json:"timestamp"`
	Context          *string  `json:"context"`
	ProviderMemoryID string   `json:"provider_memory_id"`
	MemoryID         string   `json:"memory_id"`
	DocumentID       string   `json:"document_id"`
	State            string   `json:"state"`
	Reason           *string  `json:"reason"`
	OccurredStart    *string  `json:"occurred_start"`
	OccurredEnd      *string  `json:"occurred_end"`
	FactType         *string  `json:"fact_type"`
	Entities         []string `json:"entities"`
}

func parseHindsightEventContent(raw json.RawMessage) (hindsightEventContent, error) {
	if len(raw) == 0 {
		return hindsightEventContent{}, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return hindsightEventContent{Text: text}, nil
	}
	var content hindsightEventContent
	if err := json.Unmarshal(raw, &content); err != nil {
		return hindsightEventContent{}, fmt.Errorf("%w: content must be a JSON object or string", ErrHindsightConfig)
	}
	if strings.TrimSpace(content.Text) == "" {
		content.Text = content.Summary
	}
	return content, nil
}

func hindsightScopeTagsFromRequest(scope MemoryScope) ([]string, string, error) {
	return hindsightScopeTags(memoryScopeJSON{
		WorkspaceID: uuidString(scope.WorkspaceID),
		ProjectID:   uuidString(scope.ProjectID),
		AgentID:     uuidString(scope.AgentID),
		IssueID:     uuidString(scope.IssueID),
		TaskID:      uuidString(scope.TaskID),
	})
}

func hindsightScopeTags(scope memoryScopeJSON) ([]string, string, error) {
	workspaceID := strings.TrimSpace(scope.WorkspaceID)
	if _, err := uuid.Parse(workspaceID); err != nil {
		return nil, "", fmt.Errorf("%w: workspace_id is required and must be a UUID", ErrHindsightConfig)
	}
	tags := []string{"workspace:" + workspaceID}
	for _, item := range []struct {
		name  string
		value string
	}{
		{name: "project", value: scope.ProjectID},
		{name: "agent", value: scope.AgentID},
		{name: "issue", value: scope.IssueID},
	} {
		value := strings.TrimSpace(item.value)
		if value == "" {
			continue
		}
		if _, err := uuid.Parse(value); err != nil {
			return nil, "", fmt.Errorf("%w: %s_id must be a UUID", ErrHindsightConfig, item.name)
		}
		tags = append(tags, item.name+":"+value)
	}
	return tags, workspaceID, nil
}

func hindsightBankID(workspaceID string) string {
	return "multica-ws-" + strings.ReplaceAll(workspaceID, "-", "")
}

func hindsightMemoriesPath(workspaceID string) string {
	return "/v1/default/banks/" + url.PathEscape(hindsightBankID(workspaceID)) + "/memories"
}

func hindsightDocumentsPath(workspaceID string) string {
	return "/v1/default/banks/" + url.PathEscape(hindsightBankID(workspaceID)) + "/documents"
}

func hindsightDocumentID(event MemoryEventEnvelope, content hindsightEventContent) string {
	if explicit := hindsightExplicitDocumentID(event, content); explicit != "" {
		return explicit
	}
	sourceType := hindsightMetadataString(event.Metadata, "source_type")
	sourceID := hindsightMetadataString(event.Metadata, "source_id")
	var identity string
	if sourceType != "" && sourceID != "" {
		identity = strings.Join([]string{event.Scope.WorkspaceID, sourceType, sourceID}, "\x00")
	} else {
		raw, _ := json.Marshal(event)
		identity = string(raw)
	}
	return "multica-" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity)).String()
}

func hindsightOperationID(event MemoryEventEnvelope, documentID, operation string) string {
	raw, _ := json.Marshal(event)
	name := strings.Join([]string{operation, documentID, string(raw)}, "\x00")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
}

func hindsightExplicitDocumentID(event MemoryEventEnvelope, content hindsightEventContent) string {
	for _, value := range []string{
		content.DocumentID,
		hindsightMetadataString(event.Metadata, "document_id"),
	} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func hindsightMemoryID(event MemoryEventEnvelope, content hindsightEventContent) string {
	for _, value := range []string{
		content.MemoryID,
		hindsightMetadataString(event.Metadata, "memory_id"),
	} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func hindsightMetadata(event MemoryEventEnvelope) map[string]string {
	metadata := make(map[string]string, len(event.Metadata)+7)
	for key, value := range event.Metadata {
		if key == "" || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			metadata[key] = typed
		case bool:
			metadata[key] = strconv.FormatBool(typed)
		case float64:
			metadata[key] = strconv.FormatFloat(typed, 'g', -1, 64)
		default:
			if raw, err := json.Marshal(typed); err == nil {
				metadata[key] = string(raw)
			}
		}
	}
	metadata["workspace_id"] = event.Scope.WorkspaceID
	setIfNotEmpty(metadata, "project_id", event.Scope.ProjectID)
	setIfNotEmpty(metadata, "agent_id", event.Scope.AgentID)
	setIfNotEmpty(metadata, "issue_id", event.Scope.IssueID)
	setIfNotEmpty(metadata, "task_id", event.Scope.TaskID)
	metadata["event_type"] = event.EventType
	return metadata
}

func hindsightMetadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func setIfNotEmpty(values map[string]string, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		values[key] = value
	}
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		if counts[value] == 0 {
			return false
		}
		counts[value]--
	}
	return true
}

func optionalNonEmptyString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func optionalStringSlice(value []string) *[]string {
	if value == nil {
		return nil
	}
	return &value
}

type hindsightRetainRequest struct {
	Items       []hindsightMemoryItem `json:"items"`
	Async       bool                  `json:"async"`
	OperationID string                `json:"operation_id"`
}

type hindsightMemoryItem struct {
	Content           string            `json:"content"`
	Timestamp         string            `json:"timestamp,omitempty"`
	Context           *string           `json:"context,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	DocumentID        string            `json:"document_id"`
	Tags              []string          `json:"tags"`
	ObservationScopes string            `json:"observation_scopes"`
	UpdateMode        string            `json:"update_mode"`
}

type hindsightRecallRequest struct {
	Query     string                 `json:"query"`
	Budget    string                 `json:"budget"`
	MaxTokens int                    `json:"max_tokens"`
	Trace     bool                   `json:"trace"`
	Tags      []string               `json:"tags"`
	TagsMatch string                 `json:"tags_match"`
	Include   hindsightRecallInclude `json:"include"`
}

type hindsightRecallInclude struct {
	Entities any `json:"entities"`
}

type hindsightRecallResponse struct {
	Results []json.RawMessage `json:"results"`
}

type hindsightRecallResult struct {
	ID         string                 `json:"id"`
	DocumentID string                 `json:"document_id"`
	Tags       []string               `json:"tags"`
	Scores     *hindsightRecallScores `json:"scores"`
}

type hindsightRecallScores struct {
	Final float64 `json:"final"`
}

func (r hindsightRecallResult) finalScore() *float64 {
	if r.Scores == nil {
		return nil
	}
	score := r.Scores.Final
	return &score
}

type hindsightRecallProvenance struct {
	Provider  string                          `json:"provider"`
	ScopeTags []string                        `json:"scope_tags"`
	TagsMatch string                          `json:"tags_match"`
	MaxTokens int                             `json:"max_tokens"`
	Items     []hindsightRecallProvenanceItem `json:"items"`
}

type hindsightRecallProvenanceItem struct {
	ProviderRecordID string   `json:"provider_record_id"`
	DocumentID       string   `json:"document_id,omitempty"`
	Score            *float64 `json:"score,omitempty"`
	Rank             int      `json:"rank"`
}

type hindsightUpdateMemoryRequest struct {
	Text          *string   `json:"text,omitempty"`
	Context       *string   `json:"context,omitempty"`
	OccurredStart *string   `json:"occurred_start,omitempty"`
	OccurredEnd   *string   `json:"occurred_end,omitempty"`
	FactType      *string   `json:"fact_type,omitempty"`
	Entities      *[]string `json:"entities,omitempty"`
	State         string    `json:"state,omitempty"`
	Reason        *string   `json:"reason,omitempty"`
}

func (r hindsightUpdateMemoryRequest) empty() bool {
	return r.Text == nil &&
		r.Context == nil &&
		r.OccurredStart == nil &&
		r.OccurredEnd == nil &&
		r.FactType == nil &&
		r.Entities == nil &&
		r.State == "" &&
		r.Reason == nil
}

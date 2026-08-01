package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
)

const (
	Mem0ProviderName          = "mem0"
	mem0ScopeVersion          = 1
	defaultMem0RequestTimeout = 5 * time.Second
	defaultMem0MaxAttempts    = 3
	defaultMem0RetryBase      = 100 * time.Millisecond
	defaultMem0MaxResponse    = int64(2 << 20)
	defaultMem0MaxRequest     = int64(1 << 20)
	defaultMem0RecallTokens   = 1500
	hardMem0RecallTokens      = 3000
	defaultMem0MemoryTokens   = 220
	defaultMem0RecallResults  = 100
)

var (
	ErrMem0Config           = errors.New("mem0 provider config invalid")
	ErrMem0Scope            = errors.New("mem0 scope mismatch")
	ErrMem0ResponseTooLarge = errors.New("mem0 response exceeds configured limit")
	ErrMem0RequestTooLarge  = errors.New("mem0 request exceeds configured limit")
)

// Mem0ProviderConfig configures the authenticated Mem0 OSS REST adapter.
// APIKey is sent only through X-API-Key, which is the programmatic auth scheme
// supported by the self-hosted server. Platform /v1 endpoints are deliberately
// unsupported.
type Mem0ProviderConfig struct {
	BaseURL          string
	APIKey           string
	HTTPClient       *http.Client
	RequestTimeout   time.Duration
	MaxAttempts      int
	RetryBase        time.Duration
	MaxResponseBytes int64
	MaxRequestBytes  int64
	MaxRecallTokens  int
	MaxMemoryTokens  int
	MaxRecallResults int
	Clock            func() time.Time
	Sleep            func(context.Context, time.Duration) error
}

// Mem0Provider is a provider-neutral gateway adapter for the authenticated,
// self-hosted Mem0 OSS REST server.
type Mem0Provider struct {
	baseURL          *url.URL
	apiKey           string
	httpClient       *http.Client
	requestTimeout   time.Duration
	maxAttempts      int
	retryBase        time.Duration
	maxResponseBytes int64
	maxRequestBytes  int64
	maxRecallTokens  int
	maxMemoryTokens  int
	maxRecallResults int
	clock            func() time.Time
	sleep            func(context.Context, time.Duration) error

	retainMu sync.Mutex
	retained map[string]MemoryProviderResult
}

type Mem0ScopeMapping struct {
	WorkspaceID string         `json:"workspace_id"`
	UserID      string         `json:"user_id"`
	AgentID     string         `json:"agent_id,omitempty"`
	RunID       string         `json:"run_id,omitempty"`
	Filters     map[string]any `json:"filters"`
	Metadata    map[string]any `json:"metadata"`
}

type Mem0HTTPError struct {
	Operation  string
	StatusCode int
	RequestID  string
	Body       string
	Retryable  bool
}

func (e *Mem0HTTPError) Error() string {
	if e == nil {
		return ""
	}
	suffix := ""
	if e.Body != "" {
		suffix = ": " + e.Body
	}
	return fmt.Sprintf("mem0 %s returned HTTP %d%s", e.Operation, e.StatusCode, suffix)
}

type mem0TransportError struct {
	operation string
	err       error
}

func (e *mem0TransportError) Error() string {
	return fmt.Sprintf("mem0 %s transport failed: %v", e.operation, e.err)
}

func (e *mem0TransportError) Unwrap() error {
	return e.err
}

func NewMem0Provider(cfg Mem0ProviderConfig) (*Mem0Provider, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("%w: base URL is required", ErrMem0Config)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: parse base URL: %v", ErrMem0Config, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("%w: base URL must be an absolute http(s) URL", ErrMem0Config)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("%w: base URL must not contain a query or fragment", ErrMem0Config)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("%w: API key is required", ErrMem0Config)
	}

	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaultMem0RequestTimeout
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultMem0MaxAttempts
	}
	if cfg.RetryBase <= 0 {
		cfg.RetryBase = defaultMem0RetryBase
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = defaultMem0MaxResponse
	}
	if cfg.MaxRequestBytes <= 0 {
		cfg.MaxRequestBytes = defaultMem0MaxRequest
	}
	if cfg.MaxRecallTokens <= 0 {
		cfg.MaxRecallTokens = defaultMem0RecallTokens
	}
	if cfg.MaxRecallTokens > hardMem0RecallTokens {
		return nil, fmt.Errorf("%w: recall token budget %d exceeds hard maximum %d", ErrMem0Config, cfg.MaxRecallTokens, hardMem0RecallTokens)
	}
	if cfg.MaxMemoryTokens <= 0 {
		cfg.MaxMemoryTokens = defaultMem0MemoryTokens
	}
	if cfg.MaxMemoryTokens > cfg.MaxRecallTokens {
		cfg.MaxMemoryTokens = cfg.MaxRecallTokens
	}
	if cfg.MaxRecallResults <= 0 {
		cfg.MaxRecallResults = defaultMem0RecallResults
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}

	return &Mem0Provider{
		baseURL:          parsed,
		apiKey:           strings.TrimSpace(cfg.APIKey),
		httpClient:       cfg.HTTPClient,
		requestTimeout:   cfg.RequestTimeout,
		maxAttempts:      cfg.MaxAttempts,
		retryBase:        cfg.RetryBase,
		maxResponseBytes: cfg.MaxResponseBytes,
		maxRequestBytes:  cfg.MaxRequestBytes,
		maxRecallTokens:  cfg.MaxRecallTokens,
		maxMemoryTokens:  cfg.MaxMemoryTokens,
		maxRecallResults: cfg.MaxRecallResults,
		clock:            cfg.Clock,
		sleep:            cfg.Sleep,
		retained:         make(map[string]MemoryProviderResult),
	}, nil
}

func (p *Mem0Provider) Name() string {
	return Mem0ProviderName
}

// MapMem0Scope is the exact provider mapping:
//   - workspace_id -> user_id
//   - agent_id -> agent_id
//   - task_id -> run_id
//   - project_id and issue_id -> exact metadata filters
//
// Every native identifier is also duplicated in metadata. This lets the
// adapter post-validate provider responses instead of trusting Mem0 namespaces
// as the Multica authorization boundary.
func MapMem0Scope(scope MemoryScope) (Mem0ScopeMapping, error) {
	if !scope.WorkspaceID.Valid {
		return Mem0ScopeMapping{}, fmt.Errorf("%w: workspace_id is required", ErrMem0Config)
	}
	workspaceID := uuidString(scope.WorkspaceID)
	mapping := Mem0ScopeMapping{
		WorkspaceID: workspaceID,
		UserID:      "multica:workspace:" + workspaceID,
		Filters: map[string]any{
			"user_id":               "multica:workspace:" + workspaceID,
			"multica_scope_version": mem0ScopeVersion,
			"multica_workspace_id":  workspaceID,
		},
		Metadata: map[string]any{
			"multica_scope_version": mem0ScopeVersion,
			"multica_workspace_id":  workspaceID,
		},
	}
	addMetadataUUID(mapping.Filters, mapping.Metadata, "multica_project_id", scope.ProjectID)
	if scope.AgentID.Valid {
		agentID := uuidString(scope.AgentID)
		mapping.AgentID = "multica:agent:" + agentID
		mapping.Filters["agent_id"] = mapping.AgentID
		mapping.Metadata["multica_agent_id"] = agentID
		mapping.Filters["multica_agent_id"] = agentID
	}
	addMetadataUUID(mapping.Filters, mapping.Metadata, "multica_issue_id", scope.IssueID)
	if scope.TaskID.Valid {
		taskID := uuidString(scope.TaskID)
		mapping.RunID = "multica:task:" + taskID
		mapping.Filters["run_id"] = mapping.RunID
		mapping.Metadata["multica_task_id"] = taskID
		mapping.Filters["multica_task_id"] = taskID
	}
	return mapping, nil
}

func addMetadataUUID(filters, metadata map[string]any, key string, value pgtype.UUID) {
	if !value.Valid {
		return
	}
	v := uuidString(value)
	filters[key] = v
	metadata[key] = v
}

func (p *Mem0Provider) Retain(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	scope, err := memoryScopeFromEnvelope(event.Scope)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	mapping, err := MapMem0Scope(scope)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	messages, query, err := mem0Messages(event.Content)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return MemoryProviderResult{}, fmt.Errorf("marshal mem0 event: %w", err)
	}
	idempotencyKey := MemoryIdempotencyKey(eventJSON)
	metadata := cloneMap(mapping.Metadata)
	for key, value := range event.Metadata {
		if strings.HasPrefix(key, "multica_") {
			continue
		}
		metadata[key] = value
	}
	metadata["multica_idempotency_key"] = idempotencyKey
	metadata["multica_event_type"] = event.EventType
	metadata["multica_actor_type"] = event.Actor.Type

	payload := map[string]any{
		"messages": messages,
		"user_id":  mapping.UserID,
		"metadata": metadata,
	}
	if mapping.AgentID != "" {
		payload["agent_id"] = mapping.AgentID
	}
	if mapping.RunID != "" {
		payload["run_id"] = mapping.RunID
	}

	// Serialize retains per adapter and retain successful results in memory.
	// The provider-side metadata preflight below keeps the same property across
	// process restarts and resolves the ambiguous "server committed then client
	// timed out" case without blindly replaying POST /memories.
	p.retainMu.Lock()
	defer p.retainMu.Unlock()
	if result, ok := p.retained[idempotencyKey]; ok {
		return result, nil
	}

	var lastErr error
	for attempt := 1; attempt <= p.maxAttempts; attempt++ {
		existing, found, preflightErr := p.findRetained(ctx, mapping, query, idempotencyKey)
		if preflightErr != nil {
			lastErr = preflightErr
			if !IsMem0Retryable(preflightErr) || attempt == p.maxAttempts {
				return MemoryProviderResult{}, preflightErr
			}
			if err := p.waitRetry(ctx, attempt); err != nil {
				return MemoryProviderResult{}, err
			}
			continue
		}
		if found {
			p.retained[idempotencyKey] = existing
			return existing, nil
		}

		raw, _, requestErr := p.doOnce(ctx, "retain", http.MethodPost, "/memories", nil, payload, idempotencyKey, mapping.WorkspaceID)
		if requestErr == nil {
			memoryID := firstMemoryID(raw)
			result := MemoryProviderResult{ProviderMemoryID: memoryID, Response: raw}
			p.retained[idempotencyKey] = result
			return result, nil
		}
		lastErr = requestErr
		if !IsMem0Retryable(requestErr) || attempt == p.maxAttempts {
			return MemoryProviderResult{}, requestErr
		}
		if err := p.waitRetry(ctx, attempt); err != nil {
			return MemoryProviderResult{}, err
		}
	}
	return MemoryProviderResult{}, lastErr
}

func (p *Mem0Provider) findRetained(ctx context.Context, mapping Mem0ScopeMapping, query, idempotencyKey string) (MemoryProviderResult, bool, error) {
	filters := cloneMap(mapping.Filters)
	filters["multica_idempotency_key"] = idempotencyKey
	payload := map[string]any{
		"query":   query,
		"filters": filters,
		"top_k":   1,
	}
	raw, _, err := p.doOnce(ctx, "retain idempotency preflight", http.MethodPost, "/search", nil, payload, idempotencyKey, mapping.WorkspaceID)
	if err != nil {
		return MemoryProviderResult{}, false, err
	}
	memories, err := decodeMem0Memories(raw)
	if err != nil {
		return MemoryProviderResult{}, false, err
	}
	for _, memory := range memories {
		if err := validateMem0MemoryScope(memory, mapping); err != nil {
			continue
		}
		if stringValue(memory.Metadata["multica_idempotency_key"]) != idempotencyKey {
			continue
		}
		return MemoryProviderResult{ProviderMemoryID: memory.ID, Response: raw}, true, nil
	}
	return MemoryProviderResult{}, false, nil
}

func (p *Mem0Provider) Recall(ctx context.Context, req MemoryRecallRequest) (MemoryRecallResult, error) {
	mapping, err := MapMem0Scope(req.Scope)
	if err != nil {
		return MemoryRecallResult{}, err
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return MemoryRecallResult{}, fmt.Errorf("%w: recall query is required", ErrMem0Config)
	}
	limit := int(req.Limit)
	if limit <= 0 || limit > p.maxRecallResults {
		limit = p.maxRecallResults
	}
	payload := map[string]any{
		"query":   query,
		"filters": mapping.Filters,
		"top_k":   limit,
		"explain": true,
	}
	raw, requestID, err := p.do(ctx, "recall", http.MethodPost, "/search", nil, payload, "", mapping.WorkspaceID)
	if err != nil {
		return MemoryRecallResult{}, err
	}
	memories, err := decodeMem0Memories(raw)
	if err != nil {
		return MemoryRecallResult{}, err
	}
	sort.SliceStable(memories, func(i, j int) bool {
		return memories[i].Score > memories[j].Score
	})

	results := make([]map[string]any, 0, len(memories))
	provenanceItems := make([]map[string]any, 0, len(memories))
	rejected := 0
	truncated := 0
	resultsJSON, err := json.Marshal(results)
	if err != nil {
		return MemoryRecallResult{}, err
	}
	usedTokens := approximateTokens(string(resultsJSON))
	recalledAt := p.clock().UTC().Format(time.RFC3339Nano)
	for _, memory := range memories {
		if err := validateMem0MemoryScope(memory, mapping); err != nil {
			rejected++
			continue
		}
		text, textTruncated := truncateApproxTokens(memory.Memory, p.maxMemoryTokens)
		if textTruncated {
			truncated++
		}
		scopeJSON := mem0ScopeProvenance(memory, mapping)
		// Results are prompt-injected. Keep this projection strict and budget
		// its final serialized form; provider metadata belongs in provenance.
		result := map[string]any{
			"provider":           Mem0ProviderName,
			"provider_record_id": memory.ID,
			"text":               text,
			"score":              memory.Score,
		}
		candidateResults := append(results, result)
		candidateJSON, err := json.Marshal(candidateResults)
		if err != nil {
			return MemoryRecallResult{}, err
		}
		candidateTokens := approximateTokens(string(candidateJSON))
		if candidateTokens > p.maxRecallTokens {
			truncated++
			continue
		}
		results = candidateResults
		resultsJSON = candidateJSON
		usedTokens = candidateTokens
		provenanceItems = append(provenanceItems, map[string]any{
			"provider":           Mem0ProviderName,
			"provider_record_id": memory.ID,
			"score":              memory.Score,
			"scope":              scopeJSON,
			"source":             mem0SourceProvenance(memory.Metadata),
			"recalled_at":        recalledAt,
		})
	}
	provenanceJSON, err := json.Marshal(map[string]any{
		"provider":                     Mem0ProviderName,
		"request_id":                   requestID,
		"items":                        provenanceItems,
		"provider_results_rejected":    rejected,
		"results_truncated":            truncated,
		"estimated_injected_tokens":    usedTokens,
		"configured_token_budget":      p.maxRecallTokens,
		"configured_per_memory_tokens": p.maxMemoryTokens,
	})
	if err != nil {
		return MemoryRecallResult{}, err
	}
	return MemoryRecallResult{
		Provider:   Mem0ProviderName,
		Results:    resultsJSON,
		Provenance: provenanceJSON,
	}, nil
}

func (p *Mem0Provider) Get(ctx context.Context, scope MemoryScope, memoryID string) (json.RawMessage, error) {
	mapping, err := MapMem0Scope(scope)
	if err != nil {
		return nil, err
	}
	memory, raw, err := p.getScopedMemory(ctx, mapping, memoryID)
	if err != nil {
		return nil, err
	}
	normalized, err := json.Marshal(memory.normalized())
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return raw, nil
	}
	return normalized, nil
}

func (p *Mem0Provider) History(ctx context.Context, scope MemoryScope, memoryID string) (json.RawMessage, error) {
	mapping, err := MapMem0Scope(scope)
	if err != nil {
		return nil, err
	}
	if _, _, err := p.getScopedMemory(ctx, mapping, memoryID); err != nil {
		return nil, err
	}
	raw, _, err := p.do(ctx, "history", http.MethodGet, mem0MemoryPath(memoryID)+"/history", nil, nil, "", mapping.WorkspaceID)
	return raw, err
}

func (p *Mem0Provider) Update(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	scope, err := memoryScopeFromEnvelope(event.Scope)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	mapping, err := MapMem0Scope(scope)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	memoryID, text, metadata, err := mem0Mutation(event)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	current, _, err := p.getScopedMemory(ctx, mapping, memoryID)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	payload := map[string]any{}
	if text != nil {
		payload["text"] = *text
	}
	if metadata != nil {
		merged := cloneMap(current.Metadata)
		for key, value := range metadata {
			if isProtectedMem0Metadata(key) {
				continue
			}
			merged[key] = value
		}
		payload["metadata"] = merged
	}
	if len(payload) == 0 {
		return MemoryProviderResult{}, fmt.Errorf("%w: update requires text or metadata", ErrMem0Config)
	}
	raw, _, err := p.do(ctx, "update", http.MethodPut, mem0MemoryPath(memoryID), nil, payload, "", mapping.WorkspaceID)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	return MemoryProviderResult{ProviderMemoryID: memoryID, Response: raw}, nil
}

func (p *Mem0Provider) Invalidate(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	scope, err := memoryScopeFromEnvelope(event.Scope)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	mapping, err := MapMem0Scope(scope)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	memoryID, _, metadata, err := mem0Mutation(event)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	current, _, err := p.getScopedMemory(ctx, mapping, memoryID)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	merged := cloneMap(current.Metadata)
	for key, value := range metadata {
		if !isProtectedMem0Metadata(key) {
			merged[key] = value
		}
	}
	merged["multica_invalidated"] = true
	merged["multica_invalidated_at"] = p.clock().UTC().Format(time.RFC3339Nano)
	raw, _, err := p.do(ctx, "invalidate", http.MethodPut, mem0MemoryPath(memoryID), nil, map[string]any{"metadata": merged}, "", mapping.WorkspaceID)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	return MemoryProviderResult{ProviderMemoryID: memoryID, Response: raw}, nil
}

func (p *Mem0Provider) Delete(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	scope, err := memoryScopeFromEnvelope(event.Scope)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	mapping, err := MapMem0Scope(scope)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	memoryID, _, _, err := mem0Mutation(event)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	if _, _, err := p.getScopedMemory(ctx, mapping, memoryID); err != nil {
		return MemoryProviderResult{}, err
	}
	raw, _, err := p.do(ctx, "delete", http.MethodDelete, mem0MemoryPath(memoryID), nil, nil, "", mapping.WorkspaceID)
	if err != nil {
		return MemoryProviderResult{}, err
	}
	return MemoryProviderResult{ProviderMemoryID: memoryID, Response: raw}, nil
}

func (p *Mem0Provider) Health(ctx context.Context) (MemoryProviderHealth, error) {
	_, requestID, err := p.do(ctx, "health", http.MethodGet, "/configure", nil, nil, "", "")
	health := MemoryProviderHealth{
		Provider: Mem0ProviderName,
		OK:       err == nil,
		Details:  map[string]any{"authenticated": true},
	}
	if requestID != "" {
		health.Details["request_id"] = requestID
	}
	return health, err
}

func (p *Mem0Provider) getScopedMemory(ctx context.Context, mapping Mem0ScopeMapping, memoryID string) (mem0Memory, json.RawMessage, error) {
	if strings.TrimSpace(memoryID) == "" {
		return mem0Memory{}, nil, fmt.Errorf("%w: provider memory ID is required", ErrMem0Config)
	}
	raw, _, err := p.do(ctx, "get", http.MethodGet, mem0MemoryPath(memoryID), nil, nil, "", mapping.WorkspaceID)
	if err != nil {
		return mem0Memory{}, nil, err
	}
	memory, err := decodeMem0Memory(raw)
	if err != nil {
		return mem0Memory{}, nil, err
	}
	if err := validateMem0MemoryScope(memory, mapping); err != nil {
		return mem0Memory{}, nil, err
	}
	return memory, raw, nil
}

func (p *Mem0Provider) do(ctx context.Context, operation, method, requestPath string, query url.Values, body any, idempotencyKey, workspaceID string) (json.RawMessage, string, error) {
	var lastErr error
	for attempt := 1; attempt <= p.maxAttempts; attempt++ {
		raw, requestID, err := p.doOnce(ctx, operation, method, requestPath, query, body, idempotencyKey, workspaceID)
		if err == nil {
			return raw, requestID, nil
		}
		lastErr = err
		if !IsMem0Retryable(err) || attempt == p.maxAttempts {
			return nil, requestID, err
		}
		if err := p.waitRetry(ctx, attempt); err != nil {
			return nil, requestID, err
		}
	}
	return nil, "", lastErr
}

func (p *Mem0Provider) doOnce(ctx context.Context, operation, method, requestPath string, query url.Values, body any, idempotencyKey, workspaceID string) (json.RawMessage, string, error) {
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, "", fmt.Errorf("marshal mem0 %s request: %w", operation, err)
		}
		if int64(len(encoded)) > p.maxRequestBytes {
			return nil, "", fmt.Errorf("%w: %s body is %d bytes (limit %d)", ErrMem0RequestTooLarge, operation, len(encoded), p.maxRequestBytes)
		}
		requestBody = bytes.NewReader(encoded)
	}
	endpoint := *p.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + requestPath
	endpoint.RawQuery = query.Encode()

	attemptCtx, cancel := context.WithTimeout(ctx, p.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(attemptCtx, method, endpoint.String(), requestBody)
	if err != nil {
		return nil, "", fmt.Errorf("build mem0 %s request: %w", operation, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-API-Key", p.apiKey)
	if workspaceID != "" {
		request.Header.Set("X-Workspace-ID", workspaceID)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}

	response, err := p.httpClient.Do(request)
	if err != nil {
		return nil, "", &mem0TransportError{operation: operation, err: err}
	}
	defer response.Body.Close()
	requestID := response.Header.Get("X-Request-ID")
	raw, err := readBounded(response.Body, p.maxResponseBytes)
	if err != nil {
		return nil, requestID, fmt.Errorf("%w: %s", err, operation)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, requestID, &Mem0HTTPError{
			Operation:  operation,
			StatusCode: response.StatusCode,
			RequestID:  requestID,
			Body:       compactErrorBody(raw),
			Retryable:  mem0RetryableStatus(response.StatusCode),
		}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if !json.Valid(raw) {
		return nil, requestID, fmt.Errorf("mem0 %s returned invalid JSON", operation)
	}
	return raw, requestID, nil
}

func (p *Mem0Provider) waitRetry(ctx context.Context, attempt int) error {
	shift := attempt - 1
	if shift > 5 {
		shift = 5
	}
	delay := p.retryBase * time.Duration(1<<shift)
	return p.sleep(ctx, delay)
}

func IsMem0Retryable(err error) bool {
	var httpErr *Mem0HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Retryable
	}
	var transportErr *mem0TransportError
	if errors.As(err, &transportErr) {
		if errors.Is(transportErr, context.Canceled) {
			return false
		}
		return true
	}
	return false
}

func mem0RetryableStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError
}

func readBounded(reader io.Reader, maxBytes int64) (json.RawMessage, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, ErrMem0ResponseTooLarge
	}
	return raw, nil
}

func compactErrorBody(raw []byte) string {
	value := strings.TrimSpace(string(raw))
	if len(value) > 512 {
		value = value[:512] + "..."
	}
	return value
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func memoryScopeFromEnvelope(scope memoryScopeJSON) (MemoryScope, error) {
	workspaceID, err := parseOptionalMemoryUUID(scope.WorkspaceID, true, "workspace_id")
	if err != nil {
		return MemoryScope{}, err
	}
	projectID, err := parseOptionalMemoryUUID(scope.ProjectID, false, "project_id")
	if err != nil {
		return MemoryScope{}, err
	}
	agentID, err := parseOptionalMemoryUUID(scope.AgentID, false, "agent_id")
	if err != nil {
		return MemoryScope{}, err
	}
	issueID, err := parseOptionalMemoryUUID(scope.IssueID, false, "issue_id")
	if err != nil {
		return MemoryScope{}, err
	}
	taskID, err := parseOptionalMemoryUUID(scope.TaskID, false, "task_id")
	if err != nil {
		return MemoryScope{}, err
	}
	return MemoryScope{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		AgentID:     agentID,
		IssueID:     issueID,
		TaskID:      taskID,
	}, nil
}

func parseOptionalMemoryUUID(value string, required bool, field string) (pgtype.UUID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		if required {
			return pgtype.UUID{}, fmt.Errorf("%w: %s is required", ErrMem0Config, field)
		}
		return pgtype.UUID{}, nil
	}
	parsed, err := util.ParseUUID(value)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("%w: invalid %s", ErrMem0Config, field)
	}
	return parsed, nil
}

func mem0Messages(content json.RawMessage) ([]map[string]string, string, error) {
	if len(content) == 0 || !json.Valid(content) {
		return nil, "", fmt.Errorf("%w: retain content must be valid JSON", ErrMem0Config)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(content, &object); err == nil {
		if rawMessages, ok := object["messages"]; ok {
			var messages []map[string]string
			if err := json.Unmarshal(rawMessages, &messages); err != nil || len(messages) == 0 {
				return nil, "", fmt.Errorf("%w: messages must be a non-empty role/content list", ErrMem0Config)
			}
			var queryParts []string
			for _, message := range messages {
				role := strings.TrimSpace(message["role"])
				text := strings.TrimSpace(message["content"])
				if role == "" || text == "" {
					return nil, "", fmt.Errorf("%w: every message requires role and content", ErrMem0Config)
				}
				queryParts = append(queryParts, text)
			}
			return messages, strings.Join(queryParts, "\n"), nil
		}
		for _, field := range []string{"text", "summary"} {
			if rawText, ok := object[field]; ok {
				var text string
				if json.Unmarshal(rawText, &text) == nil && strings.TrimSpace(text) != "" {
					text = strings.TrimSpace(text)
					return []map[string]string{{"role": "user", "content": text}}, text, nil
				}
			}
		}
	}
	text := strings.TrimSpace(string(content))
	return []map[string]string{{"role": "user", "content": text}}, text, nil
}

func mem0Mutation(event MemoryEventEnvelope) (string, *string, map[string]any, error) {
	var content struct {
		MemoryID         string         `json:"memory_id"`
		ProviderMemoryID string         `json:"provider_memory_id"`
		Text             *string        `json:"text"`
		Metadata         map[string]any `json:"metadata"`
	}
	if len(event.Content) > 0 {
		if err := json.Unmarshal(event.Content, &content); err != nil {
			return "", nil, nil, fmt.Errorf("%w: mutation content must be an object", ErrMem0Config)
		}
	}
	memoryID := strings.TrimSpace(content.MemoryID)
	if memoryID == "" {
		memoryID = strings.TrimSpace(content.ProviderMemoryID)
	}
	if memoryID == "" {
		memoryID = stringValue(event.Metadata["provider_memory_id"])
	}
	if memoryID == "" {
		return "", nil, nil, fmt.Errorf("%w: provider memory ID is required", ErrMem0Config)
	}
	return memoryID, content.Text, content.Metadata, nil
}

func isProtectedMem0Metadata(key string) bool {
	return strings.HasPrefix(key, "multica_")
}

func mem0MemoryPath(memoryID string) string {
	return path.Join("/memories", url.PathEscape(strings.TrimSpace(memoryID)))
}

type mem0Memory struct {
	ID        string
	Memory    string
	UserID    string
	AgentID   string
	RunID     string
	Score     float64
	Metadata  map[string]any
	CreatedAt any
	UpdatedAt any
	Raw       map[string]any
}

func (m mem0Memory) normalized() map[string]any {
	return map[string]any{
		"id":         m.ID,
		"memory":     m.Memory,
		"user_id":    m.UserID,
		"agent_id":   m.AgentID,
		"run_id":     m.RunID,
		"score":      m.Score,
		"metadata":   m.Metadata,
		"created_at": m.CreatedAt,
		"updated_at": m.UpdatedAt,
	}
}

func decodeMem0Memory(raw json.RawMessage) (mem0Memory, error) {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return mem0Memory{}, fmt.Errorf("decode mem0 memory: %w", err)
	}
	return mem0MemoryFromMap(value), nil
}

func decodeMem0Memories(raw json.RawMessage) ([]mem0Memory, error) {
	var envelope struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Results != nil {
		memories := make([]mem0Memory, 0, len(envelope.Results))
		for _, result := range envelope.Results {
			memories = append(memories, mem0MemoryFromMap(result))
		}
		return memories, nil
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("decode mem0 results: %w", err)
	}
	memories := make([]mem0Memory, 0, len(list))
	for _, result := range list {
		memories = append(memories, mem0MemoryFromMap(result))
	}
	return memories, nil
}

func mem0MemoryFromMap(value map[string]any) mem0Memory {
	metadata, _ := value["metadata"].(map[string]any)
	metadata = cloneMap(metadata)
	for key, item := range value {
		if strings.HasPrefix(key, "multica_") {
			metadata[key] = item
		}
	}
	memory := stringValue(value["memory"])
	if memory == "" {
		memory = stringValue(value["data"])
	}
	return mem0Memory{
		ID:        stringValue(value["id"]),
		Memory:    memory,
		UserID:    stringValue(value["user_id"]),
		AgentID:   stringValue(value["agent_id"]),
		RunID:     stringValue(value["run_id"]),
		Score:     floatValue(value["score"]),
		Metadata:  metadata,
		CreatedAt: value["created_at"],
		UpdatedAt: value["updated_at"],
		Raw:       value,
	}
}

func validateMem0MemoryScope(memory mem0Memory, expected Mem0ScopeMapping) error {
	if memory.UserID != expected.UserID {
		return fmt.Errorf("%w: workspace user_id", ErrMem0Scope)
	}
	if stringValue(memory.Metadata["multica_workspace_id"]) != stringValue(expected.Metadata["multica_workspace_id"]) {
		return fmt.Errorf("%w: workspace metadata", ErrMem0Scope)
	}
	if expected.AgentID != "" && memory.AgentID != expected.AgentID {
		return fmt.Errorf("%w: agent_id", ErrMem0Scope)
	}
	if expected.RunID != "" && memory.RunID != expected.RunID {
		return fmt.Errorf("%w: run_id", ErrMem0Scope)
	}
	for _, key := range []string{"multica_project_id", "multica_agent_id", "multica_issue_id", "multica_task_id"} {
		expectedValue, ok := expected.Metadata[key]
		if !ok {
			continue
		}
		if stringValue(memory.Metadata[key]) != stringValue(expectedValue) {
			return fmt.Errorf("%w: %s", ErrMem0Scope, key)
		}
	}
	return nil
}

func mem0ScopeProvenance(memory mem0Memory, mapping Mem0ScopeMapping) map[string]any {
	return map[string]any{
		"workspace_id": mapping.Metadata["multica_workspace_id"],
		"project_id":   memory.Metadata["multica_project_id"],
		"agent_id":     memory.Metadata["multica_agent_id"],
		"issue_id":     memory.Metadata["multica_issue_id"],
		"task_id":      memory.Metadata["multica_task_id"],
	}
}

func mem0SourceProvenance(metadata map[string]any) map[string]any {
	return map[string]any{
		"source_type":    stringValue(metadata["source_type"]),
		"source_id":      stringValue(metadata["source_id"]),
		"source_version": stringValue(metadata["source_version"]),
	}
}

func firstMemoryID(raw json.RawMessage) string {
	memories, err := decodeMem0Memories(raw)
	if err == nil && len(memories) > 0 {
		return memories[0].ID
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) == nil {
		return stringValue(value["id"])
	}
	return ""
}

func cloneMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func floatValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case json.Number:
		result, _ := typed.Float64()
		return result
	default:
		return 0
	}
}

func approximateTokens(text string) int {
	if text == "" {
		return 0
	}
	return int(math.Ceil(float64(len([]rune(text))) / 4.0))
}

func truncateApproxTokens(text string, maxTokens int) (string, bool) {
	runes := []rune(text)
	maxRunes := maxTokens * 4
	if len(runes) <= maxRunes {
		return text, false
	}
	if maxRunes <= 1 {
		return "…", true
	}
	return string(runes[:maxRunes-1]) + "…", true
}

var _ MemoryProvider = (*Mem0Provider)(nil)

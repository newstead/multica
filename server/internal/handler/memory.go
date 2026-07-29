package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type MemoryConfigResponse struct {
	WorkspaceID      string         `json:"workspace_id"`
	Enabled          bool           `json:"enabled"`
	PrimaryProvider  string         `json:"primary_provider"`
	ShadowProvider   *string        `json:"shadow_provider,omitempty"`
	ReadMode         string         `json:"read_mode"`
	ProviderSettings map[string]any `json:"provider_settings"`
	CreatedAt        string         `json:"created_at,omitempty"`
	UpdatedAt        string         `json:"updated_at,omitempty"`
}

type UpdateMemoryConfigRequest struct {
	Enabled          bool           `json:"enabled"`
	PrimaryProvider  string         `json:"primary_provider"`
	ShadowProvider   *string        `json:"shadow_provider"`
	ReadMode         string         `json:"read_mode"`
	ProviderSettings map[string]any `json:"provider_settings"`
}

type MemoryRetainEventRequest struct {
	EventType      string          `json:"event_type"`
	IdempotencyKey string          `json:"idempotency_key"`
	CorrelationID  string          `json:"correlation_id"`
	SourceID       string          `json:"source_id"`
	ProjectID      string          `json:"project_id"`
	AgentID        string          `json:"agent_id"`
	IssueID        string          `json:"issue_id"`
	TaskID         string          `json:"task_id"`
	ActorType      string          `json:"actor_type"`
	ActorID        string          `json:"actor_id"`
	Content        json.RawMessage `json:"content"`
	Metadata       map[string]any  `json:"metadata"`
}

type MemoryRecallRequestBody struct {
	Provider      string `json:"provider"`
	ReadMode      string `json:"read_mode"`
	CorrelationID string `json:"correlation_id"`
	ProjectID     string `json:"project_id"`
	AgentID       string `json:"agent_id"`
	IssueID       string `json:"issue_id"`
	TaskID        string `json:"task_id"`
	Query         string `json:"query"`
	Limit         int32  `json:"limit"`
}

type MemoryEventResponse struct {
	ID             string         `json:"id"`
	WorkspaceID    string         `json:"workspace_id"`
	ProjectID      *string        `json:"project_id,omitempty"`
	AgentID        *string        `json:"agent_id,omitempty"`
	IssueID        *string        `json:"issue_id,omitempty"`
	TaskID         *string        `json:"task_id,omitempty"`
	ActorType      string         `json:"actor_type"`
	ActorID        *string        `json:"actor_id,omitempty"`
	EventType      string         `json:"event_type"`
	IdempotencyKey string         `json:"idempotency_key"`
	Status         string         `json:"status"`
	Envelope       map[string]any `json:"envelope"`
	CreatedAt      string         `json:"created_at"`
}

type MemoryDeliveryResponse struct {
	ID            string `json:"id"`
	WorkspaceID   string `json:"workspace_id"`
	MemoryEventID string `json:"memory_event_id"`
	Provider      string `json:"provider"`
	Status        string `json:"status"`
	AttemptCount  int32  `json:"attempt_count"`
	DeliveryLagMs int64  `json:"delivery_lag_ms"`
	NextAttemptAt string `json:"next_attempt_at,omitempty"`
}

type MemoryRecallSampleResponse struct {
	ID                  string         `json:"id"`
	WorkspaceID         string         `json:"workspace_id"`
	ProjectID           *string        `json:"project_id,omitempty"`
	AgentID             *string        `json:"agent_id,omitempty"`
	IssueID             *string        `json:"issue_id,omitempty"`
	TaskID              *string        `json:"task_id,omitempty"`
	Provider            string         `json:"provider"`
	ReadMode            string         `json:"read_mode"`
	RecallCorrelationID string         `json:"recall_correlation_id"`
	Query               string         `json:"query"`
	Results             []any          `json:"results"`
	Provenance          map[string]any `json:"provenance"`
	SampledAt           string         `json:"sampled_at"`
}

type MemoryRecallProviderResponse struct {
	Provider   string                     `json:"provider"`
	Results    []any                      `json:"results"`
	Provenance map[string]any             `json:"provenance"`
	Sample     MemoryRecallSampleResponse `json:"sample"`
}

type MemoryRecallResponse struct {
	Mode                string                        `json:"mode"`
	RecallCorrelationID string                        `json:"recall_correlation_id"`
	Primary             *MemoryRecallProviderResponse `json:"primary,omitempty"`
	Shadow              *MemoryRecallProviderResponse `json:"shadow,omitempty"`
	Errors              map[string]string             `json:"errors,omitempty"`
}

func (h *Handler) memoryGatewayEnabled(r *http.Request) bool {
	return featureflags.MemoryGatewayEnabled(r.Context(), h.FeatureFlags)
}

func (h *Handler) GetMemoryConfig(w http.ResponseWriter, r *http.Request) {
	if !h.memoryGatewayEnabled(r) {
		writeError(w, http.StatusNotFound, "memory gateway not found")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	cfg, err := h.Queries.GetMemoryWorkspaceConfig(r.Context(), wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, MemoryConfigResponse{WorkspaceID: uuidToString(wsUUID), Enabled: false, ReadMode: "primary", ProviderSettings: map[string]any{}})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load memory config")
		return
	}
	writeJSON(w, http.StatusOK, memoryConfigToResponse(cfg))
}

func (h *Handler) UpdateMemoryConfig(w http.ResponseWriter, r *http.Request) {
	if !h.memoryGatewayEnabled(r) {
		writeError(w, http.StatusNotFound, "memory gateway not found")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	var req UpdateMemoryConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	readMode := strings.TrimSpace(req.ReadMode)
	if readMode == "" {
		readMode = "primary"
	}
	if readMode != "primary" && readMode != "shadow" && readMode != "dual" {
		writeError(w, http.StatusBadRequest, "invalid read_mode")
		return
	}
	primary := strings.TrimSpace(req.PrimaryProvider)
	if req.Enabled && primary == "" {
		writeError(w, http.StatusBadRequest, "primary_provider is required when memory is enabled")
		return
	}
	settings := req.ProviderSettings
	if settings == nil {
		settings = map[string]any{}
	}
	settingsRaw, err := json.Marshal(settings)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid provider_settings")
		return
	}
	credentials := []byte(`{}`)
	if existing, err := h.Queries.GetMemoryWorkspaceConfig(r.Context(), wsUUID); err == nil && len(existing.ProviderCredentialsEncrypted) > 0 {
		credentials = existing.ProviderCredentialsEncrypted
	}
	cfg, err := h.Queries.UpsertMemoryWorkspaceConfig(r.Context(), db.UpsertMemoryWorkspaceConfigParams{
		WorkspaceID:                  wsUUID,
		Enabled:                      req.Enabled,
		PrimaryProvider:              primary,
		ShadowProvider:               ptrToText(req.ShadowProvider),
		ReadMode:                     readMode,
		ProviderSettings:             settingsRaw,
		ProviderCredentialsEncrypted: credentials,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update memory config")
		return
	}
	writeJSON(w, http.StatusOK, memoryConfigToResponse(cfg))
}

func (h *Handler) CreateMemoryRetainEvent(w http.ResponseWriter, r *http.Request) {
	if !h.memoryGatewayEnabled(r) {
		writeError(w, http.StatusNotFound, "memory gateway not found")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	var req MemoryRetainEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	scope, ok := memoryScopeFromRequest(w, wsUUID, req)
	if !ok {
		return
	}
	actorID, ok := optionalUUIDOrBadRequest(w, req.ActorID, "actor_id")
	if !ok {
		return
	}
	res, err := h.MemoryService.Retain(r.Context(), service.MemoryRetainRequest{
		Scope:          scope,
		Actor:          service.MemoryActor{Type: req.ActorType, ID: actorID},
		EventType:      strings.TrimSpace(req.EventType),
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
		CorrelationID:  strings.TrimSpace(req.CorrelationID),
		SourceID:       strings.TrimSpace(req.SourceID),
		Content:        req.Content,
		Metadata:       req.Metadata,
	})
	if err != nil {
		if errors.Is(err, service.ErrMemoryDisabled) {
			writeError(w, http.StatusConflict, "memory gateway is disabled for this workspace")
			return
		}
		if errors.Is(err, service.ErrMemoryConfig) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create memory event")
		return
	}
	deliveries := make([]MemoryDeliveryResponse, 0, len(res.Deliveries))
	for _, delivery := range res.Deliveries {
		deliveries = append(deliveries, memoryDeliveryToResponse(delivery))
	}
	status := http.StatusCreated
	if !res.Inserted {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"event":      memoryEventToResponse(res.Event),
		"inserted":   res.Inserted,
		"deliveries": deliveries,
	})
}

func (h *Handler) CreateMemoryRecall(w http.ResponseWriter, r *http.Request) {
	if !h.memoryGatewayEnabled(r) {
		writeError(w, http.StatusNotFound, "memory gateway not found")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	var req MemoryRecallRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	scope, ok := memoryScopeFromRecallRequest(w, wsUUID, req)
	if !ok {
		return
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	res, err := h.MemoryService.Recall(r.Context(), service.MemoryRecallRequest{
		Scope:         scope,
		Provider:      strings.TrimSpace(req.Provider),
		ReadMode:      strings.TrimSpace(req.ReadMode),
		CorrelationID: strings.TrimSpace(req.CorrelationID),
		Query:         strings.TrimSpace(req.Query),
		Limit:         limit,
	})
	if err != nil {
		if errors.Is(err, service.ErrMemoryDisabled) {
			writeError(w, http.StatusConflict, "memory gateway is disabled for this workspace")
			return
		}
		if errors.Is(err, service.ErrMemoryConfig) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to recall memory")
		return
	}
	writeJSON(w, http.StatusOK, memoryRecallToResponse(res))
}

func (h *Handler) ListMemoryRecallSamples(w http.ResponseWriter, r *http.Request) {
	if !h.memoryGatewayEnabled(r) {
		writeError(w, http.StatusNotFound, "memory gateway not found")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	limit, offset := parseMemoryLimitOffset(r, 100, 500)
	rows, err := h.Queries.ListMemoryRecallSamplesByWorkspace(r.Context(), db.ListMemoryRecallSamplesByWorkspaceParams{
		WorkspaceID: wsUUID,
		Limit:       int32(limit),
		Offset:      int32(offset),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list memory recall samples")
		return
	}
	out := make([]MemoryRecallSampleResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, memoryRecallSampleToResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"samples": out})
}

func memoryScopeFromRecallRequest(w http.ResponseWriter, workspaceID pgtype.UUID, req MemoryRecallRequestBody) (service.MemoryScope, bool) {
	projectID, ok := optionalUUIDOrBadRequest(w, req.ProjectID, "project_id")
	if !ok {
		return service.MemoryScope{}, false
	}
	agentID, ok := optionalUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return service.MemoryScope{}, false
	}
	issueID, ok := optionalUUIDOrBadRequest(w, req.IssueID, "issue_id")
	if !ok {
		return service.MemoryScope{}, false
	}
	taskID, ok := optionalUUIDOrBadRequest(w, req.TaskID, "task_id")
	if !ok {
		return service.MemoryScope{}, false
	}
	return service.MemoryScope{WorkspaceID: workspaceID, ProjectID: projectID, AgentID: agentID, IssueID: issueID, TaskID: taskID}, true
}

func memoryScopeFromRequest(w http.ResponseWriter, workspaceID pgtype.UUID, req MemoryRetainEventRequest) (service.MemoryScope, bool) {
	projectID, ok := optionalUUIDOrBadRequest(w, req.ProjectID, "project_id")
	if !ok {
		return service.MemoryScope{}, false
	}
	agentID, ok := optionalUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return service.MemoryScope{}, false
	}
	issueID, ok := optionalUUIDOrBadRequest(w, req.IssueID, "issue_id")
	if !ok {
		return service.MemoryScope{}, false
	}
	taskID, ok := optionalUUIDOrBadRequest(w, req.TaskID, "task_id")
	if !ok {
		return service.MemoryScope{}, false
	}
	return service.MemoryScope{WorkspaceID: workspaceID, ProjectID: projectID, AgentID: agentID, IssueID: issueID, TaskID: taskID}, true
}

func optionalUUIDOrBadRequest(w http.ResponseWriter, raw, field string) (pgtype.UUID, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return pgtype.UUID{}, true
	}
	return parseUUIDOrBadRequest(w, raw, field)
}

func parseMemoryLimitOffset(r *http.Request, defaultLimit, maxLimit int) (int, int) {
	limit := defaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			offset = n
		}
	}
	return limit, offset
}

func memoryRecallToResponse(result service.MemoryRecallReadResult) MemoryRecallResponse {
	return MemoryRecallResponse{
		Mode:                result.Mode,
		RecallCorrelationID: result.CorrelationID,
		Primary:             memoryProviderRecallToResponse(result.Primary),
		Shadow:              memoryProviderRecallToResponse(result.Shadow),
		Errors:              result.Errors,
	}
}

func memoryProviderRecallToResponse(recall *service.MemoryProviderRecall) *MemoryRecallProviderResponse {
	if recall == nil {
		return nil
	}
	return &MemoryRecallProviderResponse{
		Provider:   recall.Provider,
		Results:    parseMemoryResults(recall.Result.Results),
		Provenance: parseMemoryProvenance(recall.Result.Provenance),
		Sample:     memoryRecallSampleToResponse(recall.Sample),
	}
}

func parseMemoryResults(raw json.RawMessage) []any {
	var results []any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &results)
	}
	if results == nil {
		results = []any{}
	}
	return results
}

func parseMemoryProvenance(raw json.RawMessage) map[string]any {
	var provenance map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &provenance)
	}
	if provenance == nil {
		provenance = map[string]any{}
	}
	return provenance
}

func memoryConfigToResponse(cfg db.MemoryWorkspaceConfig) MemoryConfigResponse {
	var settings map[string]any
	if len(cfg.ProviderSettings) > 0 {
		_ = json.Unmarshal(cfg.ProviderSettings, &settings)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return MemoryConfigResponse{
		WorkspaceID:      uuidToString(cfg.WorkspaceID),
		Enabled:          cfg.Enabled,
		PrimaryProvider:  cfg.PrimaryProvider,
		ShadowProvider:   textToPtr(cfg.ShadowProvider),
		ReadMode:         cfg.ReadMode,
		ProviderSettings: settings,
		CreatedAt:        timestampToString(cfg.CreatedAt),
		UpdatedAt:        timestampToString(cfg.UpdatedAt),
	}
}

func memoryEventToResponse(event db.MemoryEvent) MemoryEventResponse {
	var envelope map[string]any
	if len(event.Envelope) > 0 {
		_ = json.Unmarshal(event.Envelope, &envelope)
	}
	if envelope == nil {
		envelope = map[string]any{}
	}
	return MemoryEventResponse{
		ID:             uuidToString(event.ID),
		WorkspaceID:    uuidToString(event.WorkspaceID),
		ProjectID:      uuidToPtr(event.ProjectID),
		AgentID:        uuidToPtr(event.AgentID),
		IssueID:        uuidToPtr(event.IssueID),
		TaskID:         uuidToPtr(event.TaskID),
		ActorType:      event.ActorType,
		ActorID:        uuidToPtr(event.ActorID),
		EventType:      event.EventType,
		IdempotencyKey: event.IdempotencyKey,
		Status:         event.Status,
		Envelope:       envelope,
		CreatedAt:      timestampToString(event.CreatedAt),
	}
}

func memoryDeliveryToResponse(delivery db.MemoryProviderDelivery) MemoryDeliveryResponse {
	return MemoryDeliveryResponse{
		ID:            uuidToString(delivery.ID),
		WorkspaceID:   uuidToString(delivery.WorkspaceID),
		MemoryEventID: uuidToString(delivery.MemoryEventID),
		Provider:      delivery.Provider,
		Status:        delivery.Status,
		AttemptCount:  delivery.AttemptCount,
		DeliveryLagMs: delivery.DeliveryLagMs,
		NextAttemptAt: timestampToString(delivery.NextAttemptAt),
	}
}

func memoryRecallSampleToResponse(sample db.MemoryRecallSample) MemoryRecallSampleResponse {
	var results []any
	if len(sample.Results) > 0 {
		_ = json.Unmarshal(sample.Results, &results)
	}
	if results == nil {
		results = []any{}
	}
	var provenance map[string]any
	if len(sample.Provenance) > 0 {
		_ = json.Unmarshal(sample.Provenance, &provenance)
	}
	if provenance == nil {
		provenance = map[string]any{}
	}
	return MemoryRecallSampleResponse{
		ID:                  uuidToString(sample.ID),
		WorkspaceID:         uuidToString(sample.WorkspaceID),
		ProjectID:           uuidToPtr(sample.ProjectID),
		AgentID:             uuidToPtr(sample.AgentID),
		IssueID:             uuidToPtr(sample.IssueID),
		TaskID:              uuidToPtr(sample.TaskID),
		Provider:            sample.Provider,
		ReadMode:            sample.ReadMode,
		RecallCorrelationID: sample.RecallCorrelationID,
		Query:               sample.Query,
		Results:             results,
		Provenance:          provenance,
		SampledAt:           timestampToString(sample.SampledAt),
	}
}

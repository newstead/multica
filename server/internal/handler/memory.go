package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
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
	Provider       string          `json:"provider"`
	IdempotencyKey string          `json:"idempotency_key"`
	CorrelationID  string          `json:"correlation_id"`
	SourceID       string          `json:"source_id"`
	ProjectID      string          `json:"project_id"`
	AgentID        string          `json:"agent_id"`
	IssueID        string          `json:"issue_id"`
	TaskID         string          `json:"task_id"`
	ActorType      string          `json:"actor_type"`
	ActorID        string          `json:"actor_id"`
	SourceType     string          `json:"source_type"`
	Text           string          `json:"text"`
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

type MemoryMem0BoardDeliveryResponse struct {
	ID                string  `json:"id"`
	WorkspaceID       string  `json:"workspace_id"`
	MemoryEventID     string  `json:"memory_event_id"`
	ProjectID         *string `json:"project_id,omitempty"`
	AgentID           *string `json:"agent_id,omitempty"`
	IssueID           *string `json:"issue_id,omitempty"`
	TaskID            *string `json:"task_id,omitempty"`
	EventType         string  `json:"event_type"`
	Provider          string  `json:"provider"`
	Status            string  `json:"status"`
	AttemptCount      int32   `json:"attempt_count"`
	DeliveryLagMs     int64   `json:"delivery_lag_ms"`
	EventCreatedAt    string  `json:"event_created_at"`
	DeliveryCreatedAt string  `json:"delivery_created_at"`
	LastAttemptAt     string  `json:"last_attempt_at,omitempty"`
	TerminalAt        string  `json:"terminal_at,omitempty"`
	UpdatedAt         string  `json:"updated_at"`
}

type MemoryMem0BoardResponse struct {
	Health        *service.MemoryProviderHealth     `json:"health,omitempty"`
	HealthError   string                            `json:"health_error,omitempty"`
	Deliveries    []MemoryMem0BoardDeliveryResponse `json:"deliveries"`
	RecallSamples []MemoryRecallSampleResponse      `json:"recall_samples"`
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

type MemoryAuditDeliveryResponse struct {
	ID               string         `json:"id"`
	Provider         string         `json:"provider"`
	Status           string         `json:"status"`
	AttemptCount     int32          `json:"attempt_count"`
	ProviderMemoryID *string        `json:"provider_memory_id,omitempty"`
	Response         map[string]any `json:"response"`
	Error            *string        `json:"error,omitempty"`
	CreatedAt        string         `json:"created_at"`
	UpdatedAt        string         `json:"updated_at"`
	TerminalAt       *string        `json:"terminal_at,omitempty"`
	DeliveryLagMs    int64          `json:"delivery_lag_ms"`
}

type MemoryAuditEventResponse struct {
	ID             string                        `json:"id"`
	WorkspaceID    string                        `json:"workspace_id"`
	ProjectID      *string                       `json:"project_id,omitempty"`
	AgentID        *string                       `json:"agent_id,omitempty"`
	IssueID        *string                       `json:"issue_id,omitempty"`
	TaskID         *string                       `json:"task_id,omitempty"`
	ActorType      string                        `json:"actor_type"`
	ActorID        *string                       `json:"actor_id,omitempty"`
	EventType      string                        `json:"event_type"`
	IdempotencyKey string                        `json:"idempotency_key"`
	Status         string                        `json:"status"`
	SourceType     *string                       `json:"source_type,omitempty"`
	SourceID       *string                       `json:"source_id,omitempty"`
	Text           *string                       `json:"text,omitempty"`
	Envelope       map[string]any                `json:"envelope"`
	Metadata       map[string]any                `json:"metadata"`
	CreatedAt      string                        `json:"created_at"`
	UpdatedAt      string                        `json:"updated_at"`
	Deliveries     []MemoryAuditDeliveryResponse `json:"deliveries"`
}

type MemoryAuditListResponse struct {
	Events []MemoryAuditEventResponse `json:"events"`
	Total  int                        `json:"total"`
	Limit  int                        `json:"limit"`
	Offset int                        `json:"offset"`
}

type MemoryMutationRequest struct {
	Provider         string         `json:"provider"`
	ProviderMemoryID string         `json:"provider_memory_id"`
	MemoryID         string         `json:"memory_id"`
	DocumentID       string         `json:"document_id"`
	Text             string         `json:"text"`
	Reason           string         `json:"reason"`
	Metadata         map[string]any `json:"metadata"`
	Confirmation     string         `json:"confirmation"`
}

type MemoryEraseRequest struct {
	Scope        string `json:"scope"`
	ProjectID    string `json:"project_id"`
	IssueID      string `json:"issue_id"`
	Confirmation string `json:"confirmation"`
}

type MemoryMutationProviderResult struct {
	Provider         string                  `json:"provider"`
	ProviderMemoryID string                  `json:"provider_memory_id,omitempty"`
	EventID          string                  `json:"event_id,omitempty"`
	DeliveryID       string                  `json:"delivery_id,omitempty"`
	Status           string                  `json:"status"`
	Error            string                  `json:"error,omitempty"`
	Response         map[string]any          `json:"response,omitempty"`
	Delivery         *MemoryDeliveryResponse `json:"delivery,omitempty"`
}

type MemoryMutationResponse struct {
	Operation string                         `json:"operation"`
	Results   []MemoryMutationProviderResult `json:"results"`
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
	if strings.TrimSpace(req.SourceType) != "" {
		sourceID, ok := optionalUUIDOrBadRequest(w, req.SourceID, "source_id")
		if !ok {
			return
		}
		res, attempted, err := h.MemoryService.RetainApprovedSource(r.Context(), service.MemoryCaptureSource{
			SourceType: req.SourceType,
			SourceID:   sourceID,
			Scope:      scope,
			Actor:      service.MemoryActor{Type: req.ActorType, ID: actorID},
			Text:       req.Text,
			Metadata:   req.Metadata,
		})
		if !attempted {
			writeError(w, http.StatusBadRequest, "memory source is not approved for capture")
			return
		}
		writeMemoryRetainResponse(w, res, err)
		return
	}
	res, err := h.MemoryService.Retain(r.Context(), service.MemoryRetainRequest{
		Scope:          scope,
		Actor:          service.MemoryActor{Type: req.ActorType, ID: actorID},
		EventType:      strings.TrimSpace(req.EventType),
		Provider:       strings.TrimSpace(req.Provider),
		IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
		CorrelationID:  strings.TrimSpace(req.CorrelationID),
		SourceID:       strings.TrimSpace(req.SourceID),
		Content:        req.Content,
		Metadata:       req.Metadata,
	})
	writeMemoryRetainResponse(w, res, err)
}

func writeMemoryRetainResponse(w http.ResponseWriter, res service.MemoryRetainResult, err error) {
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

func (h *Handler) GetMemoryMem0Board(w http.ResponseWriter, r *http.Request) {
	if !h.memoryGatewayEnabled(r) {
		writeError(w, http.StatusNotFound, "memory gateway not found")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	limit, offset := parseMemoryLimitOffset(r, 100, 500)
	filters, ok := memoryScopeFiltersFromQuery(w, r)
	if !ok {
		return
	}
	deliveryRows, err := h.Queries.ListMemoryMem0DeliveriesByWorkspace(r.Context(), db.ListMemoryMem0DeliveriesByWorkspaceParams{
		WorkspaceID: wsUUID,
		Limit:       int32(limit),
		Offset:      int32(offset),
		ProjectID:   filters.ProjectID,
		AgentID:     filters.AgentID,
		IssueID:     filters.IssueID,
		TaskID:      filters.TaskID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list mem0 memory deliveries")
		return
	}
	recallRows, err := h.Queries.ListMemoryRecallSamplesByWorkspaceProviderAndScope(r.Context(), db.ListMemoryRecallSamplesByWorkspaceProviderAndScopeParams{
		WorkspaceID: wsUUID,
		Provider:    service.Mem0ProviderName,
		Limit:       int32(limit),
		Offset:      int32(offset),
		ProjectID:   filters.ProjectID,
		AgentID:     filters.AgentID,
		IssueID:     filters.IssueID,
		TaskID:      filters.TaskID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list mem0 recall samples")
		return
	}
	deliveries := make([]MemoryMem0BoardDeliveryResponse, 0, len(deliveryRows))
	for _, row := range deliveryRows {
		deliveries = append(deliveries, memoryMem0BoardDeliveryToResponse(row))
	}
	recallSamples := make([]MemoryRecallSampleResponse, 0, len(recallRows))
	for _, row := range recallRows {
		recallSamples = append(recallSamples, memoryRecallSampleToResponse(row))
	}
	health, healthErr := h.memoryProviderHealth(r.Context(), service.Mem0ProviderName, uuidToString(wsUUID))
	writeJSON(w, http.StatusOK, MemoryMem0BoardResponse{
		Health:        health,
		HealthError:   healthErr,
		Deliveries:    deliveries,
		RecallSamples: recallSamples,
	})
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

type memoryScopeFilters struct {
	ProjectID pgtype.UUID
	AgentID   pgtype.UUID
	IssueID   pgtype.UUID
	TaskID    pgtype.UUID
}

func memoryScopeFiltersFromQuery(w http.ResponseWriter, r *http.Request) (memoryScopeFilters, bool) {
	query := r.URL.Query()
	projectID, ok := optionalUUIDOrBadRequest(w, query.Get("project_id"), "project_id")
	if !ok {
		return memoryScopeFilters{}, false
	}
	agentID, ok := optionalUUIDOrBadRequest(w, query.Get("agent_id"), "agent_id")
	if !ok {
		return memoryScopeFilters{}, false
	}
	issueID, ok := optionalUUIDOrBadRequest(w, query.Get("issue_id"), "issue_id")
	if !ok {
		return memoryScopeFilters{}, false
	}
	taskID, ok := optionalUUIDOrBadRequest(w, query.Get("task_id"), "task_id")
	if !ok {
		return memoryScopeFilters{}, false
	}
	return memoryScopeFilters{ProjectID: projectID, AgentID: agentID, IssueID: issueID, TaskID: taskID}, true
}

func (h *Handler) ListMemoryAuditEvents(w http.ResponseWriter, r *http.Request) {
	h.listMemoryAuditEvents(w, r, false)
}

func (h *Handler) ExportMemoryAuditEvents(w http.ResponseWriter, r *http.Request) {
	h.listMemoryAuditEvents(w, r, true)
}

func (h *Handler) listMemoryAuditEvents(w http.ResponseWriter, r *http.Request, export bool) {
	if !h.memoryGatewayEnabled(r) {
		writeError(w, http.StatusNotFound, "memory gateway not found")
		return
	}
	if h.DB == nil {
		writeError(w, http.StatusInternalServerError, "memory audit store is not configured")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	limit, offset := parseMemoryLimitOffset(r, 100, 500)
	if export {
		limit, offset = 1000, 0
	}
	filters, ok := memoryAuditFiltersFromRequest(w, r, wsUUID)
	if !ok {
		return
	}
	events, total, err := h.queryMemoryAuditEvents(r.Context(), filters, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list memory audit events")
		return
	}
	writeJSON(w, http.StatusOK, MemoryAuditListResponse{Events: events, Total: total, Limit: limit, Offset: offset})
}

func (h *Handler) CorrectMemoryAuditEvent(w http.ResponseWriter, r *http.Request) {
	h.mutateMemoryAuditEvent(w, r, "update")
}

func (h *Handler) InvalidateMemoryAuditEvent(w http.ResponseWriter, r *http.Request) {
	h.mutateMemoryAuditEvent(w, r, "invalidate")
}

func (h *Handler) DeleteMemoryAuditEvent(w http.ResponseWriter, r *http.Request) {
	h.mutateMemoryAuditEvent(w, r, "delete")
}

func (h *Handler) mutateMemoryAuditEvent(w http.ResponseWriter, r *http.Request, operation string) {
	if !h.memoryGatewayEnabled(r) {
		writeError(w, http.StatusNotFound, "memory gateway not found")
		return
	}
	if h.MemoryService == nil || h.DB == nil {
		writeError(w, http.StatusInternalServerError, "memory service is not configured")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	eventUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "eventId"), "memory event id")
	if !ok {
		return
	}
	var req MemoryMutationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if operation == "delete" && req.Confirmation != "DELETE" {
		writeError(w, http.StatusBadRequest, "delete confirmation is required")
		return
	}
	if operation == "invalidate" && req.Confirmation != "INVALIDATE" {
		writeError(w, http.StatusBadRequest, "invalidate confirmation is required")
		return
	}
	if operation == "update" && strings.TrimSpace(req.Text) == "" && len(req.Metadata) == 0 {
		writeError(w, http.StatusBadRequest, "correction text or metadata is required")
		return
	}
	source, err := h.Queries.GetMemoryEventInWorkspace(r.Context(), db.GetMemoryEventInWorkspaceParams{ID: eventUUID, WorkspaceID: wsUUID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "memory event not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load memory event")
		return
	}
	deliveries, err := h.memoryDeliveriesForEvent(r.Context(), wsUUID, eventUUID, strings.TrimSpace(req.Provider))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load provider deliveries")
		return
	}
	if len(deliveries) == 0 {
		writeError(w, http.StatusBadRequest, "no provider deliveries match this memory")
		return
	}
	actorType, actorID := h.resolveActor(r, requestUserID(r), uuidToString(wsUUID))
	results := make([]MemoryMutationProviderResult, 0, len(deliveries))
	for _, delivery := range deliveries {
		target := memoryMutationTargetFromDelivery(source, delivery)
		if err := validateMemoryMutationRequestTarget(req, target); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		content, resultID, targetKind, skipReason := memoryMutationPayload(operation, target)
		if skipReason != "" {
			results = append(results, MemoryMutationProviderResult{Provider: delivery.Provider, ProviderMemoryID: resultID, Status: "skipped", Error: skipReason})
			continue
		}
		if req.Text != "" {
			content["text"] = req.Text
		}
		if req.Reason != "" {
			content["reason"] = req.Reason
		}
		if req.Metadata != nil {
			content["metadata"] = req.Metadata
		}
		if operation == "invalidate" {
			content["state"] = "invalidated"
		}
		results = append(results, h.dispatchMemoryAdminMutation(r, source, actorType, actorID, operation, delivery.Provider, resultID, targetKind, content))
	}
	writeJSON(w, http.StatusOK, MemoryMutationResponse{Operation: operation, Results: results})
}

func (h *Handler) EraseMemoryScope(w http.ResponseWriter, r *http.Request) {
	if !h.memoryGatewayEnabled(r) {
		writeError(w, http.StatusNotFound, "memory gateway not found")
		return
	}
	if h.MemoryService == nil || h.DB == nil {
		writeError(w, http.StatusInternalServerError, "memory service is not configured")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}
	var req MemoryEraseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Confirmation != "ERASE" {
		writeError(w, http.StatusBadRequest, "erase confirmation is required")
		return
	}
	scope := strings.TrimSpace(req.Scope)
	if scope == "" {
		scope = "workspace"
	}
	if scope != "workspace" && scope != "project" && scope != "issue" {
		writeError(w, http.StatusBadRequest, "invalid erase scope")
		return
	}
	filters := memoryAuditFilters{WorkspaceID: wsUUID}
	if scope == "project" {
		if strings.TrimSpace(req.ProjectID) == "" {
			writeError(w, http.StatusBadRequest, "project_id is required")
			return
		}
		projectID, ok := optionalUUIDOrBadRequest(w, req.ProjectID, "project_id")
		if !ok {
			return
		}
		filters.ProjectID = projectID
	}
	if scope == "issue" {
		if strings.TrimSpace(req.IssueID) == "" {
			writeError(w, http.StatusBadRequest, "issue_id is required")
			return
		}
		issueID, ok := optionalUUIDOrBadRequest(w, req.IssueID, "issue_id")
		if !ok {
			return
		}
		filters.IssueID = issueID
	}
	targets, err := h.memoryEraseTargets(r.Context(), filters)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load erase targets")
		return
	}
	actorType, actorID := h.resolveActor(r, requestUserID(r), uuidToString(wsUUID))
	results := make([]MemoryMutationProviderResult, 0, len(targets))
	seen := map[string]bool{}
	for _, eraseTarget := range targets {
		providerMemoryID := strings.TrimSpace(eraseTarget.ProviderMemoryID.String)
		if providerMemoryID == "" {
			continue
		}
		target := memoryMutationTarget{Provider: eraseTarget.Provider, ProviderMemoryID: providerMemoryID}
		if target.Provider == "hindsight" {
			target.DocumentID = providerMemoryID
		} else {
			target.MemoryID = providerMemoryID
		}
		content, resultID, targetKind, skipReason := memoryMutationPayload("delete", target)
		if skipReason != "" {
			results = append(results, MemoryMutationProviderResult{Provider: eraseTarget.Provider, ProviderMemoryID: resultID, Status: "skipped", Error: skipReason})
			continue
		}
		key := eraseTarget.Provider + "\x00" + targetKind + "\x00" + resultID
		if seen[key] {
			continue
		}
		seen[key] = true
		content["reason"] = "scope erase"
		results = append(results, h.dispatchMemoryAdminMutation(r, eraseTarget.Event, actorType, actorID, "delete", eraseTarget.Provider, resultID, targetKind, content))
	}
	writeJSON(w, http.StatusOK, MemoryMutationResponse{Operation: "erase", Results: results})
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

type memoryAuditFilters struct {
	WorkspaceID pgtype.UUID
	ProjectID   pgtype.UUID
	AgentID     pgtype.UUID
	IssueID     pgtype.UUID
	TaskID      pgtype.UUID
}

type memoryEraseTarget struct {
	Event            db.MemoryEvent
	Provider         string
	ProviderMemoryID pgtype.Text
}

type memoryMutationTarget struct {
	Provider         string
	ProviderMemoryID string
	MemoryID         string
	DocumentID       string
}

func memoryAuditFiltersFromRequest(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID) (memoryAuditFilters, bool) {
	q := r.URL.Query()
	filters := memoryAuditFilters{WorkspaceID: workspaceID}
	var ok bool
	if filters.ProjectID, ok = optionalUUIDOrBadRequest(w, q.Get("project_id"), "project_id"); !ok {
		return memoryAuditFilters{}, false
	}
	if filters.AgentID, ok = optionalUUIDOrBadRequest(w, q.Get("agent_id"), "agent_id"); !ok {
		return memoryAuditFilters{}, false
	}
	if filters.IssueID, ok = optionalUUIDOrBadRequest(w, q.Get("issue_id"), "issue_id"); !ok {
		return memoryAuditFilters{}, false
	}
	if filters.TaskID, ok = optionalUUIDOrBadRequest(w, q.Get("task_id"), "task_id"); !ok {
		return memoryAuditFilters{}, false
	}
	return filters, true
}

func (h *Handler) queryMemoryAuditEvents(ctx context.Context, filters memoryAuditFilters, limit, offset int) ([]MemoryAuditEventResponse, int, error) {
	args := []any{filters.WorkspaceID}
	where := []string{"memory_event.workspace_id = $1"}
	addUUIDFilter := func(column string, value pgtype.UUID) {
		if !value.Valid {
			return
		}
		args = append(args, value)
		where = append(where, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	addUUIDFilter("memory_event.project_id", filters.ProjectID)
	addUUIDFilter("memory_event.agent_id", filters.AgentID)
	addUUIDFilter("memory_event.issue_id", filters.IssueID)
	addUUIDFilter("memory_event.task_id", filters.TaskID)
	whereSQL := strings.Join(where, " AND ")
	countSQL := "SELECT count(*) FROM memory_event WHERE " + whereSQL
	var total int
	if err := h.DB.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	query := `
WITH page AS (
  SELECT * FROM memory_event
  WHERE ` + whereSQL + `
  ORDER BY created_at DESC
  LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args)) + `
)
SELECT
  page.id, page.workspace_id, page.project_id, page.agent_id,
  page.issue_id, page.task_id, page.actor_type, page.actor_id,
  page.event_type, page.idempotency_key, page.envelope, page.status,
  page.created_at, page.updated_at,
  memory_provider_delivery.id, memory_provider_delivery.provider, memory_provider_delivery.status,
  COALESCE(memory_provider_delivery.attempt_count, 0), memory_provider_delivery.provider_memory_id,
  COALESCE(memory_provider_delivery.response, '{}'::jsonb), memory_provider_delivery.error, memory_provider_delivery.created_at,
  memory_provider_delivery.updated_at, memory_provider_delivery.terminal_at,
  COALESCE(memory_provider_delivery.delivery_lag_ms, 0)
FROM page
LEFT JOIN memory_provider_delivery
  ON memory_provider_delivery.memory_event_id = page.id
 AND memory_provider_delivery.workspace_id = page.workspace_id
ORDER BY page.created_at DESC, memory_provider_delivery.provider ASC`
	rows, err := h.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	events := []MemoryAuditEventResponse{}
	eventByID := map[string]int{}
	for rows.Next() {
		var event db.MemoryEvent
		var deliveryID pgtype.UUID
		var deliveryProvider, deliveryStatus pgtype.Text
		var deliveryAttemptCount int32
		var providerMemoryID pgtype.Text
		var response []byte
		var deliveryError pgtype.Text
		var deliveryCreatedAt, deliveryUpdatedAt, terminalAt pgtype.Timestamptz
		var deliveryLagMs int64
		if err := rows.Scan(
			&event.ID, &event.WorkspaceID, &event.ProjectID, &event.AgentID,
			&event.IssueID, &event.TaskID, &event.ActorType, &event.ActorID,
			&event.EventType, &event.IdempotencyKey, &event.Envelope, &event.Status,
			&event.CreatedAt, &event.UpdatedAt,
			&deliveryID, &deliveryProvider, &deliveryStatus, &deliveryAttemptCount,
			&providerMemoryID, &response, &deliveryError, &deliveryCreatedAt,
			&deliveryUpdatedAt, &terminalAt, &deliveryLagMs,
		); err != nil {
			return nil, 0, err
		}
		eventID := uuidToString(event.ID)
		idx, exists := eventByID[eventID]
		if !exists {
			events = append(events, memoryAuditEventToResponse(event))
			idx = len(events) - 1
			eventByID[eventID] = idx
		}
		if deliveryID.Valid {
			events[idx].Deliveries = append(events[idx].Deliveries, memoryAuditDeliveryToResponse(deliveryID, deliveryProvider.String, deliveryStatus.String, deliveryAttemptCount, providerMemoryID, response, deliveryError, deliveryCreatedAt, deliveryUpdatedAt, terminalAt, deliveryLagMs))
		}
	}
	return events, total, rows.Err()
}

func (h *Handler) memoryDeliveriesForEvent(ctx context.Context, workspaceID, eventID pgtype.UUID, provider string) ([]db.MemoryProviderDelivery, error) {
	args := []any{workspaceID, eventID}
	where := "workspace_id = $1 AND memory_event_id = $2"
	if provider != "" {
		args = append(args, provider)
		where += " AND provider = $3"
	}
	rows, err := h.DB.Query(ctx, `
SELECT id, workspace_id, memory_event_id, provider, status, attempt_count, next_attempt_at,
       last_attempt_at, terminal_at, provider_memory_id, response, error, created_at, updated_at,
       delivery_lag_ms
FROM memory_provider_delivery
WHERE `+where+`
ORDER BY provider ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deliveries := []db.MemoryProviderDelivery{}
	for rows.Next() {
		var d db.MemoryProviderDelivery
		if err := rows.Scan(&d.ID, &d.WorkspaceID, &d.MemoryEventID, &d.Provider, &d.Status, &d.AttemptCount, &d.NextAttemptAt, &d.LastAttemptAt, &d.TerminalAt, &d.ProviderMemoryID, &d.Response, &d.Error, &d.CreatedAt, &d.UpdatedAt, &d.DeliveryLagMs); err != nil {
			return nil, err
		}
		deliveries = append(deliveries, d)
	}
	return deliveries, rows.Err()
}

func (h *Handler) memoryEraseTargets(ctx context.Context, filters memoryAuditFilters) ([]memoryEraseTarget, error) {
	args := []any{filters.WorkspaceID}
	where := []string{"memory_event.workspace_id = $1", "memory_event.event_type = 'retain'", "memory_provider_delivery.status = 'delivered'", "memory_provider_delivery.provider_memory_id IS NOT NULL"}
	addUUIDFilter := func(column string, value pgtype.UUID) {
		if !value.Valid {
			return
		}
		args = append(args, value)
		where = append(where, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	addUUIDFilter("memory_event.project_id", filters.ProjectID)
	addUUIDFilter("memory_event.issue_id", filters.IssueID)
	rows, err := h.DB.Query(ctx, `
SELECT memory_event.id, memory_event.workspace_id, memory_event.project_id, memory_event.agent_id,
       memory_event.issue_id, memory_event.task_id, memory_event.actor_type, memory_event.actor_id,
       memory_event.event_type, memory_event.idempotency_key, memory_event.envelope, memory_event.status,
       memory_event.attempt_count, memory_event.available_at, memory_event.last_attempt_at,
       memory_event.terminal_at, memory_event.error, memory_event.created_at, memory_event.updated_at,
       memory_provider_delivery.provider, memory_provider_delivery.provider_memory_id
FROM memory_event
JOIN memory_provider_delivery
  ON memory_provider_delivery.memory_event_id = memory_event.id
 AND memory_provider_delivery.workspace_id = memory_event.workspace_id
WHERE `+strings.Join(where, " AND ")+`
ORDER BY memory_event.created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := []memoryEraseTarget{}
	for rows.Next() {
		var target memoryEraseTarget
		if err := rows.Scan(
			&target.Event.ID, &target.Event.WorkspaceID, &target.Event.ProjectID, &target.Event.AgentID,
			&target.Event.IssueID, &target.Event.TaskID, &target.Event.ActorType, &target.Event.ActorID,
			&target.Event.EventType, &target.Event.IdempotencyKey, &target.Event.Envelope, &target.Event.Status,
			&target.Event.AttemptCount, &target.Event.AvailableAt, &target.Event.LastAttemptAt,
			&target.Event.TerminalAt, &target.Event.Error, &target.Event.CreatedAt, &target.Event.UpdatedAt,
			&target.Provider, &target.ProviderMemoryID,
		); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func memoryMutationTargetFromDelivery(source db.MemoryEvent, delivery db.MemoryProviderDelivery) memoryMutationTarget {
	providerMemoryID := strings.TrimSpace(delivery.ProviderMemoryID.String)
	target := memoryMutationTarget{Provider: delivery.Provider, ProviderMemoryID: providerMemoryID}
	if delivery.Provider != "hindsight" {
		target.MemoryID = providerMemoryID
		return target
	}

	memoryID := firstNonEmpty(memoryDeliveryResponseString(delivery.Response, "memory_id"), memoryDeliveryResponseString(delivery.Response, "id"))
	documentID := memoryDeliveryResponseString(delivery.Response, "document_id")
	switch source.EventType {
	case "retain", "delete":
		documentID = firstNonEmpty(documentID, providerMemoryID)
	case "invalidate", "restore":
		memoryID = firstNonEmpty(memoryID, providerMemoryID)
	case "update":
		if memoryID == "" {
			documentID = firstNonEmpty(documentID, providerMemoryID)
		}
	default:
		memoryID = firstNonEmpty(memoryID, providerMemoryID)
	}
	target.MemoryID = memoryID
	target.DocumentID = documentID
	return target
}

func validateMemoryMutationRequestTarget(req MemoryMutationRequest, target memoryMutationTarget) error {
	checks := []struct {
		field     string
		supplied  string
		canonical string
	}{
		{field: "provider_memory_id", supplied: req.ProviderMemoryID, canonical: target.ProviderMemoryID},
		{field: "memory_id", supplied: req.MemoryID, canonical: target.MemoryID},
		{field: "document_id", supplied: req.DocumentID, canonical: target.DocumentID},
	}
	for _, check := range checks {
		supplied := strings.TrimSpace(check.supplied)
		if supplied == "" {
			continue
		}
		if supplied != strings.TrimSpace(check.canonical) {
			return fmt.Errorf("%s does not match the audited provider target", check.field)
		}
	}
	return nil
}

func memoryMutationPayload(operation string, target memoryMutationTarget) (map[string]any, string, string, string) {
	content := map[string]any{}
	if target.Provider == "hindsight" {
		switch operation {
		case "delete":
			if target.DocumentID == "" {
				return nil, target.ProviderMemoryID, "document", "provider document id is missing"
			}
			content["document_id"] = target.DocumentID
			return content, target.DocumentID, "document", ""
		case "invalidate":
			if target.MemoryID == "" {
				return nil, target.ProviderMemoryID, "memory", "verified provider memory id is missing"
			}
			content["memory_id"] = target.MemoryID
			content["provider_memory_id"] = target.MemoryID
			return content, target.MemoryID, "memory", ""
		default:
			if target.DocumentID != "" {
				content["document_id"] = target.DocumentID
				return content, target.DocumentID, "document", ""
			}
			if target.MemoryID != "" {
				content["memory_id"] = target.MemoryID
				content["provider_memory_id"] = target.MemoryID
				return content, target.MemoryID, "memory", ""
			}
			return nil, target.ProviderMemoryID, "", "provider target id is missing"
		}
	}

	if target.MemoryID == "" {
		return nil, target.ProviderMemoryID, "memory", "provider memory id is missing"
	}
	content["provider_memory_id"] = target.MemoryID
	content["memory_id"] = target.MemoryID
	return content, target.MemoryID, "memory", ""
}

func memoryDeliveryResponseString(response []byte, keys ...string) string {
	if len(response) == 0 {
		return ""
	}
	var data map[string]any
	if err := json.Unmarshal(response, &data); err != nil {
		return ""
	}
	for _, key := range keys {
		value, ok := data[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				return trimmed
			}
		case fmt.Stringer:
			if trimmed := strings.TrimSpace(typed.String()); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func memoryAdminMutationMetadata(source db.MemoryEvent, provider, providerMemoryID, targetKind, operation string, content map[string]any) map[string]any {
	metadata := map[string]any{
		"source_event_id":    uuidToString(source.ID),
		"provider":           provider,
		"provider_memory_id": providerMemoryID,
		"admin_action":       operation,
	}
	if targetKind != "" {
		metadata["target_kind"] = targetKind
	}
	if memoryID, ok := content["memory_id"].(string); ok && strings.TrimSpace(memoryID) != "" {
		metadata["memory_id"] = strings.TrimSpace(memoryID)
	}
	if documentID, ok := content["document_id"].(string); ok && strings.TrimSpace(documentID) != "" {
		metadata["document_id"] = strings.TrimSpace(documentID)
	}
	return metadata
}

func (h *Handler) dispatchMemoryAdminMutation(r *http.Request, source db.MemoryEvent, actorType, actorID, operation, provider, providerMemoryID, targetKind string, content map[string]any) MemoryMutationProviderResult {
	result := MemoryMutationProviderResult{Provider: provider, ProviderMemoryID: providerMemoryID, Status: "failed"}
	contentRaw, err := json.Marshal(content)
	if err != nil {
		result.Error = "failed to encode memory content"
		return result
	}
	actorUUID, _ := util.ParseUUID(actorID)
	req := service.MemoryRetainRequest{
		Scope:         service.MemoryScope{WorkspaceID: source.WorkspaceID, ProjectID: source.ProjectID, AgentID: source.AgentID, IssueID: source.IssueID, TaskID: source.TaskID},
		Actor:         service.MemoryActor{Type: actorType, ID: actorUUID},
		EventType:     operation,
		CorrelationID: "memory_admin:" + operation + ":" + uuidToString(source.ID) + ":" + provider,
		SourceID:      uuidToString(source.ID),
		Content:       contentRaw,
		Metadata:      memoryAdminMutationMetadata(source, provider, providerMemoryID, targetKind, operation, content),
	}
	_, envelopeRaw, err := service.BuildMemoryEventEnvelope(req)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	req.IdempotencyKey = service.MemoryIdempotencyKey(envelopeRaw)
	inserted, err := h.Queries.UpsertMemoryEvent(r.Context(), db.UpsertMemoryEventParams{
		WorkspaceID: source.WorkspaceID, ProjectID: source.ProjectID, AgentID: source.AgentID,
		IssueID: source.IssueID, TaskID: source.TaskID, ActorType: actorType, ActorID: actorUUID,
		EventType: operation, IdempotencyKey: req.IdempotencyKey, Envelope: envelopeRaw,
		AvailableAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	event := memoryEventFromUpsertRow(inserted)
	result.EventID = uuidToString(event.ID)
	delivery, err := h.Queries.UpsertMemoryProviderDelivery(r.Context(), db.UpsertMemoryProviderDeliveryParams{
		WorkspaceID: source.WorkspaceID, MemoryEventID: event.ID, Provider: provider,
		NextAttemptAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}
	deliveryRow := memoryDeliveryFromUpsertRow(delivery)
	result.DeliveryID = uuidToString(deliveryRow.ID)
	dispatch, err := h.MemoryService.DispatchMemoryProviderDelivery(r.Context(), source.WorkspaceID, deliveryRow.ID)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Status = dispatch.Delivery.Status
	deliveryResponse := memoryDeliveryToResponse(dispatch.Delivery)
	result.Delivery = &deliveryResponse
	if dispatch.Error != "" {
		result.Error = dispatch.Error
	}
	result.Response = parseMemoryProvenance(dispatch.Delivery.Response)
	return result
}

func memoryEventFromUpsertRow(row db.UpsertMemoryEventRow) db.MemoryEvent {
	return db.MemoryEvent{ID: row.ID, WorkspaceID: row.WorkspaceID, ProjectID: row.ProjectID, AgentID: row.AgentID, IssueID: row.IssueID, TaskID: row.TaskID, ActorType: row.ActorType, ActorID: row.ActorID, EventType: row.EventType, IdempotencyKey: row.IdempotencyKey, Envelope: row.Envelope, Status: row.Status, AttemptCount: row.AttemptCount, AvailableAt: row.AvailableAt, LastAttemptAt: row.LastAttemptAt, TerminalAt: row.TerminalAt, Error: row.Error, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func memoryDeliveryFromUpsertRow(row db.UpsertMemoryProviderDeliveryRow) db.MemoryProviderDelivery {
	return db.MemoryProviderDelivery{ID: row.ID, WorkspaceID: row.WorkspaceID, MemoryEventID: row.MemoryEventID, Provider: row.Provider, Status: row.Status, AttemptCount: row.AttemptCount, NextAttemptAt: row.NextAttemptAt, LastAttemptAt: row.LastAttemptAt, TerminalAt: row.TerminalAt, ProviderMemoryID: row.ProviderMemoryID, Response: row.Response, Error: row.Error, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, DeliveryLagMs: row.DeliveryLagMs}
}

func memoryAuditEventToResponse(event db.MemoryEvent) MemoryAuditEventResponse {
	envelope := parseMemoryProvenance(event.Envelope)
	metadata := map[string]any{}
	if raw, ok := envelope["metadata"].(map[string]any); ok {
		metadata = raw
	}
	content, _ := envelope["content"].(map[string]any)
	return MemoryAuditEventResponse{ID: uuidToString(event.ID), WorkspaceID: uuidToString(event.WorkspaceID), ProjectID: uuidToPtr(event.ProjectID), AgentID: uuidToPtr(event.AgentID), IssueID: uuidToPtr(event.IssueID), TaskID: uuidToPtr(event.TaskID), ActorType: event.ActorType, ActorID: uuidToPtr(event.ActorID), EventType: event.EventType, IdempotencyKey: event.IdempotencyKey, Status: event.Status, SourceType: stringPtrFromAny(firstAny(metadata["source_type"], content["source_type"])), SourceID: stringPtrFromAny(firstAny(metadata["source_id"], content["source_id"], envelope["source_id"])), Text: stringPtrFromAny(content["text"]), Envelope: envelope, Metadata: metadata, CreatedAt: timestampToString(event.CreatedAt), UpdatedAt: timestampToString(event.UpdatedAt), Deliveries: []MemoryAuditDeliveryResponse{}}
}

func memoryAuditDeliveryToResponse(id pgtype.UUID, provider, status string, attemptCount int32, providerMemoryID pgtype.Text, response []byte, errText pgtype.Text, createdAt, updatedAt, terminalAt pgtype.Timestamptz, deliveryLagMs int64) MemoryAuditDeliveryResponse {
	return MemoryAuditDeliveryResponse{ID: uuidToString(id), Provider: provider, Status: status, AttemptCount: attemptCount, ProviderMemoryID: textToPtr(providerMemoryID), Response: parseMemoryProvenance(response), Error: textToPtr(errText), CreatedAt: timestampToString(createdAt), UpdatedAt: timestampToString(updatedAt), TerminalAt: timestampToPtr(terminalAt), DeliveryLagMs: deliveryLagMs}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstAny(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func stringPtrFromAny(value any) *string {
	s, ok := value.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return nil
	}
	s = strings.TrimSpace(s)
	return &s
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

type workspaceMemoryHealthProvider interface {
	HealthForWorkspace(context.Context, string) (service.MemoryProviderHealth, error)
}

func (h *Handler) memoryProviderHealth(ctx context.Context, providerName string, workspaceIDs ...string) (*service.MemoryProviderHealth, string) {
	if h == nil || h.MemoryService == nil {
		return nil, "memory service not configured"
	}
	for name, provider := range h.MemoryService.Providers {
		if !strings.EqualFold(name, providerName) {
			continue
		}
		var (
			health service.MemoryProviderHealth
			err    error
		)
		if len(workspaceIDs) > 0 && strings.TrimSpace(workspaceIDs[0]) != "" {
			if scoped, ok := provider.(workspaceMemoryHealthProvider); ok {
				health, err = scoped.HealthForWorkspace(ctx, workspaceIDs[0])
			} else {
				health, err = provider.Health(ctx)
			}
		} else {
			health, err = provider.Health(ctx)
		}
		if strings.TrimSpace(health.Provider) == "" {
			health.Provider = providerName
		}
		if err != nil {
			return &health, err.Error()
		}
		return &health, ""
	}
	return nil, "provider not registered"
}

func memoryMem0BoardDeliveryToResponse(delivery db.ListMemoryMem0DeliveriesByWorkspaceRow) MemoryMem0BoardDeliveryResponse {
	return MemoryMem0BoardDeliveryResponse{
		ID:                uuidToString(delivery.ID),
		WorkspaceID:       uuidToString(delivery.WorkspaceID),
		MemoryEventID:     uuidToString(delivery.MemoryEventID),
		ProjectID:         uuidToPtr(delivery.ProjectID),
		AgentID:           uuidToPtr(delivery.AgentID),
		IssueID:           uuidToPtr(delivery.IssueID),
		TaskID:            uuidToPtr(delivery.TaskID),
		EventType:         delivery.EventType,
		Provider:          delivery.Provider,
		Status:            delivery.Status,
		AttemptCount:      delivery.AttemptCount,
		DeliveryLagMs:     delivery.DeliveryLagMs,
		EventCreatedAt:    timestampToString(delivery.EventCreatedAt),
		DeliveryCreatedAt: timestampToString(delivery.DeliveryCreatedAt),
		LastAttemptAt:     timestampToString(delivery.LastAttemptAt),
		TerminalAt:        timestampToString(delivery.TerminalAt),
		UpdatedAt:         timestampToString(delivery.UpdatedAt),
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

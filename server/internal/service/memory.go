package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

var (
	ErrMemoryDisabled = errors.New("memory gateway disabled")
	ErrMemoryConfig   = errors.New("memory gateway config invalid")
)

type MemoryProvider interface {
	Name() string
	Retain(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error)
	Recall(ctx context.Context, req MemoryRecallRequest) (MemoryRecallResult, error)
	Update(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error)
	Invalidate(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error)
	Delete(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error)
	Health(ctx context.Context) (MemoryProviderHealth, error)
}

type MemoryProviderHealth struct {
	Provider string         `json:"provider"`
	OK       bool           `json:"ok"`
	Details  map[string]any `json:"details,omitempty"`
}

type MemoryProviderResult struct {
	ProviderMemoryID string          `json:"provider_memory_id,omitempty"`
	Response         json.RawMessage `json:"response,omitempty"`
}

type MemoryScope struct {
	WorkspaceID pgtype.UUID `json:"-"`
	ProjectID   pgtype.UUID `json:"-"`
	AgentID     pgtype.UUID `json:"-"`
	IssueID     pgtype.UUID `json:"-"`
	TaskID      pgtype.UUID `json:"-"`
}

type MemoryActor struct {
	Type string      `json:"type"`
	ID   pgtype.UUID `json:"-"`
}

type MemoryEventEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	EventType     string          `json:"event_type"`
	CorrelationID string          `json:"correlation_id"`
	SourceID      string          `json:"source_id"`
	Scope         memoryScopeJSON `json:"scope"`
	Actor         memoryActorJSON `json:"actor"`
	Content       json.RawMessage `json:"content"`
	Metadata      map[string]any  `json:"metadata,omitempty"`
}

type memoryScopeJSON struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	IssueID     string `json:"issue_id,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
}

type memoryActorJSON struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

type MemoryRetainRequest struct {
	Scope          MemoryScope
	Actor          MemoryActor
	EventType      string
	IdempotencyKey string
	CorrelationID  string
	SourceID       string
	Content        json.RawMessage
	Metadata       map[string]any
}

type MemoryRetainResult struct {
	Event        db.MemoryEvent
	Inserted     bool
	Deliveries   []db.MemoryProviderDelivery
	Envelope     MemoryEventEnvelope
	EnvelopeJSON []byte
}

type MemoryRecallRequest struct {
	Scope         MemoryScope
	Provider      string
	ReadMode      string
	CorrelationID string
	Query         string
	Limit         int32
}

type MemoryRecallResult struct {
	Provider   string          `json:"provider"`
	Results    json.RawMessage `json:"results"`
	Provenance json.RawMessage `json:"provenance"`
}

type MemoryProviderRecall struct {
	Provider string
	Result   MemoryRecallResult
	Sample   db.MemoryRecallSample
}

type MemoryRecallReadResult struct {
	Mode          string
	CorrelationID string
	Primary       *MemoryProviderRecall
	Shadow        *MemoryProviderRecall
	Errors        map[string]string
}

type MemoryDispatchResult struct {
	Provider string
	Delivery db.MemoryProviderDelivery
	Error    string
}

type MemoryService struct {
	Queries             *db.Queries
	TxStarter           TxStarter
	Clock               func() time.Time
	Providers           map[string]MemoryProvider
	MaxDeliveryAttempts int32
	BaseBackoff         time.Duration
}

func NewMemoryService(queries *db.Queries, txStarter ...TxStarter) *MemoryService {
	var tx TxStarter
	if len(txStarter) > 0 {
		tx = txStarter[0]
	}
	return &MemoryService{
		Queries:             queries,
		TxStarter:           tx,
		Clock:               time.Now,
		Providers:           map[string]MemoryProvider{},
		MaxDeliveryAttempts: 5,
		BaseBackoff:         time.Minute,
	}
}

func (s *MemoryService) now() time.Time {
	if s != nil && s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func (s *MemoryService) Retain(ctx context.Context, req MemoryRetainRequest) (MemoryRetainResult, error) {
	if s == nil || s.Queries == nil {
		return MemoryRetainResult{}, fmt.Errorf("memory service not configured")
	}
	if !req.Scope.WorkspaceID.Valid {
		return MemoryRetainResult{}, fmt.Errorf("%w: workspace_id is required", ErrMemoryConfig)
	}
	eventType, err := normalizeMemoryEventType(req.EventType)
	if err != nil {
		return MemoryRetainResult{}, err
	}
	req.EventType = eventType
	cfg, err := s.Queries.GetMemoryWorkspaceConfig(ctx, req.Scope.WorkspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MemoryRetainResult{}, ErrMemoryDisabled
		}
		return MemoryRetainResult{}, err
	}
	if !cfg.Enabled {
		return MemoryRetainResult{}, ErrMemoryDisabled
	}
	providers := deliveryProviders(cfg)
	if len(providers) == 0 {
		return MemoryRetainResult{}, fmt.Errorf("%w: at least one provider is required when enabled", ErrMemoryConfig)
	}

	key := strings.TrimSpace(req.IdempotencyKey)
	if req.SourceID == "" {
		req.SourceID = memorySourceID(req)
	}
	if req.CorrelationID == "" && key != "" {
		req.CorrelationID = key
	}
	envelope, raw, err := BuildMemoryEventEnvelope(req)
	if err != nil {
		return MemoryRetainResult{}, err
	}
	if key == "" {
		key = MemoryIdempotencyKey(raw)
		req.IdempotencyKey = key
		req.CorrelationID = key
		envelope, raw, err = BuildMemoryEventEnvelope(req)
		if err != nil {
			return MemoryRetainResult{}, err
		}
	}
	now := s.now()
	var out MemoryRetainResult
	if err := s.runInTx(ctx, func(q *db.Queries) error {
		eventRow, err := q.UpsertMemoryEvent(ctx, db.UpsertMemoryEventParams{
			WorkspaceID:    req.Scope.WorkspaceID,
			ProjectID:      req.Scope.ProjectID,
			AgentID:        req.Scope.AgentID,
			IssueID:        req.Scope.IssueID,
			TaskID:         req.Scope.TaskID,
			ActorType:      normalizeMemoryActorType(req.Actor.Type),
			ActorID:        req.Actor.ID,
			EventType:      req.EventType,
			IdempotencyKey: key,
			Envelope:       raw,
			AvailableAt:    pgtype.Timestamptz{Time: now, Valid: true},
		})
		if err != nil {
			return err
		}

		out = MemoryRetainResult{
			Event:        memoryEventFromUpsertRow(eventRow),
			Inserted:     eventRow.Inserted,
			Envelope:     envelope,
			EnvelopeJSON: raw,
		}
		for _, provider := range providers {
			delivery, err := q.UpsertMemoryProviderDelivery(ctx, db.UpsertMemoryProviderDeliveryParams{
				WorkspaceID:   req.Scope.WorkspaceID,
				MemoryEventID: eventRow.ID,
				Provider:      provider,
				NextAttemptAt: pgtype.Timestamptz{Time: now, Valid: true},
			})
			if err != nil {
				return err
			}
			out.Deliveries = append(out.Deliveries, memoryDeliveryFromUpsertRow(delivery))
		}
		return nil
	}); err != nil {
		return MemoryRetainResult{}, err
	}
	return out, nil
}

func (s *MemoryService) DispatchMemoryProviderDelivery(ctx context.Context, workspaceID, deliveryID pgtype.UUID) (MemoryDispatchResult, error) {
	if s == nil || s.Queries == nil {
		return MemoryDispatchResult{}, fmt.Errorf("memory service not configured")
	}
	row, err := s.Queries.GetMemoryProviderDeliveryForDispatch(ctx, db.GetMemoryProviderDeliveryForDispatchParams{ID: deliveryID, WorkspaceID: workspaceID})
	if err != nil {
		return MemoryDispatchResult{}, err
	}
	return s.dispatchMemoryProviderDelivery(ctx, row.MemoryProviderDelivery, row.Envelope, row.EventType, row.EventCreatedAt)
}

func (s *MemoryService) DispatchDueMemoryProviderDeliveries(ctx context.Context, workspaceID pgtype.UUID, limit int32) ([]MemoryDispatchResult, error) {
	if s == nil || s.Queries == nil {
		return nil, fmt.Errorf("memory service not configured")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.Queries.ListDueMemoryProviderDeliveries(ctx, db.ListDueMemoryProviderDeliveriesParams{
		WorkspaceID:   workspaceID,
		NextAttemptAt: pgtype.Timestamptz{Time: s.now(), Valid: true},
		Limit:         limit,
	})
	if err != nil {
		return nil, err
	}
	results := make([]MemoryDispatchResult, 0, len(rows))
	for _, row := range rows {
		result, dispatchErr := s.dispatchMemoryProviderDelivery(ctx, row.MemoryProviderDelivery, row.Envelope, row.EventType, row.EventCreatedAt)
		results = append(results, result)
		if dispatchErr != nil {
			return results, dispatchErr
		}
	}
	return results, nil
}

func (s *MemoryService) dispatchMemoryProviderDelivery(ctx context.Context, delivery db.MemoryProviderDelivery, raw []byte, eventType string, eventCreatedAt pgtype.Timestamptz) (MemoryDispatchResult, error) {
	result := MemoryDispatchResult{Provider: delivery.Provider, Delivery: delivery}
	var envelope MemoryEventEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		updated, updateErr := s.recordMemoryDeliveryFailure(ctx, delivery, eventCreatedAt, fmt.Errorf("decode memory envelope: %w", err))
		result.Delivery = updated
		result.Error = err.Error()
		if updateErr != nil {
			return result, updateErr
		}
		return result, nil
	}
	provider := s.memoryProvider(delivery.Provider)
	if provider == nil {
		err := fmt.Errorf("%w: provider %q is not registered", ErrMemoryConfig, delivery.Provider)
		updated, updateErr := s.recordMemoryDeliveryFailure(ctx, delivery, eventCreatedAt, err)
		result.Delivery = updated
		result.Error = err.Error()
		if updateErr != nil {
			return result, updateErr
		}
		return result, nil
	}

	providerResult, err := callMemoryProvider(ctx, provider, eventType, envelope)
	if err != nil {
		updated, updateErr := s.recordMemoryDeliveryFailure(ctx, delivery, eventCreatedAt, err)
		result.Delivery = updated
		result.Error = err.Error()
		if updateErr != nil {
			return result, updateErr
		}
		return result, nil
	}
	updated, err := s.recordMemoryDeliverySuccess(ctx, delivery, eventCreatedAt, providerResult)
	result.Delivery = updated
	return result, err
}

func (s *MemoryService) recordMemoryDeliverySuccess(ctx context.Context, delivery db.MemoryProviderDelivery, eventCreatedAt pgtype.Timestamptz, result MemoryProviderResult) (db.MemoryProviderDelivery, error) {
	response := result.Response
	if len(response) == 0 {
		response = json.RawMessage(`{}`)
	}
	now := s.now()
	updated, err := s.Queries.UpdateMemoryProviderDeliveryResult(ctx, db.UpdateMemoryProviderDeliveryResultParams{
		ID:               delivery.ID,
		WorkspaceID:      delivery.WorkspaceID,
		Status:           "delivered",
		AttemptCount:     delivery.AttemptCount + 1,
		NextAttemptAt:    pgtype.Timestamptz{},
		Response:         []byte(response),
		LastAttemptAt:    pgtype.Timestamptz{Time: now, Valid: true},
		TerminalAt:       pgtype.Timestamptz{},
		ProviderMemoryID: textValue(result.ProviderMemoryID),
		DeliveryLagMs:    deliveryLagMillis(eventCreatedAt, now),
		Error:            pgtype.Text{},
	})
	return updated, err
}

func (s *MemoryService) recordMemoryDeliveryFailure(ctx context.Context, delivery db.MemoryProviderDelivery, eventCreatedAt pgtype.Timestamptz, failure error) (db.MemoryProviderDelivery, error) {
	attemptCount := delivery.AttemptCount + 1
	status, nextAttemptAt, terminalAt := s.DeliveryFailureState(attemptCount)
	now := s.now()
	updated, err := s.Queries.UpdateMemoryProviderDeliveryResult(ctx, db.UpdateMemoryProviderDeliveryResultParams{
		ID:               delivery.ID,
		WorkspaceID:      delivery.WorkspaceID,
		Status:           status,
		AttemptCount:     attemptCount,
		NextAttemptAt:    nextAttemptAt,
		Response:         []byte(`{}`),
		LastAttemptAt:    pgtype.Timestamptz{Time: now, Valid: true},
		TerminalAt:       terminalAt,
		ProviderMemoryID: delivery.ProviderMemoryID,
		DeliveryLagMs:    deliveryLagMillis(eventCreatedAt, now),
		Error:            textValue(failure.Error()),
	})
	return updated, err
}

func (s *MemoryService) Recall(ctx context.Context, req MemoryRecallRequest) (MemoryRecallReadResult, error) {
	if s == nil || s.Queries == nil {
		return MemoryRecallReadResult{}, fmt.Errorf("memory service not configured")
	}
	if !req.Scope.WorkspaceID.Valid {
		return MemoryRecallReadResult{}, fmt.Errorf("%w: workspace_id is required", ErrMemoryConfig)
	}
	cfg, err := s.Queries.GetMemoryWorkspaceConfig(ctx, req.Scope.WorkspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MemoryRecallReadResult{}, ErrMemoryDisabled
		}
		return MemoryRecallReadResult{}, err
	}
	if !cfg.Enabled {
		return MemoryRecallReadResult{}, ErrMemoryDisabled
	}
	mode := strings.TrimSpace(req.ReadMode)
	if mode == "" {
		mode = strings.TrimSpace(cfg.ReadMode)
	}
	if mode == "" {
		mode = "primary"
	}
	if mode != "primary" && mode != "shadow" && mode != "dual" {
		return MemoryRecallReadResult{}, fmt.Errorf("%w: unsupported read_mode", ErrMemoryConfig)
	}

	primaryProvider := strings.TrimSpace(cfg.PrimaryProvider)
	if req.Provider != "" && req.ReadMode == "" {
		primaryProvider = strings.TrimSpace(req.Provider)
	}
	shadowProvider := strings.TrimSpace(cfg.ShadowProvider.String)
	correlationID := strings.TrimSpace(req.CorrelationID)
	if correlationID == "" {
		correlationID = "recall_" + uuid.NewString()
	}
	out := MemoryRecallReadResult{Mode: mode, CorrelationID: correlationID, Errors: map[string]string{}}
	recallOne := func(providerName string) (*MemoryProviderRecall, error) {
		recallReq := req
		recallReq.Provider = providerName
		recallReq.ReadMode = mode
		recallReq.CorrelationID = correlationID
		return s.recallProvider(ctx, recallReq)
	}

	switch mode {
	case "primary":
		if primaryProvider == "" {
			return out, fmt.Errorf("%w: primary_provider is required", ErrMemoryConfig)
		}
		out.Primary, err = recallOne(primaryProvider)
	case "shadow":
		if shadowProvider == "" {
			return out, fmt.Errorf("%w: shadow_provider is required", ErrMemoryConfig)
		}
		out.Shadow, err = recallOne(shadowProvider)
	case "dual":
		if primaryProvider == "" || shadowProvider == "" {
			return out, fmt.Errorf("%w: primary_provider and shadow_provider are required for dual read", ErrMemoryConfig)
		}
		if out.Primary, err = recallOne(primaryProvider); err != nil {
			out.Errors[primaryProvider] = err.Error()
		}
		if out.Shadow, err = recallOne(shadowProvider); err != nil {
			out.Errors[shadowProvider] = err.Error()
		}
		if out.Primary == nil && out.Shadow == nil && len(out.Errors) > 0 {
			return out, fmt.Errorf("%w: all recall providers failed", ErrMemoryConfig)
		}
		return out, nil
	}
	if err != nil {
		return out, err
	}
	return out, nil
}

func (s *MemoryService) recallProvider(ctx context.Context, req MemoryRecallRequest) (*MemoryProviderRecall, error) {
	providerName := strings.TrimSpace(req.Provider)
	provider := s.memoryProvider(providerName)
	if provider == nil {
		return nil, fmt.Errorf("%w: provider %q is not registered", ErrMemoryConfig, providerName)
	}
	result, err := provider.Recall(ctx, req)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Provider) == "" {
		result.Provider = providerName
	}
	sample, err := s.RecordRecallSample(ctx, req, result)
	if err != nil {
		return nil, err
	}
	return &MemoryProviderRecall{Provider: providerName, Result: result, Sample: sample}, nil
}

func (s *MemoryService) runInTx(ctx context.Context, fn func(*db.Queries) error) error {
	if s.TxStarter == nil {
		return fn(s.Queries)
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(s.Queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *MemoryService) RecordRecallSample(ctx context.Context, req MemoryRecallRequest, result MemoryRecallResult) (db.MemoryRecallSample, error) {
	if s == nil || s.Queries == nil {
		return db.MemoryRecallSample{}, fmt.Errorf("memory service not configured")
	}
	provider := strings.TrimSpace(result.Provider)
	if provider == "" {
		provider = strings.TrimSpace(req.Provider)
	}
	if provider == "" {
		return db.MemoryRecallSample{}, fmt.Errorf("%w: provider is required", ErrMemoryConfig)
	}
	results := result.Results
	if len(results) == 0 {
		results = json.RawMessage(`[]`)
	}
	provenance := result.Provenance
	if len(provenance) == 0 {
		provenance = json.RawMessage(`{}`)
	}
	return s.Queries.CreateMemoryRecallSample(ctx, db.CreateMemoryRecallSampleParams{
		WorkspaceID:         req.Scope.WorkspaceID,
		ProjectID:           req.Scope.ProjectID,
		AgentID:             req.Scope.AgentID,
		IssueID:             req.Scope.IssueID,
		TaskID:              req.Scope.TaskID,
		Provider:            provider,
		Query:               req.Query,
		Results:             []byte(results),
		Provenance:          []byte(provenance),
		SampledAt:           pgtype.Timestamptz{Time: s.now(), Valid: true},
		RecallCorrelationID: strings.TrimSpace(req.CorrelationID),
		ReadMode:            strings.TrimSpace(req.ReadMode),
	})
}

func (s *MemoryService) DeliveryFailureState(attemptCount int32) (status string, nextAttemptAt pgtype.Timestamptz, terminalAt pgtype.Timestamptz) {
	now := s.now()
	maxAttempts := s.MaxDeliveryAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if attemptCount >= maxAttempts {
		return "terminal_failed", pgtype.Timestamptz{}, pgtype.Timestamptz{Time: now, Valid: true}
	}
	base := s.BaseBackoff
	if base <= 0 {
		base = time.Minute
	}
	shift := attemptCount - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 5 {
		shift = 5
	}
	return "retry", pgtype.Timestamptz{Time: now.Add(base * time.Duration(1<<shift)), Valid: true}, pgtype.Timestamptz{}
}

func BuildMemoryEventEnvelope(req MemoryRetainRequest) (MemoryEventEnvelope, []byte, error) {
	content := req.Content
	if len(content) == 0 {
		content = json.RawMessage(`{}`)
	}
	if !json.Valid(content) {
		return MemoryEventEnvelope{}, nil, fmt.Errorf("%w: content must be valid JSON", ErrMemoryConfig)
	}
	eventType, err := normalizeMemoryEventType(req.EventType)
	if err != nil {
		return MemoryEventEnvelope{}, nil, err
	}
	correlationID := strings.TrimSpace(req.CorrelationID)
	sourceID := strings.TrimSpace(req.SourceID)
	if sourceID == "" {
		sourceID = memorySourceID(req)
	}
	envelope := MemoryEventEnvelope{
		SchemaVersion: 1,
		EventType:     eventType,
		CorrelationID: correlationID,
		SourceID:      sourceID,
		Scope: memoryScopeJSON{
			WorkspaceID: uuidString(req.Scope.WorkspaceID),
			ProjectID:   uuidString(req.Scope.ProjectID),
			AgentID:     uuidString(req.Scope.AgentID),
			IssueID:     uuidString(req.Scope.IssueID),
			TaskID:      uuidString(req.Scope.TaskID),
		},
		Actor: memoryActorJSON{
			Type: normalizeMemoryActorType(req.Actor.Type),
			ID:   uuidString(req.Actor.ID),
		},
		Content:  content,
		Metadata: req.Metadata,
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return MemoryEventEnvelope{}, nil, err
	}
	return envelope, raw, nil
}

func MemoryIdempotencyKey(envelope []byte) string {
	sum := sha256.Sum256(envelope)
	return "mem_" + hex.EncodeToString(sum[:])
}

func memoryEventFromUpsertRow(row db.UpsertMemoryEventRow) db.MemoryEvent {
	return db.MemoryEvent{
		ID:             row.ID,
		WorkspaceID:    row.WorkspaceID,
		ProjectID:      row.ProjectID,
		AgentID:        row.AgentID,
		IssueID:        row.IssueID,
		TaskID:         row.TaskID,
		ActorType:      row.ActorType,
		ActorID:        row.ActorID,
		EventType:      row.EventType,
		IdempotencyKey: row.IdempotencyKey,
		Envelope:       row.Envelope,
		Status:         row.Status,
		AttemptCount:   row.AttemptCount,
		AvailableAt:    row.AvailableAt,
		LastAttemptAt:  row.LastAttemptAt,
		TerminalAt:     row.TerminalAt,
		Error:          row.Error,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
}

func memoryDeliveryFromUpsertRow(row db.UpsertMemoryProviderDeliveryRow) db.MemoryProviderDelivery {
	return db.MemoryProviderDelivery{
		ID:               row.ID,
		WorkspaceID:      row.WorkspaceID,
		MemoryEventID:    row.MemoryEventID,
		Provider:         row.Provider,
		Status:           row.Status,
		AttemptCount:     row.AttemptCount,
		NextAttemptAt:    row.NextAttemptAt,
		LastAttemptAt:    row.LastAttemptAt,
		TerminalAt:       row.TerminalAt,
		ProviderMemoryID: row.ProviderMemoryID,
		Response:         row.Response,
		Error:            row.Error,
		DeliveryLagMs:    row.DeliveryLagMs,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
}

func (s *MemoryService) memoryProvider(provider string) MemoryProvider {
	provider = strings.TrimSpace(provider)
	if provider == "" || s == nil {
		return nil
	}
	if p := s.Providers[provider]; p != nil {
		return p
	}
	for _, p := range s.Providers {
		if p != nil && strings.TrimSpace(p.Name()) == provider {
			return p
		}
	}
	return nil
}

func callMemoryProvider(ctx context.Context, provider MemoryProvider, eventType string, envelope MemoryEventEnvelope) (MemoryProviderResult, error) {
	switch eventType {
	case "retain":
		return provider.Retain(ctx, envelope)
	case "update":
		return provider.Update(ctx, envelope)
	case "invalidate":
		return provider.Invalidate(ctx, envelope)
	case "delete":
		return provider.Delete(ctx, envelope)
	default:
		return MemoryProviderResult{}, fmt.Errorf("%w: unsupported event_type", ErrMemoryConfig)
	}
}

func deliveryProviders(cfg db.MemoryWorkspaceConfig) []string {
	seen := map[string]bool{}
	providers := make([]string, 0, 2)
	for _, provider := range []string{cfg.PrimaryProvider, cfg.ShadowProvider.String} {
		provider = strings.TrimSpace(provider)
		if provider == "" || seen[provider] {
			continue
		}
		seen[provider] = true
		providers = append(providers, provider)
	}
	return providers
}

func normalizeMemoryEventType(v string) (string, error) {
	switch strings.TrimSpace(v) {
	case "", "retain":
		return "retain", nil
	case "update", "invalidate", "delete":
		return strings.TrimSpace(v), nil
	default:
		return "", fmt.Errorf("%w: unsupported event_type", ErrMemoryConfig)
	}
}

func normalizeMemoryActorType(v string) string {
	switch strings.TrimSpace(v) {
	case "member", "agent", "system":
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func memorySourceID(req MemoryRetainRequest) string {
	for _, part := range []struct {
		prefix string
		id     pgtype.UUID
	}{
		{prefix: "task", id: req.Scope.TaskID},
		{prefix: "issue", id: req.Scope.IssueID},
		{prefix: "agent", id: req.Scope.AgentID},
		{prefix: "project", id: req.Scope.ProjectID},
		{prefix: "workspace", id: req.Scope.WorkspaceID},
	} {
		if part.id.Valid {
			return part.prefix + ":" + uuidString(part.id)
		}
	}
	return ""
}

func textValue(v string) pgtype.Text {
	v = strings.TrimSpace(v)
	if v == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: v, Valid: true}
}

func deliveryLagMillis(eventCreatedAt pgtype.Timestamptz, now time.Time) int64 {
	if !eventCreatedAt.Valid {
		return 0
	}
	lag := now.Sub(eventCreatedAt.Time)
	if lag < 0 {
		return 0
	}
	return lag.Milliseconds()
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return util.UUIDToString(u)
}

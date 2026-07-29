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
	Scope    MemoryScope
	Provider string
	Query    string
	Limit    int32
}

type MemoryRecallResult struct {
	Provider   string          `json:"provider"`
	Results    json.RawMessage `json:"results"`
	Provenance json.RawMessage `json:"provenance"`
}

type MemoryService struct {
	Queries             *db.Queries
	TxStarter           TxStarter
	Clock               func() time.Time
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
	if req.EventType == "" {
		req.EventType = "retain"
	}
	if req.EventType != "retain" && req.EventType != "update" && req.EventType != "invalidate" && req.EventType != "delete" {
		return MemoryRetainResult{}, fmt.Errorf("%w: unsupported event_type", ErrMemoryConfig)
	}
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

	envelope, raw, err := BuildMemoryEventEnvelope(req)
	if err != nil {
		return MemoryRetainResult{}, err
	}
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" {
		key = MemoryIdempotencyKey(raw)
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
		WorkspaceID: req.Scope.WorkspaceID,
		ProjectID:   req.Scope.ProjectID,
		AgentID:     req.Scope.AgentID,
		IssueID:     req.Scope.IssueID,
		TaskID:      req.Scope.TaskID,
		Provider:    provider,
		Query:       req.Query,
		Results:     []byte(results),
		Provenance:  []byte(provenance),
		SampledAt:   pgtype.Timestamptz{Time: s.now(), Valid: true},
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
	envelope := MemoryEventEnvelope{
		SchemaVersion: 1,
		EventType:     req.EventType,
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
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
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

func normalizeMemoryActorType(v string) string {
	switch strings.TrimSpace(v) {
	case "member", "agent", "system":
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return util.UUIDToString(u)
}

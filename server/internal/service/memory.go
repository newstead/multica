package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
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

type MemoryCaptureSource struct {
	SourceType string
	SourceID   pgtype.UUID
	Scope      MemoryScope
	Actor      MemoryActor
	Text       string
	Metadata   map[string]any
}

type MemoryRecallForTaskRequest struct {
	Scope       MemoryScope
	Query       string
	TokenBudget int
	Limit       int32
}

const (
	MemorySourceIssueDescription    = "issue_description"
	MemorySourceHumanComment        = "human_comment"
	MemorySourceAgentOutcomeSummary = "agent_outcome_summary"
	MemorySourceExplicitFeedback    = "explicit_feedback"
	MemorySourceMergedPRVerdict     = "merged_pr_verdict"

	defaultMemoryRecallTokenBudget = 600
	defaultMemoryRecallLimit       = 8
	maxMemoryRecallEventScan       = 200
	maxMemoryRecallTextTokens      = 220
)

var approvedMemorySources = map[string]bool{
	MemorySourceIssueDescription:    true,
	MemorySourceHumanComment:        true,
	MemorySourceAgentOutcomeSummary: true,
	MemorySourceExplicitFeedback:    true,
	MemorySourceMergedPRVerdict:     true,
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

func (s *MemoryService) RetainApprovedSource(ctx context.Context, src MemoryCaptureSource) (MemoryRetainResult, bool, error) {
	req, ok := BuildApprovedMemoryRetainRequest(src)
	if !ok {
		return MemoryRetainResult{}, false, nil
	}
	res, err := s.Retain(ctx, req)
	if errors.Is(err, ErrMemoryDisabled) {
		return MemoryRetainResult{}, true, nil
	}
	return res, true, err
}

func BuildApprovedMemoryRetainRequest(src MemoryCaptureSource) (MemoryRetainRequest, bool) {
	sourceType := strings.TrimSpace(src.SourceType)
	text := strings.TrimSpace(src.Text)
	if !approvedMemorySources[sourceType] || text == "" || containsMemoryDeniedContent(text) {
		return MemoryRetainRequest{}, false
	}
	content, err := json.Marshal(map[string]any{
		"source_type": sourceType,
		"source_id":   uuidString(src.SourceID),
		"text":        text,
	})
	if err != nil {
		return MemoryRetainRequest{}, false
	}
	metadata := map[string]any{
		"source_type": sourceType,
		"source_id":   uuidString(src.SourceID),
	}
	for k, v := range src.Metadata {
		if strings.TrimSpace(k) != "" {
			metadata[k] = v
		}
	}
	keyParts := []string{
		sourceType,
		uuidString(src.Scope.WorkspaceID),
		uuidString(src.Scope.ProjectID),
		uuidString(src.Scope.AgentID),
		uuidString(src.Scope.IssueID),
		uuidString(src.Scope.TaskID),
		uuidString(src.SourceID),
	}
	return MemoryRetainRequest{
		Scope:          src.Scope,
		Actor:          src.Actor,
		EventType:      "retain",
		IdempotencyKey: "memory_capture:" + strings.Join(keyParts, ":"),
		Content:        content,
		Metadata:       metadata,
	}, true
}

func containsMemoryDeniedContent(text string) bool {
	lower := strings.ToLower(text)
	denied := []string{
		"-----begin private key-----",
		"-----begin rsa private key-----",
		"aws_secret_access_key",
		"secret_access_key",
		"password=",
		"passwd=",
		"api_key=",
		"apikey=",
		"token=",
		"authorization: bearer",
		"raw shell output",
	}
	for _, needle := range denied {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func (s *MemoryService) RecallForTask(ctx context.Context, req MemoryRecallForTaskRequest) ([]protocol.MemoryRecallData, error) {
	if s == nil || s.Queries == nil {
		return nil, fmt.Errorf("memory service not configured")
	}
	if !req.Scope.WorkspaceID.Valid {
		return nil, fmt.Errorf("%w: workspace_id is required", ErrMemoryConfig)
	}
	cfg, err := s.Queries.GetMemoryWorkspaceConfig(ctx, req.Scope.WorkspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMemoryDisabled
		}
		return nil, err
	}
	if !cfg.Enabled {
		return nil, ErrMemoryDisabled
	}
	provider := strings.TrimSpace(cfg.PrimaryProvider)
	if provider == "" {
		return nil, fmt.Errorf("%w: primary provider is required when enabled", ErrMemoryConfig)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultMemoryRecallLimit
	}
	tokenBudget := req.TokenBudget
	if tokenBudget <= 0 {
		tokenBudget = defaultMemoryRecallTokenBudget
	}

	rows, err := s.Queries.ListMemoryEventsByWorkspace(ctx, db.ListMemoryEventsByWorkspaceParams{
		WorkspaceID: req.Scope.WorkspaceID,
		Limit:       maxMemoryRecallEventScan,
		Offset:      0,
	})
	if err != nil {
		return nil, err
	}
	recalledAt := s.now().Format(time.RFC3339)
	items := make([]protocol.MemoryRecallData, 0, len(rows))
	for _, row := range rows {
		item, ok := memoryRecallDataFromEvent(row, provider, recalledAt)
		if !ok || !memoryEventMatchesScope(row, req.Scope) {
			continue
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if scoreA, scoreB := memoryScopeSpecificity(a.Scope), memoryScopeSpecificity(b.Scope); scoreA != scoreB {
			return scoreA > scoreB
		}
		if a.CapturedAt != b.CapturedAt {
			return a.CapturedAt > b.CapturedAt
		}
		return a.MemoryID < b.MemoryID
	})

	out := make([]protocol.MemoryRecallData, 0, minInt(len(items), int(limit)))
	used := 0
	for _, item := range items {
		item.Text = truncateApproxTokens(item.Text, maxMemoryRecallTextTokens)
		cost := approxTokenCount(item.Text) + 24
		if cost > tokenBudget {
			item.Text = truncateApproxTokens(item.Text, maxInt(tokenBudget-24, 0))
			cost = approxTokenCount(item.Text) + 24
		}
		if strings.TrimSpace(item.Text) == "" || used+cost > tokenBudget {
			continue
		}
		out = append(out, item)
		used += cost
		if len(out) >= int(limit) {
			break
		}
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

type memoryEventContent struct {
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
	Text       string `json:"text"`
}

func memoryRecallDataFromEvent(row db.MemoryEvent, provider, recalledAt string) (protocol.MemoryRecallData, bool) {
	if row.EventType != "retain" || row.Status == "deleted" {
		return protocol.MemoryRecallData{}, false
	}
	var env MemoryEventEnvelope
	if err := json.Unmarshal(row.Envelope, &env); err != nil {
		return protocol.MemoryRecallData{}, false
	}
	var content memoryEventContent
	if err := json.Unmarshal(env.Content, &content); err != nil {
		return protocol.MemoryRecallData{}, false
	}
	if !approvedMemorySources[content.SourceType] || strings.TrimSpace(content.Text) == "" || containsMemoryDeniedContent(content.Text) {
		return protocol.MemoryRecallData{}, false
	}
	return protocol.MemoryRecallData{
		MemoryID: uuidString(row.ID),
		Provider: provider,
		Scope: protocol.MemoryRecallScope{
			WorkspaceID: uuidString(row.WorkspaceID),
			ProjectID:   uuidString(row.ProjectID),
			AgentID:     uuidString(row.AgentID),
			IssueID:     uuidString(row.IssueID),
			TaskID:      uuidString(row.TaskID),
		},
		SourceType: content.SourceType,
		SourceID:   content.SourceID,
		Text:       content.Text,
		CapturedAt: timestamptzString(row.CreatedAt),
		RecalledAt: recalledAt,
	}, true
}

func memoryEventMatchesScope(row db.MemoryEvent, scope MemoryScope) bool {
	if row.WorkspaceID != scope.WorkspaceID {
		return false
	}
	if row.ProjectID.Valid && (!scope.ProjectID.Valid || row.ProjectID != scope.ProjectID) {
		return false
	}
	if row.AgentID.Valid && (!scope.AgentID.Valid || row.AgentID != scope.AgentID) {
		return false
	}
	if row.IssueID.Valid && (!scope.IssueID.Valid || row.IssueID != scope.IssueID) {
		return false
	}
	if row.TaskID.Valid && (!scope.TaskID.Valid || row.TaskID != scope.TaskID) {
		return false
	}
	return true
}

func memoryScopeSpecificity(scope protocol.MemoryRecallScope) int {
	score := 0
	if scope.ProjectID != "" {
		score++
	}
	if scope.AgentID != "" {
		score++
	}
	if scope.IssueID != "" {
		score += 2
	}
	if scope.TaskID != "" {
		score++
	}
	return score
}

func approxTokenCount(text string) int {
	return len(strings.Fields(text))
}

func truncateApproxTokens(text string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	fields := strings.Fields(text)
	if len(fields) <= maxTokens {
		return strings.TrimSpace(text)
	}
	return strings.Join(fields[:maxTokens], " ") + " ..."
}

func timestamptzString(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

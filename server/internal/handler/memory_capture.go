package handler

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func (h *Handler) captureMemoryBestEffort(ctx context.Context, src service.MemoryCaptureSource) {
	if h == nil || h.MemoryService == nil || !featureflags.MemoryGatewayEnabled(ctx, h.FeatureFlags) {
		return
	}
	_, attempted, err := h.MemoryService.RetainApprovedSource(ctx, src)
	if err != nil && !errors.Is(err, service.ErrMemoryDisabled) {
		slog.Warn("memory capture failed", "source_type", src.SourceType, "error", err)
	}
	if !attempted {
		slog.Debug("memory capture skipped by policy", "source_type", src.SourceType)
	}
}

func issueMemoryScope(issue db.Issue, agentID pgtype.UUID) service.MemoryScope {
	return service.MemoryScope{
		WorkspaceID: issue.WorkspaceID,
		ProjectID:   issue.ProjectID,
		AgentID:     agentID,
		IssueID:     issue.ID,
	}
}

func optionalUUID(s string) pgtype.UUID {
	if s == "" {
		return pgtype.UUID{}
	}
	u, err := util.ParseUUID(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return u
}

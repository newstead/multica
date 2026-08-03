package handler

import (
	"net/http"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
)

// GetOpsMetrics returns the narrow workspace-scoped operations summary used by
// polling clients such as the macOS tray app. It intentionally excludes task
// context, results, errors, agent env, runtime metadata, and integration data.
func (h *Handler) GetOpsMetrics(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	workspaceUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace_id")
		return
	}

	resp, err := h.OpsMetricsService.Summary(r.Context(), workspaceUUID, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get ops metrics")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

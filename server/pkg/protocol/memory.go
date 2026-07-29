package protocol

// MemoryRecallScope is the non-secret scope citation attached to recalled
// memory. Empty fields mean that scope level was not present on the capture.
type MemoryRecallScope struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	IssueID     string `json:"issue_id,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
}

// MemoryRecallData is the claim payload the daemon may inject into a prompt.
// Text is intentionally carried only on the daemon-facing claim; task results
// store MemoryRecallProvenance so user-visible metadata can cite recall without
// duplicating recalled content.
type MemoryRecallData struct {
	MemoryID   string            `json:"memory_id"`
	Provider   string            `json:"provider"`
	Scope      MemoryRecallScope `json:"scope"`
	SourceType string            `json:"source_type"`
	SourceID   string            `json:"source_id,omitempty"`
	Text       string            `json:"text"`
	CapturedAt string            `json:"captured_at,omitempty"`
	RecalledAt string            `json:"recalled_at,omitempty"`
}

type MemoryRecallProvenance struct {
	MemoryID   string            `json:"memory_id"`
	Provider   string            `json:"provider"`
	Scope      MemoryRecallScope `json:"scope"`
	SourceType string            `json:"source_type"`
	SourceID   string            `json:"source_id,omitempty"`
	CapturedAt string            `json:"captured_at,omitempty"`
	RecalledAt string            `json:"recalled_at,omitempty"`
}

func MemoryRecallProvenanceFromData(items []MemoryRecallData) []MemoryRecallProvenance {
	if len(items) == 0 {
		return nil
	}
	out := make([]MemoryRecallProvenance, 0, len(items))
	for _, item := range items {
		out = append(out, MemoryRecallProvenance{
			MemoryID:   item.MemoryID,
			Provider:   item.Provider,
			Scope:      item.Scope,
			SourceType: item.SourceType,
			SourceID:   item.SourceID,
			CapturedAt: item.CapturedAt,
			RecalledAt: item.RecalledAt,
		})
	}
	return out
}

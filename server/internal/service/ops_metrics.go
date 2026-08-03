package service

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	defaultOpsMetricsTaskLimit    int32 = 20
	defaultOpsMetricsBlockerLimit int32 = 5
	defaultOpsMetricsStaleSeconds int32 = 60
)

type OpsMetricsService struct {
	Queries *db.Queries
}

func NewOpsMetricsService(queries *db.Queries) *OpsMetricsService {
	return &OpsMetricsService{Queries: queries}
}

type OpsMetricsSummary struct {
	GeneratedAt      string                  `json:"generated_at"`
	Server           OpsMetricsServerHealth  `json:"server"`
	IssueCounts      OpsMetricsIssueCounts   `json:"issue_counts"`
	RuntimeHealth    OpsMetricsRuntimeHealth `json:"runtime_health"`
	AgentCapacity    OpsMetricsAgentCapacity `json:"agent_capacity"`
	ActiveTaskCounts OpsMetricsTaskCounts    `json:"active_task_counts"`
	ActiveTasks      []OpsMetricsActiveTask  `json:"active_tasks"`
	RecentBlockers   []OpsMetricsBlocker     `json:"recent_blockers"`
}

type OpsMetricsServerHealth struct {
	Status string `json:"status"`
}

type OpsMetricsIssueCounts struct {
	Blocked    int64 `json:"blocked"`
	InProgress int64 `json:"in_progress"`
}

type OpsMetricsRuntimeHealth struct {
	Total      int64   `json:"total"`
	Online     int64   `json:"online"`
	Offline    int64   `json:"offline"`
	Stale      int64   `json:"stale"`
	LastSeenAt *string `json:"last_seen_at"`
}

type OpsMetricsAgentCapacity struct {
	TotalAgents  int64 `json:"total_agents"`
	ActiveAgents int64 `json:"active_agents"`
	IdleAgents   int64 `json:"idle_agents"`
	TotalSlots   int64 `json:"total_slots"`
	ActiveSlots  int64 `json:"active_slots"`
	IdleSlots    int64 `json:"idle_slots"`
}

type OpsMetricsTaskCounts struct {
	Queued                int64 `json:"queued"`
	Dispatched            int64 `json:"dispatched"`
	Running               int64 `json:"running"`
	WaitingLocalDirectory int64 `json:"waiting_local_directory"`
}

type OpsMetricsActiveTask struct {
	ID              string  `json:"id"`
	Status          string  `json:"status"`
	AgentID         string  `json:"agent_id"`
	AgentName       string  `json:"agent_name"`
	IssueID         *string `json:"issue_id"`
	IssueIdentifier *string `json:"issue_identifier"`
	IssueTitle      *string `json:"issue_title"`
	RuntimeID       *string `json:"runtime_id"`
	RuntimeStatus   *string `json:"runtime_status"`
	CreatedAt       string  `json:"created_at"`
	DispatchedAt    *string `json:"dispatched_at"`
	StartedAt       *string `json:"started_at"`
	WaitReason      *string `json:"wait_reason"`
}

type OpsMetricsBlocker struct {
	IssueID       string  `json:"issue_id"`
	Identifier    string  `json:"identifier"`
	Title         string  `json:"title"`
	Priority      string  `json:"priority"`
	Status        string  `json:"status"`
	BlockedReason *string `json:"blocked_reason"`
	WaitingOn     *string `json:"waiting_on"`
	UpdatedAt     string  `json:"updated_at"`
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *OpsMetricsService) Summary(ctx context.Context, workspaceID pgtype.UUID, generatedAt time.Time) (OpsMetricsSummary, error) {
	issueCounts, err := s.Queries.GetOpsMetricsIssueCounts(ctx, workspaceID)
	if err != nil {
		return OpsMetricsSummary{}, err
	}
	runtimeHealth, err := s.Queries.GetOpsMetricsRuntimeHealth(ctx, db.GetOpsMetricsRuntimeHealthParams{
		WorkspaceID:  workspaceID,
		StaleSeconds: float64(defaultOpsMetricsStaleSeconds),
	})
	if err != nil {
		return OpsMetricsSummary{}, err
	}
	capacity, err := s.Queries.GetOpsMetricsAgentCapacity(ctx, workspaceID)
	if err != nil {
		return OpsMetricsSummary{}, err
	}
	taskCounts, err := s.Queries.GetOpsMetricsActiveTaskCounts(ctx, workspaceID)
	if err != nil {
		return OpsMetricsSummary{}, err
	}
	tasks, err := s.Queries.ListOpsMetricsActiveTasks(ctx, db.ListOpsMetricsActiveTasksParams{
		WorkspaceID: workspaceID,
		LimitCount:  defaultOpsMetricsTaskLimit,
	})
	if err != nil {
		return OpsMetricsSummary{}, err
	}
	blockers, err := s.Queries.ListOpsMetricsRecentBlockers(ctx, db.ListOpsMetricsRecentBlockersParams{
		WorkspaceID: workspaceID,
		LimitCount:  defaultOpsMetricsBlockerLimit,
	})
	if err != nil {
		return OpsMetricsSummary{}, err
	}

	activeTasks := make([]OpsMetricsActiveTask, len(tasks))
	for i, row := range tasks {
		activeTasks[i] = OpsMetricsActiveTask{
			ID:              util.UUIDToString(row.ID),
			Status:          row.Status,
			AgentID:         util.UUIDToString(row.AgentID),
			AgentName:       row.AgentName,
			IssueID:         util.UUIDToPtr(row.IssueID),
			IssueIdentifier: stringPtr(row.IssueIdentifier),
			IssueTitle:      util.TextToPtr(row.IssueTitle),
			RuntimeID:       util.UUIDToPtr(row.RuntimeID),
			RuntimeStatus:   util.TextToPtr(row.RuntimeStatus),
			CreatedAt:       util.TimestampToString(row.CreatedAt),
			DispatchedAt:    util.TimestampToPtr(row.DispatchedAt),
			StartedAt:       util.TimestampToPtr(row.StartedAt),
			WaitReason:      util.TextToPtr(row.WaitReason),
		}
	}

	recentBlockers := make([]OpsMetricsBlocker, len(blockers))
	for i, row := range blockers {
		recentBlockers[i] = OpsMetricsBlocker{
			IssueID:       util.UUIDToString(row.ID),
			Identifier:    row.Identifier,
			Title:         row.Title,
			Priority:      row.Priority,
			Status:        row.Status,
			BlockedReason: stringPtr(row.BlockedReason),
			WaitingOn:     stringPtr(row.WaitingOn),
			UpdatedAt:     util.TimestampToString(row.UpdatedAt),
		}
	}

	activeSlots := capacity.ActiveSlots
	totalSlots := capacity.TotalSlots
	idleSlots := totalSlots - activeSlots
	if idleSlots < 0 {
		idleSlots = 0
	}

	return OpsMetricsSummary{
		GeneratedAt: generatedAt.UTC().Format(time.RFC3339),
		Server:      OpsMetricsServerHealth{Status: "ok"},
		IssueCounts: OpsMetricsIssueCounts{
			Blocked:    issueCounts.BlockedCount,
			InProgress: issueCounts.InProgressCount,
		},
		RuntimeHealth: OpsMetricsRuntimeHealth{
			Total:      runtimeHealth.TotalRuntimes,
			Online:     runtimeHealth.OnlineRuntimes,
			Offline:    runtimeHealth.OfflineRuntimes,
			Stale:      runtimeHealth.StaleRuntimes,
			LastSeenAt: util.TimestampToPtr(runtimeHealth.LastSeenAt),
		},
		AgentCapacity: OpsMetricsAgentCapacity{
			TotalAgents:  capacity.TotalAgents,
			ActiveAgents: capacity.ActiveAgents,
			IdleAgents:   capacity.IdleAgents,
			TotalSlots:   totalSlots,
			ActiveSlots:  activeSlots,
			IdleSlots:    idleSlots,
		},
		ActiveTaskCounts: OpsMetricsTaskCounts{
			Queued:                taskCounts.QueuedCount,
			Dispatched:            taskCounts.DispatchedCount,
			Running:               taskCounts.RunningCount,
			WaitingLocalDirectory: taskCounts.WaitingLocalDirectoryCount,
		},
		ActiveTasks:    activeTasks,
		RecentBlockers: recentBlockers,
	}, nil
}

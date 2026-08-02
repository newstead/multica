CREATE TABLE IF NOT EXISTS memory_dual_write_telemetry (
    memory_event_id UUID PRIMARY KEY REFERENCES memory_event(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

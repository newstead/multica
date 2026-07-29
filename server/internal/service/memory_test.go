package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
)

func TestMemoryEventEnvelopeIdempotencyIsStable(t *testing.T) {
	workspaceID := util.MustParseUUID("11111111-1111-1111-1111-111111111111")
	req := MemoryRetainRequest{
		Scope:     MemoryScope{WorkspaceID: workspaceID},
		Actor:     MemoryActor{Type: "system"},
		EventType: "retain",
		Content:   json.RawMessage(`{"text":"remember this"}`),
	}
	_, a, err := BuildMemoryEventEnvelope(req)
	if err != nil {
		t.Fatalf("BuildMemoryEventEnvelope: %v", err)
	}
	_, b, err := BuildMemoryEventEnvelope(req)
	if err != nil {
		t.Fatalf("BuildMemoryEventEnvelope second call: %v", err)
	}
	if MemoryIdempotencyKey(a) != MemoryIdempotencyKey(b) {
		t.Fatal("idempotency key must be stable for the same canonical envelope")
	}
}

func TestMemoryDeliveryFailureStateTerminalAfterMaxAttempts(t *testing.T) {
	fixed := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	svc := &MemoryService{
		Clock:               func() time.Time { return fixed },
		MaxDeliveryAttempts: 2,
		BaseBackoff:         time.Minute,
	}
	status, next, terminal := svc.DeliveryFailureState(1)
	if status != "retry" || !next.Valid || terminal.Valid {
		t.Fatalf("attempt 1 = status %q next %v terminal %v, want retry with next only", status, next, terminal)
	}
	status, next, terminal = svc.DeliveryFailureState(2)
	if status != "terminal_failed" || next.Valid || !terminal.Valid {
		t.Fatalf("attempt 2 = status %q next %v terminal %v, want terminal_failed with terminal_at", status, next, terminal)
	}
}

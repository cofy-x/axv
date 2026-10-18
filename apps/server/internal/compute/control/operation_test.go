package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cofy-x/axv/apps/server/internal/compute"
)

func TestOperationJournalPreservesPrivateInputsAcrossRecovery(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
	op := newOperation("private-image-request", "", "import-image")
	op.ResourceKind, op.ResourceID, op.Status = "image", "image-private", "running"
	op.Payload = json.RawMessage(`{"url":"https://example.invalid/image?token=private-secret","upload":"/private/upload"}`)
	if err := store.transaction(ctx, func(tx *sql.Tx) error {
		return insertOperation(ctx, tx, op, "fingerprint", op.ResourceID)
	}); err != nil {
		t.Fatal(err)
	}
	// Previous builds persist public fields and private payload in one flat object.
	// Retain that exact format when splitting their in-memory representations.
	legacy, err := json.Marshal(map[string]any{
		"id": op.ID, "requestId": op.RequestID, "machineId": "", "action": op.Action,
		"status": "running", "resourceKind": "image", "resourceId": op.ResourceID, "locks": []string{op.ResourceID},
		"createdAt": op.CreatedAt, "updatedAt": op.UpdatedAt, "payload": op.Payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "UPDATE operations SET data=? WHERE id=?", string(legacy), op.ID); err != nil {
		t.Fatal(err)
	}
	hostID := store.HostID
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if store.HostID != hostID {
		t.Fatal("reopening changed the host identity")
	}
	if err := store.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	claimed, found, err := store.Claim(ctx)
	if err != nil || !found || claimed.ID != op.ID || string(claimed.Payload) != string(op.Payload) {
		t.Fatalf("recovery lost private execution inputs: %+v, found=%t, err=%v", claimed, found, err)
	}
	if len(claimed.Locks) != 1 || claimed.Locks[0] != op.ResourceID {
		t.Fatalf("recovery lost the operation dependency lock: %v", claimed.Locks)
	}
	if err := store.Phase(ctx, op.ID, "downloading"); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err := store.db.QueryRowContext(ctx, "SELECT json_extract(data,'$.payload.url') FROM operations WHERE id=?", op.ID).Scan(&payload); err != nil || payload != "https://example.invalid/image?token=private-secret" {
		t.Fatalf("phase publication lost the private journal: %q, %v", payload, err)
	}
	service := &Service{store: store}
	result, err := service.Operation(ctx, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	byRequest, err := service.OperationByRequestID(ctx, op.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := service.Operations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{result, byRequest, operations, compute.ImageResult{Operation: result}} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "payload") || strings.Contains(string(data), "private-secret") || strings.Contains(string(data), "/private/upload") {
			t.Fatalf("service exposed private execution inputs: %s", data)
		}
	}
}

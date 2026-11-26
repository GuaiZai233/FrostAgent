package instance

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/backup"
	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/sessionstate"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/GuaiZai233/FrostAgent/internal/toolhistory"
)

func TestFutureModuleInstanceLifecycle(t *testing.T) {
	root := t.TempDir()
	m, err := NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	source, err := m.Create("Synthetic future modules")
	if err != nil || source.Error != "" {
		t.Fatalf("create: %+v %v", source, err)
	}
	p := storage.ModulePayload{ModuleID: "future.plugin", RecordID: "one", FormatVersion: 20, Data: []byte{0, 255, 128, 'a'}}
	p.SHA256 = storage.ModulePayloadDigest(p.Data)
	if err := m.db.SaveModulePayload(context.Background(), source.ID, p, nil); err != nil {
		t.Fatal(err)
	}
	history := toolhistory.Record{
		Scope: chathistory.Scope{Platform: "qq", MessageType: "private", TargetID: "synthetic-user"},
		RefID: toolhistory.NewRef(), RunID: "synthetic-run", ActorID: "synthetic-user",
		Tool: "synthetic-tool", State: "complete", OccurredAt: time.Now().UTC(),
	}
	if err := toolhistory.NewStore(m.db, source.ID).Save(context.Background(), history); err != nil {
		t.Fatal(err)
	}
	checkpoint := llm.SessionCheckpoint{Version: 1, ConversationID: "private:synthetic", UserPrompt: "preserved prompt"}
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the live checkpoint override, including revisions pending SQL persistence.
	if err := m.instances[source.ID].runtime.Engine.SessionManager.Checkpoints.Save(sessionstate.Record{
		SessionID: checkpoint.ConversationID, Revision: 4, Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.SQL.Exec(m.db.Bind(`UPDATE schema_meta SET version=?,min_compatible_version=20`), storage.SchemaVersion+3); err != nil {
		t.Fatal(err)
	}
	archive, err := m.InstanceZIP(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := m.RestoreInstanceZIP("Synthetic restored modules", archive)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Enabled {
		t.Fatal("restored instance enabled")
	}
	archive, err = m.InstanceZIP(restored.ID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := backup.DecodeInstanceZIP(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Modules) != 1 || !bytes.Equal(decoded.Modules[0].Data, p.Data) || !decoded.Modules[0].RequiresRebind {
		t.Fatal("unknown module changed")
	}
	if decoded.ToolHistory == nil || !reflect.DeepEqual(decoded.ToolHistory.Records, []toolhistory.Record{history}) {
		t.Fatal("tool history changed during module round trip")
	}
	if decoded.Sessions == nil || len(decoded.Sessions.Records) != 1 {
		t.Fatal("session checkpoint missing during module round trip")
	}
	record := decoded.Sessions.Records[0]
	var compact bytes.Buffer
	if err := json.Compact(&compact, record.Payload); err != nil ||
		record.SessionID != checkpoint.ConversationID || record.Revision != 4 || !bytes.Equal(compact.Bytes(), payload) {
		t.Fatalf("session checkpoint changed during module round trip: %+v %v", record, err)
	}
	if err := m.PrepareDeletion(restored.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfirmDeletion(restored.ID); err != nil {
		t.Fatal(err)
	}
	records, err := m.db.ModulePayloads(context.Background(), restored.ID)
	if err != nil || len(records) != 0 {
		t.Fatal("orphan module rows")
	}
	records, err = m.db.ModulePayloads(context.Background(), source.ID)
	if err != nil || len(records) != 1 {
		t.Fatal("source module affected by deletion")
	}
}

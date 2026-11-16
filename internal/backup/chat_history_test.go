package backup

import (
	"context"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
)

func TestChatHistoryImportValidatesAndRollsBack(t *testing.T) {
	db := computerUseBackupDatabase(t)
	record := chathistory.Record{Scope: chathistory.Scope{Platform: "qq", MessageType: "group", TargetID: "fixture-group"}, ID: "fixture-first", Role: "user", Content: "original", OccurredAt: time.Now().UTC(), DeliveryStatus: "received"}
	for _, mutate := range []func(*chathistory.Record){
		func(r *chathistory.Record) { r.Role = "system" },
		func(r *chathistory.Record) { r.DeliveryStatus = "delivered" },
		func(r *chathistory.Record) { r.Platform = "onebot" },
		func(r *chathistory.Record) { r.TargetID = "" },
		func(r *chathistory.Record) { r.OccurredAt = time.Time{} },
	} {
		invalid := record
		mutate(&invalid)
		if err := ImportChatHistory(db, "synthetic-target", ChatHistory{FormatVersion: FormatVersion, Records: []chathistory.Record{record, invalid}}); err == nil {
			t.Fatal("invalid archive accepted")
		}
	}
	// Fail only the second write and prove the first one is rolled back.
	if _, err := db.SQL.Exec(`CREATE TRIGGER reject_history BEFORE INSERT ON chat_history WHEN NEW.id='fixture-second' BEGIN SELECT RAISE(ABORT,'synthetic import failure'); END`); err != nil {
		t.Fatal(err)
	}
	second := record
	second.ID = "fixture-second"
	if err := ImportChatHistory(db, "synthetic-target", ChatHistory{FormatVersion: FormatVersion, Records: []chathistory.Record{record, second}}); err == nil {
		t.Fatal("injected import failure ignored")
	}
	got, err := chathistory.NewStore(db, "synthetic-target").Export(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("failed import left partial originals: %#v %v", got, err)
	}
}

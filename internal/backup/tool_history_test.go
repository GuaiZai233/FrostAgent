package backup

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/toolhistory"
)

func TestToolHistoryBackupRoundTripAndValidation(t *testing.T) {
	db := computerUseBackupDatabase(t)
	r := toolhistory.Record{Scope: chathistory.Scope{Platform: "qq", MessageType: "private", TargetID: "fixture-actor"}, RefID: toolhistory.NewRef(), RunID: "fixture-run", ActorID: "fixture-actor", Tool: "fixture-tool", ArgumentsRaw: `{"id":7700000000000000001}`, ResultRaw: `{"id":"7700000000000000001"}`, ModelResultRaw: "original", State: "complete", OccurredAt: time.Now().UTC()}
	data := ToolHistory{FormatVersion: FormatVersion, Records: []toolhistory.Record{r}}
	if err := ImportToolHistory(db, "synthetic-target", data); err != nil {
		t.Fatal(err)
	}
	got, err := ExportToolHistory(db, "synthetic-target")
	if err != nil || !reflect.DeepEqual(got, data) {
		t.Fatalf("round trip changed original: %+v %v", got, err)
	}
	zip, err := BuildInstanceZIP(db, "synthetic-target", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeInstanceZIP(zip)
	if err != nil || decoded.ToolHistory == nil || !reflect.DeepEqual(*decoded.ToolHistory, data) {
		t.Fatalf("ZIP lost tool originals: %+v %v", decoded.ToolHistory, err)
	}
	invalid := r
	invalid.RefID = "unprefixed"
	if err := ImportToolHistory(db, "synthetic-target", ToolHistory{FormatVersion: FormatVersion, Records: []toolhistory.Record{r, invalid}}); err == nil {
		t.Fatal("invalid tool reference accepted")
	}
	records, err := toolhistory.NewStore(db, "synthetic-target").Export(context.Background())
	if err != nil || len(records) != 1 {
		t.Fatalf("invalid import changed history: %v %v", records, err)
	}
}

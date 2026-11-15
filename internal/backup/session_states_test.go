package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/sessionstate"
)

func TestInstanceZIPIncludesPendingSessionCheckpoint(t *testing.T) {
	db := computerUseBackupDatabase(t)
	payload, err := json.Marshal(llm.SessionCheckpoint{Version: 1, ConversationID: "private:synthetic", SystemPrompt: "system", UserPrompt: "user", ActualRequest: json.RawMessage(`{"media":"data:image/png;base64,c3ludGhldGlj"}`)})
	if err != nil {
		t.Fatal(err)
	}
	records := []sessionstate.Record{{SessionID: "private:synthetic", Revision: 4, Payload: payload}}
	archive, err := BuildInstanceZIP(db, "synthetic-source", t.TempDir(), records)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeInstanceZIP(archive)
	if err != nil {
		t.Fatal(err)
	}
	for i := range decoded.Sessions.Records {
		var buffer bytes.Buffer
		if err := json.Compact(&buffer, decoded.Sessions.Records[i].Payload); err != nil {
			t.Fatal(err)
		}
		decoded.Sessions.Records[i].Payload = buffer.Bytes()
	}
	if decoded.Sessions == nil || !reflect.DeepEqual(decoded.Sessions.Records, records) {
		t.Fatal("pending local checkpoint missing from ZIP")
	}
	if err := ImportSessionStates(db, "synthetic-target", *decoded.Sessions); err != nil {
		t.Fatal(err)
	}
	got, err := ExportSessionStates(db, "synthetic-target")
	if err != nil || !reflect.DeepEqual(got.Records, records) {
		t.Fatalf("checkpoint import: %+v %v", got, err)
	}
	if err := ValidateSessionStates(SessionStates{FormatVersion: FormatVersion, Records: append(records, records...)}); err == nil {
		t.Fatal("duplicate checkpoint accepted")
	}
	var count int
	if err := db.SQL.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM session_states WHERE instance_id='synthetic-target'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("import changed another instance")
	}
}

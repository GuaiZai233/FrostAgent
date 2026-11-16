package instance

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/toolhistory"
)

func TestToolHistoryInstanceZIPRestoreAndLegacy(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	source, err := m.Create("Fixture tool source")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	record := toolhistory.Record{Scope: chathistory.Scope{Platform: "qq", MessageType: "private", TargetID: "fixture-user"},
		RefID: toolhistory.NewRef(), RunID: "fixture-run", ActorID: "fixture-user", Tool: "fixture-order", SourceMessageID: "fixture-source",
		ArgumentsRaw: `{"product":"fixture-matcha"}`, ResultRaw: `{"orderIdStr":"7700000000000000001"}`,
		State: "complete", OccurredAt: time.Now().UTC()}
	if err := toolhistory.NewStore(m.db, source.ID).Save(ctx, record); err != nil {
		t.Fatal(err)
	}
	archive, err := m.InstanceZIP(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := m.RestoreInstanceZIP("Fixture tool restored", archive)
	if err != nil {
		t.Fatal(err)
	}
	got, err := toolhistory.NewStore(m.db, restored.ID).Export(ctx)
	if err != nil || !reflect.DeepEqual(got, []toolhistory.Record{record}) {
		t.Fatalf("instance restore changed original/ref: %+v %v", got, err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	var old bytes.Buffer
	writer := zip.NewWriter(&old)
	for _, member := range reader.File {
		if member.Name == "tool_history.json" {
			continue
		}
		in, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		out, err := writer.Create(member.Name)
		if err != nil {
			in.Close()
			t.Fatal(err)
		}
		_, err = io.Copy(out, in)
		in.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	legacy, err := m.RestoreInstanceZIP("Fixture tool legacy", old.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got, err = toolhistory.NewStore(m.db, legacy.ID).Export(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("legacy backup did not restore with empty history: %+v %v", got, err)
	}
}

package instance

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/backup"
	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
)

func rewriteHistoryZIP(t *testing.T, archive, replacement []byte) []byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, member := range reader.File {
		if member.Name == "chat_history.json" && replacement == nil {
			continue
		}
		stream, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		if member.Name == "chat_history.json" {
			data = replacement
		}
		out, err := writer.Create(member.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestChatHistoryZIPRestoreAndLegacy(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	source, err := m.Create("Fixture history source")
	if err != nil {
		t.Fatal(err)
	}
	record := chathistory.Record{Scope: chathistory.Scope{Platform: "qq", MessageType: "private", TargetID: "fixture-user"}, ID: "fixture-record", MessageID: "fixture-platform-message",
		SenderID: "fixture-user", SenderName: "Fixture", Role: "user", Content: "原文\nquotes \" and literal %_!", ReplyTo: "fixture-reply", OccurredAt: time.UnixMilli(123456).UTC(), DeliveryStatus: "received"}
	if err := chathistory.NewStore(m.db, source.ID).Save(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	archive, err := m.InstanceZIP(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := backup.DecodeInstanceZIP(archive)
	if err != nil || decoded.ChatHistory == nil {
		t.Fatalf("ZIP history missing: %v", err)
	}
	restored, err := m.RestoreInstanceZIP("Fixture restored history", archive)
	if err != nil {
		t.Fatal(err)
	}
	got, err := chathistory.NewStore(m.db, restored.ID).Export(context.Background())
	if err != nil || !reflect.DeepEqual(got, []chathistory.Record{record}) {
		t.Fatalf("restore changed original: %#v %v", got, err)
	}
	legacy, err := m.RestoreInstanceZIP("Fixture legacy", rewriteHistoryZIP(t, archive, nil))
	if err != nil {
		t.Fatal(err)
	}
	got, err = chathistory.NewStore(m.db, legacy.ID).Export(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatal("legacy backup did not restore with empty history")
	}
	// Validate the entire archive before creating a target instance.
	before, _ := m.List()
	invalid := *decoded.ChatHistory
	invalid.Records = append(invalid.Records, record)
	payload, err := json.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.RestoreInstanceZIP("Fixture invalid", rewriteHistoryZIP(t, archive, payload)); err == nil {
		t.Fatal("duplicate records accepted")
	}
	after, _ := m.List()
	if len(before) != len(after) {
		t.Fatal("invalid archive created an instance")
	}
	original, err := chathistory.NewStore(m.db, source.ID).Export(context.Background())
	if err != nil || !reflect.DeepEqual(original, []chathistory.Record{record}) {
		t.Fatal("restore changed source")
	}
}

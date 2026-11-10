package astrbot

import (
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"strings"
	"testing"
)

func assertFailureReference(t *testing.T, store *logs.Store, reply, diagnostic string) {
	t.Helper()
	_, suffix, ok := strings.Cut(reply, " (Ref_ID: ")
	id := strings.TrimSuffix(suffix, ")")
	if _, valid := logs.NormalizeErrorUUID(id); !ok || !valid || !strings.HasPrefix(reply, "[FrostAgent 错误] ") || strings.ContainsAny(reply, "\r\n") {
		t.Fatalf("invalid error reply: %q", reply)
	}
	entry, found := store.FindError(id)
	if !found || !strings.Contains(entry.Content, diagnostic) {
		t.Fatalf("missing error diagnostics for %q: %+v", reply, entry)
	}
}

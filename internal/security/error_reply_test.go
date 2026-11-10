package security

import (
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/logs"
)

func TestRejectionReferenceRetainsDecisionInOwningStore(t *testing.T) {
	store := logs.New("security-instance", "test", 5)
	decision := WatchdogDecision{Action: WatchdogBlock, IsFailure: true, EvaluationID: "synthetic-eval",
		Reason: "Post https://endpoint.invalid/private: synthetic-secret", ErrorType: "synthetic-type", SafeSummary: "synthetic-detail"}
	reply := ReportRejection(store, RejectFailureMsg, decision, "synthetic-session")
	prefix := "[FrostAgent 错误] " + RejectFailureMsg + " (Ref_ID: "
	if !strings.HasPrefix(reply, prefix) || strings.Contains(reply, "endpoint.invalid") || strings.Contains(reply, "synthetic-secret") {
		t.Fatalf("unsafe rejection reply: %q", reply)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(reply, prefix), ")")
	entry, found := store.FindError(id)
	if !found || entry.TraceID != "synthetic-session" {
		t.Fatalf("missing security diagnostics: %+v", entry)
	}
	for _, diagnostic := range []string{decision.Reason, decision.EvaluationID, decision.ErrorType, decision.SafeSummary} {
		if !strings.Contains(entry.Content, diagnostic) {
			t.Fatalf("lost security diagnostic %q: %+v", diagnostic, entry)
		}
	}
	if _, found := logs.General.FindError(id); found {
		t.Fatal("security diagnostics crossed stores")
	}
}

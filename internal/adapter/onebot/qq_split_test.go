package onebot

import (
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

// When the first split segment is rejected by the platform, the send must not
// count as delivered: no segment is confirmed and the result is incomplete, even
// though the backend returned no Go error. Otherwise declare_final would end the
// turn with a missing first part.
func TestSplitPlainFirstRejectionIsIncompleteAndNotDelivered(t *testing.T) {
	calls := 0
	s := qqFixtureSender(func(action model.OneBotAction) (oneBotAPIResponse, error) {
		calls++
		return qqReject("synthetic rejection")
	})
	s.splitPlain = true
	result, err := s.send([]tools.Msg{
		{Type: "plain", Text: "第一段"},
		{Type: "plain", Text: "第二段"},
	})
	if err != nil {
		t.Fatalf("platform rejection is reported in results, not as a Go error: %v", err)
	}
	if result.MessagesSent != 0 {
		t.Fatalf("no segment was confirmed, MessagesSent=%d", result.MessagesSent)
	}
	if !result.Incomplete {
		t.Fatal("a rejected segment must mark the result incomplete")
	}
	if calls != 1 {
		t.Fatalf("later segments must not be attempted after a rejection, calls=%d", calls)
	}
}

// Every split segment that succeeds is counted and the result is complete.
func TestSplitPlainAllConfirmedIsComplete(t *testing.T) {
	s := qqFixtureSender(func(action model.OneBotAction) (oneBotAPIResponse, error) {
		return qqOK(1), nil
	})
	s.splitPlain = true
	result, err := s.send([]tools.Msg{
		{Type: "plain", Text: "第一段"},
		{Type: "plain", Text: "第二段"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MessagesSent != 2 || result.Incomplete {
		t.Fatalf("want 2 confirmed and complete, got sent=%d incomplete=%v", result.MessagesSent, result.Incomplete)
	}
}

// A quote prefix whose body sits in a later logical segment has no body in its
// own message, so the batch is rejected before anything is sent.
func TestSplitQuoteWithBodyInLaterSegmentIsRejected(t *testing.T) {
	calls := 0
	s := qqFixtureSender(func(action model.OneBotAction) (oneBotAPIResponse, error) {
		calls++
		return qqOK(1), nil
	})
	s.splitPlain = true
	_, err := s.prepare([]tools.Msg{
		{Type: "plain", Text: "first"},
		{Type: "file", URL: "https://example.invalid/doc.pdf"},
		{Type: "quote", MessageID: "101"},
		{Type: "plain", Text: "second"},
	})
	if err == nil {
		t.Fatal("a quote without its body in the same segment must be rejected in preflight")
	}
	if calls != 0 {
		t.Fatalf("preflight must not send, calls=%d", calls)
	}
}

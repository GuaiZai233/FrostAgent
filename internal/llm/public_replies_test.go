package llm

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestFinalProgressSuccessorTranscript(t *testing.T) {
	for _, prefix := range []string{"", "first", "first\n\nsecond"} {
		t.Run(fmt.Sprintf("prefix=%q", prefix), func(t *testing.T) {
			session := NewSessionManager().GetOrCreate("synthetic-final-sequence")
			turn := session.ReserveSequenceTurn("synthetic-sender", true, false)
			messages, _, _ := session.PrepareTurnMessages(turn, "first-input", "first-input", nil)
			messages = append(messages,
				ChatMessage{Role: "assistant", ToolCalls: []ToolCall{{ID: "synthetic-call", Type: "function", Function: ToolCallFunction{Name: "synthetic-tool", Arguments: "{}"}}}},
				ChatMessage{Role: "tool", ToolCallID: "synthetic-call", Content: "private-tool-result"},
				ChatMessage{Role: "assistant", Content: "first<FROSTAGENT_SEGMENT_MARK>second<FROSTAGENT_SEGMENT_MARK>unsent"})
			turn.saveTranscript(messages)
			log := NewPublicReplyLog(session, session.Epoch())
			if prefix != "" {
				log.RecordFinalProgress("first", turn)
				log.RecordFinalProgress(prefix, turn)
			}
			next := session.ReserveSequenceTurn("synthetic-sender", true, false)
			defer next.Done()
			log.Finish(true)
			turn.Done()
			next.Wait()
			got, _, _ := session.PrepareTurnMessages(next, "next-input", "next-input", nil)
			want := append([]ChatMessage(nil), messages[:3]...)
			if prefix != "" {
				want = append(want, ChatMessage{Role: "assistant", Content: prefix})
			}
			want = append(want, ChatMessage{Role: "user", Content: "next-input"})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("successor got duplicates, unsent suffix or wrong order:\ngot %+v\nwant %+v", got, want)
			}
			if strings.Contains(fmt.Sprint(session.Snapshot()), "private-tool-result") {
				t.Fatal("private trajectory entered public history")
			}
			// A late transport confirmation is public, but cannot replace the
			// new turn's private tool trajectory or append a duplicate prefix.
			checkpoint := cloneMessages(next.lane.transcript)
			log.RecordFinalProgress("first\n\nsecond\n\nlate-confirmed", turn)
			if !reflect.DeepEqual(checkpoint, next.lane.transcript) {
				t.Fatal("late progress overwrote successor transcript")
			}
			assistants := 0
			for _, message := range session.Snapshot() {
				if message.Role == "assistant" {
					assistants++
				}
			}
			if assistants != 1 {
				t.Fatalf("progress duplicated in public history: %+v", session.Snapshot())
			}
		})
	}
}

func TestFinalProgressCommitAndReset(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		session := NewSessionManager().GetOrCreate("synthetic-final-commit")
		log := NewPublicReplyLog(session, session.Epoch())
		log.RecordFinalProgress("first", nil)
		log.RecordFinalProgress("first\n\nlast", nil)
		log.Finish(interrupted)
		if !log.CommitFinal("first\n\nlast", interrupted) || log.CommitFinal("duplicate", interrupted) {
			t.Fatal("final commitment is not exactly once")
		}
		if got := session.Snapshot(); len(got) != 1 || got[0].Content != "first\n\nlast" {
			t.Fatalf("duplicate final: %+v", got)
		}
		stale := NewPublicReplyLog(session, session.Epoch())
		if err := session.ResetSession(nil); err != nil {
			t.Fatal(err)
		}
		stale.RecordFinalProgress("stale prefix", nil)
		if len(session.Snapshot()) != 0 {
			t.Fatal("old progress restored after reset")
		}
	}
}

func TestPublicReplyLogCommitDelivered(t *testing.T) {
	for _, phase := range []string{"fallback", "already final", "reset", "no delivery", "ordinary cleanup"} {
		t.Run(phase, func(t *testing.T) {
			session := NewSessionManager().GetOrCreate("group:fixture")
			log := NewPublicReplyLog(session, session.Epoch())
			if phase != "no delivery" {
				log.Record("confirmed reply", nil)
			}
			want := "confirmed reply"
			switch phase {
			case "already final":
				log.CommitFinal("different final", false)
				want = "different final"
			case "reset":
				if err := session.ResetSession(nil); err != nil {
					t.Fatal(err)
				}
				want = ""
			case "no delivery", "ordinary cleanup":
				want = ""
			}
			if phase != "ordinary cleanup" {
				log.CommitDelivered()
				log.CommitDelivered()
			}
			log.Finish(false)
			if phase == "reset" && log.CommitFinal("late after reset", true) {
				t.Fatal("stale final callback admitted after reset")
			}
			got := session.Snapshot()
			if want == "" {
				if len(got) != 0 {
					t.Fatalf("unexpected history: %+v", got)
				}
			} else if len(got) != 1 || got[0].Content != want {
				t.Fatalf("history=%+v want one %q", got, want)
			}
		})
	}
}

func TestPublicReplyLogConcurrentFinalAndFallback(t *testing.T) {
	for i := 0; i < 50; i++ {
		session := NewSessionManager().GetOrCreate("group:fixture-concurrent")
		log := NewPublicReplyLog(session, session.Epoch())
		log.Record("confirmed reply", nil)
		start := make(chan struct{})
		accepted := make(chan bool, 2)
		fallbackDone := make(chan struct{})
		go func() {
			<-start
			log.CommitDelivered()
			close(fallbackDone)
		}()
		for j := 0; j < 2; j++ {
			go func() {
				<-start
				accepted <- log.CommitFinal("confirmed reply", false)
			}()
		}
		close(start)
		first, second := <-accepted, <-accepted
		<-fallbackDone
		if first == second {
			t.Fatal("final callback side effects not admitted exactly once")
		}
		got := session.Snapshot()
		if len(got) != 1 || got[0].Content != "confirmed reply" {
			t.Fatalf("concurrent final/fallback history=%+v", got)
		}
	}
}

func TestPublicReplyLogLateFinal(t *testing.T) {
	for _, fallbackFirst := range []bool{false, true} {
		for _, distinct := range []bool{false, true} {
			session := NewSessionManager().GetOrCreate("group:fixture-late")
			log := NewPublicReplyLog(session, session.Epoch())
			log.Record("confirmed reply", nil)
			if fallbackFirst {
				log.CommitDelivered()
				log.Finish(true)
			}
			final := "confirmed reply"
			if distinct {
				final = "different visible final"
			}
			if !log.CommitFinal(final, distinct) {
				t.Fatal("first final callback was not admitted")
			}
			if log.CommitFinal(final, distinct) {
				t.Fatal("duplicate final callback admitted compaction/extraction twice")
			}
			log.CommitDelivered()
			log.Finish(true)
			got := session.Snapshot()
			want := 1
			if distinct {
				want = 2
			}
			if len(got) != want || got[0].Content != "confirmed reply" || got[len(got)-1].Content != final {
				t.Fatalf("fallbackFirst=%v distinct=%v history=%+v", fallbackFirst, distinct, got)
			}
		}
	}
}

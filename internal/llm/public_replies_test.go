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
			// Interrupt the successor again. The late confirmation must be
			// incorporated now, not prepended from untracked shared history.
			next.saveTranscript(append(got, ChatMessage{Role: "assistant", Content: "unsent successor answer"}))
			third := session.ReserveSequenceTurn("synthetic-sender", true, false)
			defer third.Done()
			next.Done()
			third.Wait()
			inherited, _, _ := session.PrepareTurnMessages(third, "third-input", "third-input", nil)
			want = append([]ChatMessage(nil), messages[:3]...)
			want = append(want,
				ChatMessage{Role: "assistant", Content: "first\n\nsecond\n\nlate-confirmed"},
				ChatMessage{Role: "user", Content: "next-input"},
				ChatMessage{Role: "user", Content: "third-input"})
			if !reflect.DeepEqual(inherited, want) {
				t.Fatalf("late progress corrupted the next handoff:\ngot %+v\nwant %+v", inherited, want)
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

func TestFinalProgressMultipleLateTurns(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%v", reverse), func(t *testing.T) {
			session := NewSessionManager().GetOrCreate("synthetic-multiple-late")
			prepare := func(turn *SessionTurn, input string) []ChatMessage {
				turn.Wait()
				messages, _, _ := session.PrepareTurnMessages(turn, input, input, nil)
				messages = append(messages,
					ChatMessage{Role: "assistant", ToolCalls: []ToolCall{{ID: input, Type: "function", Function: ToolCallFunction{Name: "synthetic", Arguments: "{}"}}}},
					ChatMessage{Role: "tool", ToolCallID: input, Content: input + "-result"})
				turn.saveTranscript(append(messages, ChatMessage{Role: "assistant", Content: "unsent"}))
				return messages
			}
			first := session.ReserveSequenceTurn("synthetic-sender", true, false)
			firstMessages := prepare(first, "input-one")
			firstLog := NewPublicReplyLog(session, session.Epoch())
			second := session.ReserveSequenceTurn("synthetic-sender", true, false)
			first.Done()
			prepare(second, "input-two")
			secondLog := NewPublicReplyLog(session, session.Epoch())
			secondLog.RecordFinalProgress("two-prefix", second)
			third := session.ReserveSequenceTurn("synthetic-sender", true, false)
			second.Done()
			thirdMessages := prepare(third, "input-three")
			checkpoint := cloneMessages(third.lane.transcript)
			if reverse {
				secondLog.RecordFinalProgress("two-prefix\n\ntwo-late", second)
				firstLog.RecordFinalProgress("one-late", first)
			} else {
				firstLog.RecordFinalProgress("one-late", first)
				secondLog.RecordFinalProgress("two-prefix\n\ntwo-late", second)
			}
			if !reflect.DeepEqual(checkpoint, third.lane.transcript) {
				t.Fatal("late confirmations changed the active transcript")
			}
			// Saving the current model checkpoint must not discard queued facts.
			third.saveTranscript(thirdMessages)
			fourth := session.ReserveSequenceTurn("synthetic-sender", true, false)
			third.Done()
			fourth.Wait()
			got, _, _ := session.PrepareTurnMessages(fourth, "input-four", "input-four", nil)
			want := append([]ChatMessage(nil), firstMessages...)
			want = append(want, ChatMessage{Role: "assistant", Content: "one-late"})
			want = append(want, thirdMessages[3:6]...)
			want = append(want, ChatMessage{Role: "assistant", Content: "two-prefix\n\ntwo-late"})
			want = append(want, thirdMessages[7:]...)
			want = append(want, ChatMessage{Role: "user", Content: "input-four"})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("out-of-order late confirmations:\ngot %+v\nwant %+v", got, want)
			}
			fourth.saveTranscript(got)
			firstLog.RecordFinalProgress("one-late\n\none-latest", first)
			secondLog.RecordFinalProgress("two-prefix\n\ntwo-latest", second)
			fifth := session.ReserveSequenceTurn("synthetic-sender", true, false)
			fourth.Done()
			fifth.Wait()
			got, _, _ = session.PrepareTurnMessages(fifth, "input-five", "input-five", nil)
			want[3].Content = "one-late\n\none-latest"
			want[7].Content = "two-prefix\n\ntwo-latest"
			want = append(want, ChatMessage{Role: "user", Content: "input-five"})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("shifted checkpoint indices corrupted subsequent progress:\ngot %+v\nwant %+v", got, want)
			}
			fifth.Done()
			if len(fifth.lane.transcriptTurns) != 0 {
				t.Fatal("completed task retained delivery checkpoints")
			}
		})
	}
}

func TestFinalProgressLateTaskBoundaries(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprintf("reset=%v", reset), func(t *testing.T) {
			session := NewSessionManager().GetOrCreate("synthetic-late-boundary")
			first := session.ReserveSequenceTurn("synthetic-sender", true, false)
			messages, _, _ := session.PrepareTurnMessages(first, "first", "first", nil)
			first.saveTranscript(messages)
			log := NewPublicReplyLog(session, session.Epoch())
			second := session.ReserveSequenceTurn("synthetic-sender", true, false)
			first.Done()
			second.Wait()
			messages, _, _ = session.PrepareTurnMessages(second, "second", "second", nil)
			log.RecordFinalProgress("late", first)
			second.saveTranscript(messages)
			if reset {
				if err := session.ResetSession(nil); err != nil {
					t.Fatal(err)
				}
			}
			second.Done()
			third := session.ReserveSequenceTurn("synthetic-sender", true, false)
			defer third.Done()
			third.Wait()
			messages, _, _ = session.PrepareTurnMessages(third, "third", "third", nil)
			checkpoint := cloneMessages(third.lane.transcript)
			log.RecordFinalProgress("late-updated", first)
			if !reflect.DeepEqual(checkpoint, third.lane.transcript) || len(third.lane.transcriptTurns) != 1 {
				t.Fatal("old task progress entered new task checkpoint")
			}
			want := []ChatMessage{{Role: "user", Content: "third"}}
			if !reset {
				want = append([]ChatMessage{{Role: "user", Content: "first"}, {Role: "user", Content: "second"}, {Role: "assistant", Content: "late"}}, want...)
			}
			if !reflect.DeepEqual(messages, want) {
				t.Fatalf("wrong task/reset boundary: %+v", messages)
			}
			if reset && len(session.Snapshot()) != 1 {
				t.Fatal("old epoch restored public history")
			}
		})
	}
}

func TestFinalProgressLateConfirmationRacesPreparation(t *testing.T) {
	for i := 0; i < 25; i++ {
		session := NewSessionManager().GetOrCreate("synthetic-late-race")
		first := session.ReserveSequenceTurn("synthetic-sender", true, false)
		messages, _, _ := session.PrepareTurnMessages(first, "first", "first", nil)
		first.saveTranscript(messages)
		log := NewPublicReplyLog(session, session.Epoch())
		second := session.ReserveSequenceTurn("synthetic-sender", true, false)
		first.Done()
		second.Wait()
		messages, _, _ = session.PrepareTurnMessages(second, "second", "second", nil)
		second.saveTranscript(messages)
		third := session.ReserveSequenceTurn("synthetic-sender", true, false)
		second.Done()
		start, confirmed := make(chan struct{}), make(chan struct{})
		prepared := make(chan []ChatMessage, 1)
		go func() {
			<-start
			log.RecordFinalProgress("late-first", first)
			close(confirmed)
		}()
		go func() {
			<-start
			third.Wait()
			got, _, _ := session.PrepareTurnMessages(third, "third", "third", nil)
			prepared <- got
		}()
		close(start)
		got := receiveInjection(t, prepared)
		receiveInjection(t, confirmed)
		before := []ChatMessage{{Role: "user", Content: "first"}, {Role: "user", Content: "second"}, {Role: "user", Content: "third"}}
		after := []ChatMessage{{Role: "user", Content: "first"}, {Role: "assistant", Content: "late-first"}, {Role: "user", Content: "second"}, {Role: "user", Content: "third"}}
		if !reflect.DeepEqual(got, before) && !reflect.DeepEqual(got, after) {
			t.Fatalf("non-atomic preparation: %+v", got)
		}
		third.saveTranscript(got)
		fourth := session.ReserveSequenceTurn("synthetic-sender", true, false)
		third.Done()
		fourth.Wait()
		got, _, _ = session.PrepareTurnMessages(fourth, "fourth", "fourth", nil)
		want := append(after, ChatMessage{Role: "user", Content: "fourth"})
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("confirmation lost across preparation race: %+v", got)
		}
		fourth.Done()
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

// A terminal silence after a confirmed delivery (send_message, or any future
// tool that records via the log) must keep the delivered text in history, and
// a silence without delivery must leave history untouched.
func TestPublicReplyLogSilentExitKeepsDeliveredText(t *testing.T) {
	session := NewSessionManager().GetOrCreate("group:fixture-silent")
	log := NewPublicReplyLog(session, session.Epoch())
	log.Record("delivered before silence", nil)
	log.CommitDelivered()
	log.Finish(false)
	log.CommitDelivered()

	got := session.Snapshot()
	if len(got) != 1 || got[0].Role != "assistant" || got[0].Content != "delivered before silence" {
		t.Fatalf("history=%+v want one assistant entry with the delivered text", got)
	}
}

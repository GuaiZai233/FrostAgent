package llm

import "testing"

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

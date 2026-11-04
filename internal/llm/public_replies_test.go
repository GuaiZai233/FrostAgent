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

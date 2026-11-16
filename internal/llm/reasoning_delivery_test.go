package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/api"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/state"
)

func TestConfirmedReasoningSurvivesCheckpointAndCompaction(t *testing.T) {
	for _, compact := range []bool{false, true} {
		t.Run(fmt.Sprintf("compact=%v", compact), func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls > 1 {
					var body struct {
						Messages []struct {
							Role      string  `json:"role"`
							Reasoning *string `json:"reasoning_content"`
						} `json:"messages"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					count := 0
					for _, message := range body.Messages {
						if message.Role != "assistant" {
							continue
						}
						count++
						want := "synthetic-reasoning"
						if compact {
							want = ""
						}
						if message.Reasoning == nil || *message.Reasoning != want {
							t.Errorf("restored reasoning=%v want=%q", message.Reasoning, want)
							http.Error(w, "missing reasoning_content", http.StatusBadRequest)
							return
						}
					}
					if count != 1 {
						t.Errorf("assistant count=%d", count)
					}
				}
				fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"answer","reasoning_content":"synthetic-reasoning"}}]}`)
			}))
			defer upstream.Close()
			client := api.NewClient(upstream.URL, "", api.ChatCompletions, 0)
			first, err := client.Chat(context.Background(), core.ChatRequest{Model: "synthetic-model", Messages: []core.ChatMessage{{Role: core.RoleUser, Content: "input"}}})
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			session := persistentManager(t, dir).GetOrCreate("private:synthetic")
			session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "input"})
			log := NewPublicReplyLog(session, session.Epoch())
			log.SetFinalMessage(&first.Message)
			log.RecordFinalProgress("answer", nil)
			if !log.CommitFinal("answer", false) {
				t.Fatal("final not committed")
			}
			restored := persistentManager(t, dir).GetOrCreate("private:synthetic")
			messages := restored.Messages()
			if !reflect.DeepEqual(messages[1].ProviderState, first.Message.ProviderState) {
				t.Fatal("checkpoint lost original provider state")
			}
			if compact {
				snapshot, err := restored.SnapshotPrivateCompact()
				if err != nil {
					t.Fatal(err)
				}
				if !restored.CommitPrivateCompact(snapshot, "synthetic summary") {
					t.Fatal("compact failed")
				}
				// The synthetic acknowledgement and its lack of real state must
				// survive restart; the protocol layer supplies a wire-only value.
				restored = persistentManager(t, dir).GetOrCreate("private:synthetic")
				if restored.Messages()[1].ProviderState != nil {
					t.Fatal("synthetic acknowledgement gained real reasoning state")
				}
			}
			if _, err := client.Chat(context.Background(), core.ChatRequest{Model: "synthetic-model", Messages: restored.Messages(), Tools: []core.Tool{{Name: "lookup"}}}); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("requests=%d", calls)
			}
		})
	}
}

func TestFinalReasoningLateConfirmationAndReset(t *testing.T) {
	session := NewSessionManager().GetOrCreate("private:synthetic-final")
	turn := session.ReserveSequenceTurn("synthetic-sender", true, false)
	messages, _, _ := session.PrepareTurnMessages(turn, "first-input", "first-input", nil)
	final := core.ChatMessage{Role: core.RoleAssistant, Content: "first-second"}
	state.Remember(&final, "openai_chat_completions", "synthetic-model", "synthetic-reasoning", state.Origin{URL: "https://synthetic.invalid/chat/completions"})
	messages = append(messages, ChatMessage{Role: "assistant", Content: final.Content, ProviderState: final.ProviderState})
	turn.saveTranscript(messages)
	log := NewPublicReplyLog(session, session.Epoch())
	log.SetFinalMessage(&final)
	// An unconfirmed model response must not enter public history.
	if len(session.Snapshot()) != 1 {
		t.Fatal("unconfirmed final entered history")
	}
	log.RecordFinalProgress("first", turn)
	if session.Snapshot()[1].ProviderState != nil {
		t.Fatal("partial reply inherited full-response state")
	}
	next := session.ReserveSequenceTurn("synthetic-sender", true, false)
	turn.Done()
	next.Wait()
	inherited, _, _ := session.PrepareTurnMessages(next, "next-input", "next-input", nil)
	if inherited[1].ProviderState != nil || inherited[1].Content != "first" {
		t.Fatal("successor inherited unsent content or state")
	}
	before := cloneMessages(next.lane.transcript)
	log.RecordFinalProgress("first-second", turn)
	if !reflect.DeepEqual(before, next.lane.transcript) {
		t.Fatal("late ACK rewrote running successor")
	}
	next.saveTranscript(inherited)
	third := session.ReserveSequenceTurn("synthetic-sender", true, false)
	defer third.Done()
	next.Done()
	third.Wait()
	inherited, _, _ = session.PrepareTurnMessages(third, "third-input", "third-input", nil)
	if inherited[1].Content != final.Content || !reflect.DeepEqual(inherited[1].ProviderState, final.ProviderState) {
		t.Fatal("late full ACK lost final state at preparation boundary")
	}
	if !log.CommitFinal("first-second", true) {
		t.Fatal("late final not committed")
	}
	if !reflect.DeepEqual(session.Snapshot()[1].ProviderState, final.ProviderState) {
		t.Fatal("public late final lost state")
	}
	if err := session.ResetSession(nil); err != nil {
		t.Fatal(err)
	}
	log.RecordFinalProgress("first-second", turn)
	log.CommitFinal("first-second", true)
	if len(session.Snapshot()) != 0 {
		t.Fatal("late reasoning confirmation revived reset history")
	}
}

func TestFinalReasoningSourceIsImmutableAndExact(t *testing.T) {
	for _, mode := range []string{"exact", "rewritten", "tool-call", "wrong-role"} {
		t.Run(mode, func(t *testing.T) {
			source := core.ChatMessage{Role: core.RoleAssistant, Content: "original"}
			state.Remember(&source, "openai_chat_completions", "synthetic-model", "synthetic-reasoning", state.Origin{URL: "https://synthetic.invalid/chat/completions"})
			want := *source.ProviderState
			want.Content = append(json.RawMessage(nil), want.Content...)
			if mode == "tool-call" {
				source.ToolCalls = []core.ToolCall{{ID: "synthetic-call"}}
			}
			if mode == "wrong-role" {
				source.Role = core.RoleUser
			}
			session := NewSessionManager().GetOrCreate("private:synthetic-source")
			log := NewPublicReplyLog(session, session.Epoch())
			log.SetFinalMessage(&source)
			source.ProviderState.Content[0] = 'x'
			source.ProviderState.Model = "mutated-model"
			text := "original"
			if mode == "rewritten" {
				text = "filtered"
			}
			if !log.CommitFinal(text, false) {
				t.Fatal("final not committed")
			}
			got := session.Snapshot()[0].ProviderState
			if mode == "exact" {
				if !reflect.DeepEqual(got, &want) {
					t.Fatal("source mutation altered retained final state")
				}
			} else if got != nil {
				t.Fatal("changed content/role/calls inherited reasoning")
			}
		})
	}
}

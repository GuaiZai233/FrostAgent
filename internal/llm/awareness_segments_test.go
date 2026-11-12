package llm

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
)

func TestAwarenessFinalReplySegments(t *testing.T) {
	protected := "`" + replyformat.SegmentMark + "`"
	for _, kind := range []string{"private", "group"} {
		for _, tc := range []struct {
			name, output string
			enabled      bool
			limit        string
			want         []string
		}{
			{"markers after blank lines", "first\n\n" + replyformat.SegmentMark + "second\n\n" + replyformat.SegmentMark + "third", true, "3", []string{"first", "second", "third"}},
			{"disabled", "first" + replyformat.SegmentMark + "second\n\n" + protected, false, "1", []string{"firstsecond\n\n" + protected}},
			{"protected code", "first" + replyformat.SegmentMark + protected + "\n\n```text\n" + replyformat.SegmentMark + "\n```", true, "0", []string{"first", protected, "```text\n" + replyformat.SegmentMark + "\n```"}},
			{"marker only", replyformat.SegmentMark, true, "0", nil},
			{"disabled marker only", replyformat.SegmentMark, false, "0", nil},
			{"limited", "first" + replyformat.SegmentMark + "second" + replyformat.SegmentMark + "third", true, "1", nil},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				e, service, config, sends := awarenessFixture(t)
				for key, value := range map[string]string{replyformat.EnvKey: strconv.FormatBool(tc.enabled), replyformat.LimitEnvKey: tc.limit} {
					if err := config.Update(key, value, false); err != nil {
						t.Fatal(err)
					}
				}
				e.Provider = injectionProvider(func(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
					return finalInjection(tc.output), nil
				})
				target := AwarenessTargetFor("fixture-platform", kind, "fixture-target")
				service.Trigger(e.Context(), target)
				var got []string
				for _, send := range *sends {
					if len(send) != 1 || send[0].Type != "plain" {
						t.Fatalf("unexpected chain: %+v", send)
					}
					got = append(got, send[0].Text)
				}
				if tc.name == "limited" {
					if len(got) != 1 || (got[0] != "first" && got[0] != "second" && got[0] != "third") {
						t.Fatalf("limit ignored: %q", got)
					}
				} else if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("sends=%q want=%q", got, tc.want)
				}
				session := e.SessionManager.GetOrCreate(target.SessionID)
				history := session.Snapshot()
				if len(got) == 0 {
					if len(history) != 0 {
						t.Fatalf("unsent history: %+v", history)
					}
				} else if len(history) != 1 || history[0].Role != "assistant" || history[0].Content != strings.Join(got, "\n") {
					t.Fatalf("history does not match confirmed sends: %+v", history)
				}
				if kind == "group" {
					compact := session.SnapshotGroupContext(20, 12000, "").RecentStructuredMessages
					if len(compact) != len(got) {
						t.Fatalf("compact=%+v sends=%q", compact, got)
					}
					for i, msg := range compact {
						if msg.Content != got[i] {
							t.Fatalf("compact[%d]=%q want=%q", i, msg.Content, got[i])
						}
					}
				}
			})
		}
	}
}

func TestAwarenessSegmentsStopAfterDeliveryFailureOrRevocation(t *testing.T) {
	for _, action := range []string{"failure", "disable", "cancel", "reset"} {
		t.Run(action, func(t *testing.T) {
			e, service, config, _ := awarenessFixture(t)
			if err := config.Update(replyformat.EnvKey, "true", false); err != nil {
				t.Fatal(err)
			}
			target := AwarenessTargetFor("fixture-platform", "group", "fixture-target")
			session := e.SessionManager.GetOrCreate(target.SessionID)
			ctx, cancel := context.WithCancel(e.Context())
			defer cancel()
			calls := 0
			service.Resolve = func(AwarenessTarget) (AwarenessSender, bool) {
				return func(_ context.Context, _ AwarenessTarget, parts []proactive.AwarenessMessage, _ *SessionTurn) error {
					calls++
					if calls > 2 || len(parts) != 1 || parts[0].Text != []string{"first", "second"}[calls-1] {
						t.Fatalf("unexpected send: %+v", parts)
					}
					if action == "failure" && calls == 2 {
						return errors.New("fixture delivery failure")
					}
					if calls == 1 {
						switch action {
						case "disable":
							if err := config.Update(proactive.AwarenessEnabled, "false", false); err != nil {
								t.Fatal(err)
							}
						case "cancel":
							cancel()
						case "reset":
							if err := e.SessionManager.ResetSession(target.SessionID); err != nil {
								t.Fatal(err)
							}
						}
					}
					return nil
				}, true
			}
			e.Provider = injectionProvider(func(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
				return finalInjection("first" + replyformat.SegmentMark + "second" + replyformat.SegmentMark + "third"), nil
			})
			service.Trigger(ctx, target)
			wantCalls := 1
			if action == "failure" {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Fatalf("sends=%d want=%d", calls, wantCalls)
			}
			history := session.Snapshot()
			compact := session.SnapshotGroupContext(20, 12000, "").RecentStructuredMessages
			if action == "reset" {
				if len(history) != 0 || len(compact) != 0 {
					t.Fatalf("reset revived history: %+v / %+v", history, compact)
				}
			} else if len(history) != 1 || history[0].Content != "first" || len(compact) != 1 || compact[0].Content != "first" {
				t.Fatalf("unconfirmed suffix recorded: %+v / %+v", history, compact)
			}
		})
	}
}

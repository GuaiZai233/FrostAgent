package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
)

func TestSendMessageDecorationPreferenceSurvivesDeliveryRoutes(t *testing.T) {
	for _, media := range []bool{false, true} {
		for _, preference := range []string{"omitted", "true", "false"} {
			t.Run(fmt.Sprintf("%s/media=%v", preference, media), func(t *testing.T) {
				messages := []Msg{{Type: "plain", Text: " \tfixture\n "}}
				run := llm.RunContext{}
				if media {
					messages = append(messages, Msg{Type: "image", URL: "https://example.invalid/image.png"})
					run.PlatformActionHook = func(context.Context, string) (core.PlatformActionResult, error) {
						t.Fatal("tool execution must leave delivery to the engine")
						return core.PlatformActionResult{}, nil
					}
				}
				input := map[string]any{"messages": messages}
				if preference != "omitted" {
					input["use_frostagent_decoration"] = preference == "true"
				}
				args, _ := json.Marshal(input)
				result, err := SendMsgTool().ExecuteResult(llm.WithRunContext(context.Background(), run), string(args))
				if err != nil {
					t.Fatal(err)
				}
				var payload struct {
					UseFrostAgentDecoration *bool       `json:"use_frostagent_decoration"`
					Messages                []Msg       `json:"messages"`
					Arguments               QQArguments `json:"arguments"`
				}
				wire := result.MessagePayload
				if media {
					wire = result.PlatformActionPayload
				}
				if err := json.Unmarshal([]byte(wire), &payload); err != nil {
					t.Fatal(err)
				}
				if preference == "omitted" {
					if payload.UseFrostAgentDecoration != nil {
						t.Fatal("omitted preference must inherit existing behavior")
					}
				} else if payload.UseFrostAgentDecoration == nil || *payload.UseFrostAgentDecoration != (preference == "true") {
					t.Fatalf("decoration preference lost in payload: %s", wire)
				}
				got := payload.Messages
				if media {
					got = payload.Arguments.Messages
				}
				if !reflect.DeepEqual(got, messages) {
					t.Fatalf("message chain changed: %+v, want %+v", got, messages)
				}
			})
		}
	}
}

func TestSendMessageDecorationSchemaAndProactiveModes(t *testing.T) {
	for name, parameters := range map[string]map[string]any{
		"ordinary":       SendMsgTool().Parameters(),
		"specific reply": proactive.SpecificReplyParameters(),
		"awareness":      proactive.AwarenessParameters(),
	} {
		t.Run(name, func(t *testing.T) {
			property, ok := parameters["properties"].(map[string]any)["use_frostagent_decoration"].(map[string]any)
			if !ok || property["type"] != "boolean" || property["default"] != true {
				t.Fatalf("missing optional boolean with default true: %+v", property)
			}
		})
	}
	for _, awareness := range []bool{false, true} {
		for _, preference := range []string{"true", "false"} {
			run := specificReplyContext(true)
			messages := `[{"type":"mention_specific","message_id":"fixture-old"},{"type":"plain","text":"fixture"}]`
			if awareness {
				run.Awareness = true
				messages = `[{"type":"mention_user","mention_user_id":"fixture-member"},{"type":"plain","text":"fixture"}]`
			}
			args := `{"use_frostagent_decoration":` + preference + `,"messages":` + messages + `}`
			result, err := SendMsgTool().ExecuteResult(llm.WithRunContext(context.Background(), run), args)
			if err != nil || result.MessagePayload == "" {
				t.Fatalf("proactive mode rejected valid preference: awareness=%v args=%s result=%+v err=%v", awareness, args, result, err)
			}
		}
	}
}

func TestSendMessageRejectsNonBooleanDecorationPreference(t *testing.T) {
	for _, value := range []string{`"false"`, `0`, `{}`, `[]`} {
		args := `{"use_frostagent_decoration":` + value + `,"messages":[{"type":"plain","text":"fixture"}]}`
		if result, err := SendMsgTool().ExecuteResult(context.Background(), args); err == nil || result.MessagePayload != "" || result.PlatformActionPayload != "" {
			t.Fatalf("invalid preference accepted: %s -> %+v, %v", args, result, err)
		}
	}
}

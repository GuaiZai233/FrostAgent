package proactive

import "testing"

func TestSpecificReplyAcceptsDeclareFinalMessage(t *testing.T) {
	args := `{"messages":[{"type":"mention_specific","message_id":"9"},{"type":"plain","text":"hi"}],"declare_final_message":true}`
	if !IsSpecificReply(args) {
		t.Fatal("mention_specific reply with declare_final_message must be accepted")
	}
}

func TestProactiveSchemasExposeDeclareFinalMessage(t *testing.T) {
	for name, schema := range map[string]map[string]any{
		"specific":  SpecificReplyParameters(),
		"awareness": AwarenessParameters(),
	} {
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["declare_final_message"]; !ok {
			t.Fatalf("%s schema must expose declare_final_message", name)
		}
	}
}

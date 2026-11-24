package onebot

import (
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"path/filepath"
	"testing"
)

// segmentTestEngine builds an engine with the multi-segment settings applied.
// Segmentation itself now travels as multiple send_message plain components, so
// fixtures no longer produce marker text.
func segmentTestEngine(t *testing.T, output string, enabled bool) *llm.Engine {
	t.Helper()
	config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{replyformat.EnvKey: fmt.Sprint(enabled), "STICKER_ACTIVITY": "0", "ENABLE_AT_IN_GROUP_MSG": "true", "ENABLE_REPLY_IN_GROUP_MSG": "true"} {
		if err := config.Update(key, value, false); err != nil {
			t.Fatal(err)
		}
	}
	engine := newTestEngine(&mockLLMProvider{responses: []*core.ChatResponse{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: output}}}})
	installTestSendMessage(engine)
	engine.Scope = runtimescope.New(config, nil, nil)
	return engine
}

func TestPartialReplyCannotResurrectResetSession(t *testing.T) {
	engine := segmentTestEngine(t, "", true)
	defer engine.Scope.Cancel()
	session := engine.SessionManager.GetOrCreate("synthetic-session")
	epoch := session.Epoch()
	if err := session.ResetSession(nil); err != nil {
		t.Fatal(err)
	}
	recordPartialReply(session, engine, epoch, true, "old delivered prefix")
	if len(session.Snapshot()) != 0 || len(session.SnapshotGroupContext(20, 4096, "").RecentStructuredMessages) != 0 {
		t.Fatal("old epoch restored after reset")
	}
}

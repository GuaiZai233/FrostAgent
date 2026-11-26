package onebot

import (
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/model"
)

func TestReplySenderID_StandardSenderObject(t *testing.T) {
	// OneBot v11 get_msg: sender lives only in sender.user_id.
	data := oneBotMessageData{Sender: &model.OneBotSender{UserID: 10001}}
	if got := replySenderID(data); got != 10001 {
		t.Fatalf("replySenderID = %d, want 10001", got)
	}
}

func TestReplySenderID_TopLevelUserIDPreferred(t *testing.T) {
	data := oneBotMessageData{UserID: 10002, Sender: &model.OneBotSender{UserID: 10001}}
	if got := replySenderID(data); got != 10002 {
		t.Fatalf("replySenderID = %d, want 10002", got)
	}
}

func TestReplySenderID_Missing(t *testing.T) {
	if got := replySenderID(oneBotMessageData{}); got != 0 {
		t.Fatalf("replySenderID = %d, want 0", got)
	}
}

package astrbot

import (
	"fmt"
	"strings"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/runtimeinfo"
	"github.com/GuaiZai233/FrostAgent/internal/security"
)

func isRuntimeStatusCandidate(event Event) bool {
	marked, _ := event.Metadata["runtime_info_command"].(bool)
	return marked && (event.Type == "" || event.Type == "event") &&
		(event.EventType == "" || event.EventType == "message") &&
		event.MessageType == "group" && event.pokeID == ""
}

// The plugin marks commands parsed from current top-level message components.
// Content alone cannot distinguish a command from a flattened quote on older plugins.
func handleRuntimeStatusCommand(conn *wsConn, event Event, engine *llm.Engine) bool {
	if !isRuntimeStatusCandidate(event) {
		return false
	}
	reject := func() bool {
		if conn != nil {
			_ = sendTerminalNoopWithSuppress(event, conn)
		}
		return true
	}
	if conn == nil || conn.mock || engine == nil {
		return reject()
	}
	done, ok := engine.Enter()
	if !ok {
		return reject()
	}
	defer done()
	if engine.Context().Err() != nil || conn.Context().Err() != nil {
		return reject()
	}
	engine.RecordIncomingMessage()
	if !runtimeinfo.Enabled(engine.Getenv(runtimeinfo.EnabledEnv)) ||
		strings.TrimSpace(event.Content) != runtimeinfo.Command || len(event.Attachments) != 0 ||
		strings.TrimSpace(event.GroupID) == "" || strings.TrimSpace(event.UserID) == "" ||
		strings.TrimSpace(event.UserID) == strings.TrimSpace(event.SelfID) {
		return reject()
	}
	platform := event.Platform
	if platform == "" {
		platform = "astrbot"
	}
	principal, err := security.NewPrincipal(platform, event.UserID)
	if err != nil || engine.Security.CheckAccess(principal) != nil {
		return reject()
	}
	if engine.Context().Err() != nil {
		return reject()
	}
	// A distinct wire type keeps this fixed diagnostic free of automatic @/quote
	// decorations without changing normal conversational replies.
	type diagnosticAction Action
	err = conn.WriteJSON(diagnosticAction{
		Type:        "action",
		Action:      "send_message",
		SuppressLLM: true,
		Platform:    platform,
		SessionID:   conn.sessionKey(event),
		TargetID:    event.GroupID,
		MessageType: "group",
		GroupID:     event.GroupID,
		UserID:      event.UserID,
		Content:     runtimeinfo.Format(engine.Version, engine.StartedAt, time.Now()),
		Echo:        "reply_" + event.MessageID,
	})
	if err != nil {
		engine.Log().Warn(logs.WEBSOCKET, fmt.Sprintf("AstrBot 运行信息发送失败: %v", err))
	}
	return true
}

package astrbot

import (
	"context"
	"errors"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/chatflow"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"strings"
	"testing"
	"time"
)

func TestNativeConversationUsesExistingAgentAndConfirmedDelivery(t *testing.T) {
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	var delivered []chatflow.Delivery
	processor := NewConversation(engine, "telegram", func(_ context.Context, d chatflow.Delivery) error { delivered = append(delivered, d); return nil }, nil)
	defer processor.Close()
	input := chatflow.Input{Message: core.IncomingMessage{ID: "fixture-message", Platform: "telegram", UserID: "fixture-user", SessionID: "telegram:private:fixture-user", MessageType: "private", Content: "hello", CreatedAt: time.Now()}}
	select {
	case <-processor.Submit(input):
	case <-time.After(3 * time.Second):
		t.Fatal("native conversation stalled")
	}
	if len(delivered) != 1 || delivered[0].Action != "send_message" || delivered[0].Message.Platform != "telegram" || delivered[0].Message.Content == "" {
		t.Fatalf("unexpected deliveries: %+v", delivered)
	}
	if len(engine.SessionManager.GetOrCreate(input.Message.SessionID).Snapshot()) != 2 {
		t.Fatal("shared session did not record delivered turn")
	}
}

func TestNativeConversationDeliveryFailureIsNotSuccessfulHistory(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	processor := NewConversation(engine, "telegram", func(context.Context, chatflow.Delivery) error { return errors.New("synthetic delivery failure") }, nil)
	defer processor.Close()
	input := chatflow.Input{Message: core.IncomingMessage{ID: "fixture-message", Platform: "telegram", UserID: "fixture-user", SessionID: "telegram:private:fixture-user", MessageType: "private", Content: "hello", CreatedAt: time.Now()}}
	<-processor.Submit(input)
	session := engine.SessionManager.GetOrCreate(input.Message.SessionID)
	for _, m := range session.Snapshot() {
		if m.Role == string(core.RoleAssistant) {
			t.Fatal("failed delivery committed as spoken history")
		}
	}
	if failure := session.TakeDeliveryFailure(); failure == nil || !strings.Contains(failure.Wording, "synthetic") {
		t.Fatalf("failure not recorded: %+v", failure)
	}
}

func nativeFixtureInput() chatflow.Input {
	return chatflow.Input{Message: core.IncomingMessage{ID: "fixture-message", Platform: "telegram", UserID: "fixture-user", SessionID: "telegram:private:fixture-user", MessageType: "private", Content: "hello", CreatedAt: time.Now()}}
}

type nativeTestDispatcher struct{ messages []core.IncomingMessage }

func (*nativeTestDispatcher) IsConfigured() bool { return true }
func (*nativeTestDispatcher) IsEnabled() bool    { return true }
func (d *nativeTestDispatcher) DispatchIncoming(_ context.Context, m core.IncomingMessage) error {
	d.messages = append(d.messages, m)
	return nil
}

func TestNativeConversationDispatchKeepsTransportIdentity(t *testing.T) {
	dispatcher := &nativeTestDispatcher{}
	engine := newTestEngine(&mockLLMProvider{})
	p := NewConversation(engine, "telegram", func(context.Context, chatflow.Delivery) error { return nil }, dispatcher)
	defer p.Close()
	input := nativeFixtureInput()
	<-p.Submit(input)
	if len(dispatcher.messages) != 1 || dispatcher.messages[0].AdapterID != "telegram" || dispatcher.messages[0].Platform != "telegram" || dispatcher.messages[0].SessionID != input.Message.SessionID {
		t.Fatalf("lost native transport: %+v", dispatcher.messages)
	}
}

func TestNativeConversationCloseCancelsInflightDelivery(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	started := make(chan struct{})
	p := NewConversation(engine, "telegram", func(ctx context.Context, d chatflow.Delivery) error {
		if d.Action != "send_message" {
			return nil
		}
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, nil)
	defer p.Close()
	done := p.Submit(nativeFixtureInput())
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("delivery did not start")
	}
	p.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("close did not cancel native send")
	}
}

func TestNativeConversationKeepsOnlyConfirmedFinalPrefix(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	p := NewConversation(engine, "telegram", func(context.Context, chatflow.Delivery) error {
		return &chatflow.DeliveryError{Err: errors.New("synthetic second chunk failure"), DeliveredText: "confirmed prefix", Confirmed: true}
	}, nil)
	defer p.Close()
	input := nativeFixtureInput()
	<-p.Submit(input)
	session := engine.SessionManager.GetOrCreate(input.Message.SessionID)
	var replies []string
	for _, m := range session.Snapshot() {
		if m.Role == string(core.RoleAssistant) {
			replies = append(replies, fmt.Sprint(m.Content))
		}
	}
	if len(replies) != 1 || replies[0] != "confirmed prefix" {
		t.Fatalf("incorrect confirmed history: %v", replies)
	}
	if session.TakeDeliveryFailure() == nil {
		t.Fatal("partial delivery failure lost")
	}
}

func TestNativeConversationKeepsPartialToolTextAndMediaConfirmation(t *testing.T) {
	for _, text := range []string{"confirmed prefix", ""} {
		t.Run(fmt.Sprint(text == ""), func(t *testing.T) {
			provider := autoDecorationSendHookProvider()
			provider.responses[1].Message.Content = "(empty)"
			engine := newTestEngine(provider)
			engine.ToolRegistry["send_message"] = tools.SendMsgTool()
			var sends int
			p := NewConversation(engine, "telegram", func(_ context.Context, d chatflow.Delivery) error {
				if d.Action == "send_message" {
					sends++
					return &chatflow.DeliveryError{Err: errors.New("synthetic later part failure"), DeliveredText: text, Confirmed: true}
				}
				return nil
			}, nil)
			defer p.Close()
			input := nativeFixtureInput()
			<-p.Submit(input)
			if sends != 1 {
				t.Fatalf("confirmed parts followed by duplicate placeholder: %d sends", sends)
			}
			var replies []string
			for _, m := range engine.SessionManager.GetOrCreate(input.Message.SessionID).Snapshot() {
				if m.Role == string(core.RoleAssistant) {
					replies = append(replies, fmt.Sprint(m.Content))
				}
			}
			if text != "" && (len(replies) != 1 || replies[0] != text) {
				t.Fatalf("incorrect tool prefix history: %v", replies)
			}
			if text == "" && len(replies) != 0 {
				t.Fatalf("invented text for media-only partial send: %v", replies)
			}
		})
	}
}

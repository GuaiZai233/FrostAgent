package core

import (
	"context"
	"errors"
	"testing"
)

type mockAdapter struct {
	id      string
	lastMsg OutgoingMessage
	err     error
}

func (m *mockAdapter) ID() string {
	return m.id
}

func (m *mockAdapter) Send(ctx context.Context, msg OutgoingMessage) error {
	m.lastMsg = msg
	return m.err
}

func TestDefaultDispatcher(t *testing.T) {
	d := NewDefaultDispatcher()

	mockOneBot := &mockAdapter{id: "onebot"}
	mockAstrBot := &mockAdapter{id: "astrbot"}

	d.RegisterAdapter(mockOneBot)
	d.RegisterAdapter(mockAstrBot)

	// Test GetAdapter
	if a, ok := d.GetAdapter("onebot"); !ok || a.ID() != "onebot" {
		t.Errorf("GetAdapter(onebot) failed, got %v, ok=%v", a, ok)
	}

	// Test ListAdapters
	list := d.ListAdapters()
	if len(list) != 2 {
		t.Errorf("ListAdapters() len=%d, want 2", len(list))
	}

	// Test Dispatch success
	msg := OutgoingMessage{
		TargetID: "12345",
		Content:  "Hello Dispatcher",
		Platform: "onebot",
	}
	if err := d.Dispatch(context.Background(), "onebot", msg); err != nil {
		t.Errorf("Dispatch to onebot failed: %v", err)
	}
	if mockOneBot.lastMsg.Content != "Hello Dispatcher" {
		t.Errorf("mockOneBot did not receive message, got: %+v", mockOneBot.lastMsg)
	}

	// Test Dispatch to unknown platform
	if err := d.Dispatch(context.Background(), "unknown", msg); err == nil {
		t.Errorf("expected error dispatching to unknown platform, got nil")
	}

	// Test UnregisterAdapter
	d.UnregisterAdapter("onebot")
	if _, ok := d.GetAdapter("onebot"); ok {
		t.Errorf("expected onebot to be unregistered")
	}

	// Test error propagation
	mockAstrBot.err = errors.New("send failed")
	if err := d.Dispatch(context.Background(), "astrbot", msg); err == nil || err.Error() != "send failed" {
		t.Errorf("expected send failed error, got %v", err)
	}
}

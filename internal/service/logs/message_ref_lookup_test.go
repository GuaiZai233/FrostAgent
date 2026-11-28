package logs

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
)

func TestMessageRefLookupIgnoresPagingAndKeepsSourceIsolation(t *testing.T) {
	store := logs.New("synthetic-instance", "test", 100)
	id := store.RecordMessageRef(logs.MessageRef{Platform: "onebot", SessionID: "group:1", MessageID: "9", Action: "send_group_msg"})
	for i := 0; i < 60; i++ {
		store.Info(logs.SYSTEM, "newer entry")
	}
	service := New(store)
	request := func(source, value string) *connect.Request[v1.ListLogsRequest] {
		req := connect.NewRequest(&v1.ListLogsRequest{MessageRefId: value, SourceFilter: "wrong-source",
			Pagination: &v1.Pagination{PageSize: 1, PageToken: "999"}})
		req.Header().Set("X-FrostAgent-Log-Source", source)
		return req
	}
	resp, err := service.ListLogs(context.Background(), request("instance", " "+id+" "))
	if err != nil || len(resp.Msg.Entries) != 1 || resp.Msg.Entries[0].MessageRefId != id || !strings.Contains(resp.Msg.Entries[0].Summary, `"message_id":"9"`) {
		t.Fatalf("lookup failed: %v, %v", resp, err)
	}
	for _, svc := range []*Service{service, New(logs.New("other", "other", 10)), New()} {
		source := "instance"
		if svc == service {
			source = "control-plane"
		}
		if _, err := svc.ListLogs(context.Background(), request(source, id)); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("lookup crossed source boundary: %v", err)
		}
	}
	if _, err := service.ListLogs(context.Background(), request("instance", "invalid")); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid RefID accepted: %v", err)
	}
	if _, err := service.ListLogs(context.Background(), request("instance", logs.NewMessageRefID())); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("unknown RefID must be NotFound: %v", err)
	}
}

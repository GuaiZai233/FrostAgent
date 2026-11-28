package logs

import (
	"connectrpc.com/connect"
	"context"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	logspkg "github.com/GuaiZai233/FrostAgent/internal/logs"
	"path/filepath"
	"testing"
	"time"
)

func TestFilteredPersistencePaginationAndStats(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	store := logspkg.New("synthetic", "Synthetic", 2)
	if err := store.EnablePersistence(dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Second).Format(time.RFC3339Nano)
	for i := 0; i < 5; i++ {
		store.Info(logspkg.SYSTEM, "matching", "synthetic-trace")
	}
	store.Warn(logspkg.HTTP, "unrelated", "other-trace")
	store.StopPersistence()
	reopened := logspkg.New("synthetic", "Synthetic", 2)
	if err := reopened.EnablePersistence(dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	svc := New(reopened)
	req := &v1.ListLogsRequest{Scope: "instance", Since: since, TraceId: "synthetic-trace", Search: "match", Pagination: &v1.Pagination{PageSize: 2}}
	seen := map[string]bool{}
	for {
		response, err := svc.ListLogs(context.Background(), connect.NewRequest(req))
		if err != nil {
			t.Fatal(err)
		}
		if !response.Msg.Coverage.Persistent || response.Msg.Coverage.Earliest == "" {
			t.Fatal("coverage missing")
		}
		for _, entry := range response.Msg.Entries {
			if seen[entry.Id] {
				t.Fatal("duplicate page item")
			}
			seen[entry.Id] = true
		}
		if response.Msg.Pagination.PageToken == "" {
			break
		}
		req.Pagination.PageToken = response.Msg.Pagination.PageToken
	}
	if len(seen) != 5 {
		t.Fatalf("persisted matches=%d", len(seen))
	}
	stats, err := svc.GetLogStats(context.Background(), connect.NewRequest(req))
	if err != nil || stats.Msg.Total != 5 {
		t.Fatalf("%v %v", stats, err)
	}
	if _, err := svc.ListLogs(context.Background(), connect.NewRequest(&v1.ListLogsRequest{Scope: "control-plane"})); err == nil {
		t.Fatal("instance endpoint returned control-plane logs")
	}
	if _, err := svc.ClearLogs(context.Background(), connect.NewRequest(&v1.ClearLogsRequest{Scope: "instance"})); err != nil {
		t.Fatal(err)
	}
	if len(reopened.Snapshot()) != 0 {
		t.Fatal("clear retained disk history")
	}
}

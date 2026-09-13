package logs

import (
	"FrostAgent/internal/logs"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"connectrpc.com/connect"
)

func TestLogSourceSelectionAndClearAreExclusive(t *testing.T) {
	logs.General.Clear()
	t.Cleanup(logs.General.Clear)
	instanceStore := logs.New("a1b2c3d4", "实例1", 10)
	instanceStore.Info(logs.SYSTEM, "instance-only")
	logs.General.Info(logs.HTTP, "control-plane-only")
	service := New(instanceStore)

	controlPlane := connect.NewRequest(&v1.ListLogsRequest{})
	controlPlane.Header().Set("X-FrostAgent-Log-Source", "control-plane")
	response, err := service.ListLogs(context.Background(), controlPlane)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Msg.GetEntries()) != 1 || response.Msg.GetEntries()[0].GetSummary() != "control-plane-only" {
		t.Fatalf("control plane selection merged sources: %+v", response.Msg.GetEntries())
	}
	clearControlPlane := connect.NewRequest(&v1.ClearLogsRequest{})
	clearControlPlane.Header().Set("X-FrostAgent-Log-Source", "control-plane")
	if _, err = service.ClearLogs(context.Background(), clearControlPlane); err != nil {
		t.Fatal(err)
	}
	if len(logs.General.Snapshot()) != 0 || len(instanceStore.Snapshot()) != 1 {
		t.Fatal("clearing Control Plane logs also cleared the instance")
	}

	logs.General.Info(logs.HTTP, "control-plane-second")
	clearInstance := connect.NewRequest(&v1.ClearLogsRequest{})
	clearInstance.Header().Set("X-FrostAgent-Log-Source", "instance")
	if _, err = service.ClearLogs(context.Background(), clearInstance); err != nil {
		t.Fatal(err)
	}
	if len(instanceStore.Snapshot()) != 0 || len(logs.General.Snapshot()) != 1 {
		t.Fatal("clearing instance logs also cleared the Control Plane")
	}
}

func TestLogServicePreservesTraceIDInListAndStream(t *testing.T) {
	instanceStore := logs.New("inst-trace-1", "TraceInstance", 10)
	expectedTraceList := "eval-test-trace-list-001"
	instanceStore.Info(logs.LLM_REQUEST, "prompt request body", expectedTraceList)

	service := New(instanceStore)
	path, handler := frostagentv1connect.NewLogServiceHandler(service)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := frostagentv1connect.NewLogServiceClient(srv.Client(), srv.URL)

	// 1. Verify ListLogs preserves TraceId
	listReq := connect.NewRequest(&v1.ListLogsRequest{})
	listReq.Header().Set("X-FrostAgent-Log-Source", "instance")
	listResp, err := client.ListLogs(context.Background(), listReq)
	if err != nil {
		t.Fatalf("ListLogs failed: %v", err)
	}
	if len(listResp.Msg.GetEntries()) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(listResp.Msg.GetEntries()))
	}
	if gotTrace := listResp.Msg.GetEntries()[0].GetTraceId(); gotTrace != expectedTraceList {
		t.Errorf("ListLogs TraceId = %q, want %q", gotTrace, expectedTraceList)
	}

	// 2. Verify StreamLogs preserves TraceId
	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()

	expectedTraceStream := "eval-test-trace-stream-002"
	stopCh := make(chan struct{})
	defer close(stopCh)
	// Periodically emit log entry until received by stream
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				instanceStore.Info(logs.LLM_RESPONSE, "response text", expectedTraceStream)
			}
		}
	}()

	streamReq := connect.NewRequest(&v1.StreamLogsRequest{})
	streamReq.Header().Set("X-FrostAgent-Log-Source", "instance")
	stream, err := client.StreamLogs(streamCtx, streamReq)
	if err != nil {
		t.Fatalf("StreamLogs failed: %v", err)
	}
	defer stream.Close()

	if !stream.Receive() {
		if stream.Err() != nil {
			t.Fatalf("stream.Receive error: %v", stream.Err())
		}
		t.Fatal("stream closed without receiving entry")
	}

	received := stream.Msg()
	if gotTrace := received.GetTraceId(); gotTrace != expectedTraceStream {
		t.Errorf("StreamLogs TraceId = %q, want %q", gotTrace, expectedTraceStream)
	}
}

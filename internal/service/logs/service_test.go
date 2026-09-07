package logs

import (
	"FrostAgent/internal/logs"
	"context"
	"testing"

	v1 "FrostAgent/gen/proto/frostagent/v1"
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

package machine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"FrostAgent/internal/computeruse/bridge"
)

type fakeDesktop struct {
	panicInput       bool
	entered, release chan struct{}
}

func (d *fakeDesktop) Screenshot(context.Context) ([]byte, error) {
	return []byte("synthetic-png"), nil
}
func (d *fakeDesktop) Input(context.Context, []Input) error {
	if d.panicInput {
		panic("synthetic-private-error")
	}
	if d.entered != nil {
		close(d.entered)
		<-d.release
	}
	return nil
}
func setupExecutor(t *testing.T, d Desktop) (*Executor, Workspace) {
	t.Helper()
	p, project := testProjects(t)
	w, err := p.Prepare(context.Background(), project.ID, "synthetic-task", 2)
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(p, d)
	if err := p.SetDesktopPaused(false, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e, w
}
func assertExecute(t *testing.T, e *Executor, r bridge.Request, want bridge.State) {
	t.Helper()
	// Operation IDs are valid protocol identifiers, while kinds contain dots.
	r.OperationID = "synthetic-operation"
	out, err := e.Execute(context.Background(), r)
	if err != nil || out.State != want {
		t.Fatalf("%s: %+v %v", r.Kind, out, err)
	}
}
func TestMaintenanceLeaseSurvivesStalePauseAndReleasesAfterWork(t *testing.T) {
	e, w := setupExecutor(t, nil)
	assertExecute(t, e, request("maintenance.acquire", w.TaskID, 2, Params{}), bridge.Succeeded)
	assertExecute(t, e, request("task.pause", w.TaskID, 1, Params{}), bridge.Failed)
	if e.maintenanceOwner != w.TaskID {
		t.Fatal("stale pause released current maintenance")
	}
	e.active[w.TaskID] = 1
	assertExecute(t, e, request("task.pause", w.TaskID, 2, Params{}), bridge.Succeeded)
	if e.maintenanceOwner != w.TaskID {
		t.Fatal("pause released live process lease")
	}
	e.finishOperation(w.TaskID)
	if e.maintenanceOwner != "" {
		t.Fatal("completed paused maintenance stranded lease")
	}
}
func TestDesktopPanicDoesNotStrandPause(t *testing.T) {
	e, w := setupExecutor(t, &fakeDesktop{panicInput: true})
	assertExecute(t, e, request("desktop.acquire", w.TaskID, 2, Params{}), bridge.Succeeded)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("fixture did not panic")
			}
		}()
		_, _ = e.Execute(context.Background(), request("desktop.input", w.TaskID, 2, Params{Input: []Input{{Type: "key", Key: 65}}}))
	}()
	done := make(chan struct{})
	go func() {
		assertExecute(t, e, request("task.pause", w.TaskID, 2, Params{}), bridge.Succeeded)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("panic stranded machine mutex")
	}
}
func TestPauseWaitsForInputAndExpiredQueuedWriteDoesNotExecute(t *testing.T) {
	d := &fakeDesktop{entered: make(chan struct{}), release: make(chan struct{})}
	e, w := setupExecutor(t, d)
	assertExecute(t, e, request("desktop.acquire", w.TaskID, 2, Params{}), bridge.Succeeded)
	inputDone := make(chan struct{})
	go func() {
		_, _ = e.Execute(context.Background(), request("desktop.input", w.TaskID, 2, Params{Input: []Input{{Type: "key", Key: 65}}}))
		close(inputDone)
	}()
	<-d.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writeDone := make(chan struct{})
	go func() {
		_, _ = e.Execute(ctx, request("file.write", w.TaskID, 2, Params{Path: "must-not-exist", Text: "synthetic"}))
		close(writeDone)
	}()
	pauseDone := make(chan struct{})
	go func() {
		_, _ = e.Execute(context.Background(), request("task.pause", w.TaskID, 2, Params{}))
		close(pauseDone)
	}()
	select {
	case <-pauseDone:
		t.Fatal("pause acknowledged live input")
	case <-time.After(20 * time.Millisecond):
	}
	close(d.release)
	<-inputDone
	<-pauseDone
	<-writeDone
	if _, err := os.Stat(filepath.Join(w.Path, "must-not-exist")); !os.IsNotExist(err) {
		t.Fatal("expired queued write executed")
	}
}
func TestFileReadRejectsDirectoriesAndReadsEmptyFile(t *testing.T) {
	e, w := setupExecutor(t, nil)
	assertExecute(t, e, request("file.read", w.TaskID, 2, Params{Path: "."}), bridge.Failed)
	if err := os.WriteFile(filepath.Join(w.Path, "empty"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	assertExecute(t, e, request("file.read", w.TaskID, 2, Params{Path: "empty"}), bridge.Succeeded)
}

func TestHumanTakeoverBlocksEveryTaskUntilExplicitResume(t *testing.T) {
	e, w := setupExecutor(t, &fakeDesktop{})
	assertExecute(t, e, request("desktop.acquire", w.TaskID, 2, Params{}), bridge.Succeeded)
	assertExecute(t, e, request("desktop.takeover", "synthetic-control", 3, Params{}), bridge.Succeeded)
	assertExecute(t, e, request("desktop.resume", "old-control", 2, Params{}), bridge.Failed)
	assertExecute(t, e, request("desktop.acquire", w.TaskID, 2, Params{}), bridge.Failed)
	assertExecute(t, e, request("desktop.input", w.TaskID, 2, Params{Input: []Input{{Type: "key", Key: 65}}}), bridge.Failed)
	assertExecute(t, e, request("desktop.resume", "synthetic-control", 4, Params{}), bridge.Succeeded)
	assertExecute(t, e, request("desktop.acquire", w.TaskID, 2, Params{}), bridge.Succeeded)
}

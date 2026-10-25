package machine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"FrostAgent/internal/computeruse/bridge"
)

type Executor struct {
	mu               sync.Mutex
	projects         *Projects
	desktop          Desktop
	cache            *bridge.ContentCache
	desktopOwner     string
	maintenanceOwner string
	active           map[string]int
	pausePending     map[string]bool
}

func NewExecutor(projects *Projects, desktop Desktop) *Executor {
	return &Executor{projects: projects, desktop: desktop, cache: bridge.NewContentCache(), active: map[string]int{}, pausePending: map[string]bool{}}
}
func (e *Executor) Content(id string) (bridge.Content, bool) { return e.cache.Content(id) }
func (e *Executor) Close() error                             { return e.cache.Close() }
func (e *Executor) Validate(r bridge.Request) error {
	p, err := params(r.Parameters)
	if err != nil {
		return err
	}
	switch r.Kind {
	case "project.list", "desktop.acquire", "desktop.release", "desktop.screenshot", "maintenance.acquire", "maintenance.release", "task.pause", "task.instructions", "desktop.takeover", "desktop.resume":
	case "project.register":
		if p.Project == nil {
			return ErrInvalid
		}
	case "project.archive", "task.prepare":
		if !idPattern.MatchString(p.ProjectID) {
			return ErrInvalid
		}
	case "terminal.exec":
		if p.Command == "" {
			return ErrInvalid
		}
	case "file.read", "file.write":
		if p.Path == "" {
			return ErrInvalid
		}
	case "desktop.input":
		if len(p.Input) == 0 || len(p.Input) > 128 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func (e *Executor) Execute(ctx context.Context, r bridge.Request) (bridge.Outcome, error) {
	p, err := params(r.Parameters)
	if err != nil {
		return bridge.Outcome{}, err
	}
	// Gate all actions at execution time, including those queued before pause.
	e.mu.Lock()
	locked := true
	unlock := func() { locked = false; e.mu.Unlock() }
	defer func() {
		if locked {
			e.mu.Unlock()
		}
	}()
	if ctx.Err() != nil {
		unlock()
		return e.rejected(r, ErrInvalid)
	}
	switch r.Kind {
	case "project.list":
		projects, workspaces := e.projects.List()
		paused, generation := e.projects.DesktopState()
		unlock()
		return e.result(r, map[string]any{"projects": projects, "workspaces": workspaces, "desktop_paused": paused, "desktop_generation": generation}, nil)
	case "desktop.takeover", "desktop.resume":
		paused := r.Kind == "desktop.takeover"
		err := e.projects.SetDesktopPaused(paused, r.Generation)
		if err == nil {
			e.desktopOwner = ""
		}
		unlock()
		return e.result(r, map[string]bool{"desktop_paused": paused, "gui_stopped": paused && err == nil}, err)
	case "project.register":
		if e.busy() {
			unlock()
			return e.rejected(r, ErrBusy)
		}
		value, err := e.projects.Register(ctx, *p.Project)
		unlock()
		if err != nil && !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrPaused) {
			return bridge.Outcome{}, err
		}
		return e.result(r, value, err)
	case "project.archive":
		err := e.projects.Archive(p.ProjectID, p.Archived)
		unlock()
		return e.result(r, map[string]bool{"archived": p.Archived}, err)
	case "task.prepare":
		if e.active[r.TaskID] != 0 || (e.maintenanceOwner != "" && e.maintenanceOwner != r.TaskID) {
			unlock()
			return e.rejected(r, ErrBusy)
		}
		w, err := e.projects.Prepare(ctx, p.ProjectID, r.TaskID, r.Generation)
		if err == nil {
			delete(e.pausePending, r.TaskID)
		}
		unlock()
		if err != nil && !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrPaused) {
			return bridge.Outcome{}, err
		}
		return e.result(r, w, err)
	case "task.pause":
		err := e.projects.Pause(r.TaskID, r.Generation)
		if err == nil {
			e.pausePending[r.TaskID] = true
		}
		// GUI input runs under mu, so this acknowledgement follows its return.
		if err == nil && e.desktopOwner == r.TaskID {
			e.desktopOwner = ""
		}
		// Do not release maintenance while a terminal may still change tools.
		if err == nil && e.active[r.TaskID] == 0 && e.maintenanceOwner == r.TaskID {
			e.maintenanceOwner = ""
		}
		active := e.active[r.TaskID]
		unlock()
		return e.result(r, map[string]any{"gui_stopped": true, "running_operations": active}, err)
	}
	w, err := e.projects.Workspace(r.TaskID, r.Generation)
	if err != nil {
		unlock()
		return e.rejected(r, err)
	}
	if e.maintenanceOwner != "" && e.maintenanceOwner != r.TaskID {
		unlock()
		return e.rejected(r, ErrBusy)
	}
	switch r.Kind {
	case "maintenance.acquire":
		if e.busy() && e.maintenanceOwner != r.TaskID {
			unlock()
			return e.rejected(r, ErrBusy)
		}
		e.maintenanceOwner = r.TaskID
		unlock()
		return e.result(r, map[string]bool{"maintenance": true}, nil)
	case "maintenance.release":
		if e.maintenanceOwner != r.TaskID || e.active[r.TaskID] != 0 {
			unlock()
			return e.rejected(r, ErrBusy)
		}
		e.maintenanceOwner = ""
		unlock()
		return e.result(r, map[string]bool{"maintenance": false}, nil)
	case "desktop.acquire":
		if e.projects.DesktopPaused() {
			unlock()
			return e.rejected(r, ErrPaused)
		}
		if e.desktopOwner != "" && e.desktopOwner != r.TaskID {
			unlock()
			return e.rejected(r, ErrBusy)
		}
		if e.desktop == nil {
			unlock()
			return e.rejected(r, ErrInvalid)
		}
		e.desktopOwner = r.TaskID
		unlock()
		return e.result(r, map[string]bool{"desktop_lease": true}, nil)
	case "desktop.release":
		if e.desktopOwner != r.TaskID {
			unlock()
			return e.rejected(r, ErrBusy)
		}
		e.desktopOwner = ""
		unlock()
		return e.result(r, map[string]bool{"desktop_lease": false}, nil)
	case "desktop.input", "desktop.screenshot":
		if e.projects.DesktopPaused() {
			unlock()
			return e.rejected(r, ErrPaused)
		}
		if e.desktopOwner != r.TaskID || e.desktop == nil {
			unlock()
			return e.rejected(r, ErrBusy)
		}
		if r.Kind == "desktop.input" {
			err := e.desktop.Input(ctx, p.Input)
			unlock()
			if err != nil {
				return bridge.Outcome{}, err
			}
			return e.result(r, map[string]bool{"input_sent": true}, nil)
		}
		png, err := e.desktop.Screenshot(ctx)
		unlock()
		if err != nil {
			return e.rejected(r, ErrInvalid)
		}
		if err := e.cache.Put(r, "", png); err != nil {
			return bridge.Outcome{}, err
		}
		return bridge.Outcome{State: bridge.Succeeded, Code: "completed"}, nil
	case "terminal.exec", "file.read", "file.write", "task.instructions":
		e.active[r.TaskID]++
		unlock()
		defer e.finishOperation(r.TaskID)
	default:
		unlock()
		return e.rejected(r, ErrInvalid)
	}
	if r.Kind == "task.instructions" {
		var instructions string
		for _, name := range []string{"AGENTS.md", "agents.md"} {
			path := filepath.Join(w.Path, name)
			info, err := os.Stat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<10 {
				return e.rejected(r, ErrInvalid)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return e.rejected(r, ErrInvalid)
			}
			instructions = string(data)
			break
		}
		if err := e.cache.Put(r, instructions, nil); err != nil {
			return bridge.Outcome{}, err
		}
		return bridge.Outcome{State: bridge.Succeeded, Code: "completed"}, nil
	}
	if r.Kind == "terminal.exec" {
		output, code, err := runShell(ctx, w, p.Command)
		if putErr := e.cache.Put(r, output, nil); putErr != nil {
			return bridge.Outcome{}, putErr
		}
		if ctx.Err() != nil || code < 0 {
			return bridge.Outcome{State: bridge.Unknown, Code: "outcome_unknown"}, nil
		}
		state, result := bridge.Succeeded, "completed"
		if err != nil || code != 0 {
			state, result = bridge.Failed, "execution_failed"
		}
		return bridge.Outcome{State: state, Code: result, ExitCode: &code}, nil
	}
	path := p.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(w.Path, path)
	}
	if ctx.Err() != nil {
		return e.rejected(r, ErrInvalid)
	}
	if r.Kind == "file.read" {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return e.rejected(r, ErrInvalid)
		}
		file, err := os.Open(path)
		if err != nil {
			return e.rejected(r, ErrInvalid)
		}
		defer file.Close()
		stop := context.AfterFunc(ctx, func() { _ = file.Close() })
		defer stop()
		info, err = file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return e.rejected(r, ErrInvalid)
		}
		data, err := io.ReadAll(io.LimitReader(file, (256<<10)+1))
		if err != nil || len(data) > 256<<10 {
			return e.rejected(r, ErrInvalid)
		}
		if err := e.cache.Put(r, string(data), nil); err != nil {
			return bridge.Outcome{}, err
		}
		return bridge.Outcome{State: bridge.Succeeded, Code: "completed"}, nil
	}
	// Explicit file writes are whole-machine delegated operations. Coding
	// defaults remain the task worktree; absolute paths are not a sandbox escape.
	if info, err := os.Stat(path); err == nil && !info.Mode().IsRegular() {
		return e.rejected(r, ErrInvalid)
	}
	if err := os.WriteFile(path, []byte(p.Text), 0600); err != nil {
		return bridge.Outcome{}, err
	}
	return e.result(r, map[string]bool{"written": true}, nil)
}

func (e *Executor) finishOperation(task string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.active[task]--
	if e.active[task] == 0 && e.pausePending[task] && e.maintenanceOwner == task {
		e.maintenanceOwner = ""
	}
}

func (e *Executor) busy() bool {
	if e.desktopOwner != "" || e.maintenanceOwner != "" {
		return true
	}
	for _, n := range e.active {
		if n > 0 {
			return true
		}
	}
	return false
}
func (e *Executor) rejected(r bridge.Request, err error) (bridge.Outcome, error) {
	return e.result(r, nil, err)
}
func (e *Executor) result(r bridge.Request, value any, err error) (bridge.Outcome, error) {
	if err != nil {
		message := "operation rejected; inspect configuration or current machine state"
		if errors.Is(err, ErrBusy) {
			message = "machine resource busy; wait or explicitly pause the owning task"
		}
		if errors.Is(err, ErrPaused) {
			message = "task is paused; explicit resume required"
		}
		if putErr := e.cache.Put(r, message, nil); putErr != nil {
			return bridge.Outcome{}, putErr
		}
		return bridge.Outcome{State: bridge.Failed, Code: "execution_failed"}, nil
	}
	data, _ := json.Marshal(value)
	if err := e.cache.Put(r, string(data), nil); err != nil {
		return bridge.Outcome{}, err
	}
	return bridge.Outcome{State: bridge.Succeeded, Code: "completed"}, nil
}

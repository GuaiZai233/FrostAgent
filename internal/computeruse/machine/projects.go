package machine

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"FrostAgent/internal/computeruse/bridge"
)

type projectState struct {
	Version           int                  `json:"version"`
	Projects          map[string]Project   `json:"projects"`
	Workspaces        map[string]Workspace `json:"workspaces"`
	Environments      map[string][]string  `json:"environments"`
	Fences            map[string]uint64    `json:"pause_fences"`
	DesktopPaused     bool                 `json:"desktop_paused"`
	DesktopGeneration uint64               `json:"desktop_generation"`
}

type Projects struct {
	mu              sync.Mutex
	root, file, git string
	state           projectState
	blocked         bool
}

func OpenProjects(root, stateDir, gitPath string) (*Projects, error) {
	if !filepath.IsAbs(root) || !filepath.IsAbs(stateDir) || !filepath.IsAbs(gitPath) {
		return nil, ErrInvalid
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	gitPath, err = filepath.EvalSymlinks(gitPath)
	if err != nil {
		return nil, ErrInvalid
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}
	p := &Projects{root: root, file: filepath.Join(stateDir, "projects.json"), git: gitPath,
		state: projectState{Version: 1, Projects: map[string]Project{}, Workspaces: map[string]Workspace{}, Environments: map[string][]string{}}}
	data, err := os.ReadFile(p.file)
	if err == nil {
		if json.Unmarshal(data, &p.state) != nil || p.state.Version != 1 || p.state.Projects == nil || p.state.Workspaces == nil || p.state.Environments == nil {
			return nil, ErrInvalid
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if p.state.Fences == nil {
		p.state.Fences = map[string]uint64{}
	}
	p.state.DesktopPaused = true
	for id, w := range p.state.Workspaces {
		// Bridge reboot recovers files but never permits old task generations.
		w.Paused = true
		p.state.Fences[id] = w.Generation
		p.state.Workspaces[id] = w
	}
	if err := p.save(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Projects) save() error {
	data, err := json.Marshal(p.state)
	if err != nil {
		return err
	}
	err = bridge.WriteStateAtomic(p.file, data)
	if err != nil {
		p.blocked = true
	}
	return err
}

func (p *Projects) SetDesktopPaused(paused bool, generation uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blocked {
		return ErrPaused
	}
	if generation <= p.state.DesktopGeneration {
		return ErrInvalid
	}
	p.state.DesktopPaused = paused
	p.state.DesktopGeneration = generation
	return p.save()
}
func (p *Projects) DesktopPaused() bool {
	paused, _ := p.DesktopState()
	return paused
}
func (p *Projects) DesktopState() (bool, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.blocked || p.state.DesktopPaused, p.state.DesktopGeneration
}

func (p *Projects) gitCommand(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, p.git, args...)
	cmd.Dir = dir
	cmd.Env = append(pinnedEnvironment(nil), "GIT_TERMINAL_PROMPT=0")
	prepareProcess(cmd)
	output := &boundedOutput{limit: 256 << 10}
	cmd.Stdout, cmd.Stderr = output, output
	cmd.WaitDelay = 2 * time.Second
	err := runProcessTree(ctx, cmd)
	return output.String(), err
}

func (p *Projects) Register(ctx context.Context, project Project) (Project, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blocked {
		return Project{}, ErrPaused
	}
	if !idPattern.MatchString(project.ID) || project.Baseline == "" || strings.HasPrefix(project.Baseline, "-") ||
		!filepath.IsAbs(project.Shell) {
		return Project{}, ErrInvalid
	}
	shell, err := filepath.EvalSymlinks(project.Shell)
	if err != nil {
		return Project{}, ErrInvalid
	}
	if info, err := os.Stat(shell); err != nil || !info.Mode().IsRegular() {
		return Project{}, ErrInvalid
	}
	project.Shell = shell
	if runtime.GOOS == "windows" {
		name := strings.ToLower(filepath.Base(shell))
		if name != "pwsh.exe" && name != "powershell.exe" {
			return Project{}, ErrInvalid
		}
	}
	for name, value := range project.Environment {
		if !environmentAllowed(name) || strings.ContainsRune(value, 0) {
			return Project{}, ErrInvalid
		}
	}
	project.Environment = maps.Clone(project.Environment)
	path := project.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.root, path)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil || path == p.root || !inside(p.root, path) {
		return Project{}, ErrInvalid
	}
	gitInfo, err := os.Lstat(filepath.Join(path, ".git"))
	if err != nil || !gitInfo.IsDir() || gitInfo.Mode()&os.ModeSymlink != 0 {
		return Project{}, ErrInvalid
	}
	top, err := p.gitCommand(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return Project{}, ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(strings.TrimSpace(top))
	if err != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(path)) {
		return Project{}, ErrInvalid
	}
	if _, err = p.gitCommand(ctx, path, "rev-parse", "--verify", "--end-of-options", project.Baseline+"^{commit}"); err != nil {
		return Project{}, ErrInvalid
	}
	project.Path = path
	if old, ok := p.state.Projects[project.ID]; ok && old.Path != path {
		return Project{}, ErrInvalid
	}
	for id, existing := range p.state.Projects {
		if id != project.ID && strings.EqualFold(existing.Path, path) {
			return Project{}, ErrInvalid
		}
	}
	if project.Remote != "" {
		if strings.HasPrefix(project.Remote, "-") || strings.ContainsAny(project.Remote, "\r\n\x00") {
			return Project{}, ErrInvalid
		}
		if !validRemote(project.Remote) {
			return Project{}, ErrInvalid
		}
		// Only a caller's explicit project registration/update configures origin.
		if _, err = p.gitCommand(ctx, path, "remote", "get-url", "origin"); err != nil {
			_, err = p.gitCommand(ctx, path, "remote", "add", "origin", project.Remote)
		} else {
			_, err = p.gitCommand(ctx, path, "remote", "set-url", "origin", project.Remote)
		}
		if err != nil {
			return Project{}, errors.New("could not configure project remote")
		}
	}
	p.state.Projects[project.ID] = project
	if err := p.save(); err != nil {
		return Project{}, err
	}
	project.Environment = maps.Clone(project.Environment)
	return project, nil
}

func (p *Projects) List() ([]Project, []Workspace) {
	p.mu.Lock()
	defer p.mu.Unlock()
	projects := make([]Project, 0, len(p.state.Projects))
	workspaces := make([]Workspace, 0, len(p.state.Workspaces))
	for _, v := range p.state.Projects {
		v.Environment = maps.Clone(v.Environment)
		projects = append(projects, v)
	}
	for _, v := range p.state.Workspaces {
		v.Environment = nil
		workspaces = append(workspaces, v)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].ID < projects[j].ID })
	sort.Slice(workspaces, func(i, j int) bool { return workspaces[i].TaskID < workspaces[j].TaskID })
	return projects, workspaces
}

func (p *Projects) Archive(id string, archived bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blocked {
		return ErrPaused
	}
	project, ok := p.state.Projects[id]
	if !ok {
		return ErrInvalid
	}
	project.Archived = archived
	p.state.Projects[id] = project
	return p.save()
}

func (p *Projects) Prepare(ctx context.Context, projectID, taskID string, generation uint64) (Workspace, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blocked {
		return Workspace{}, ErrPaused
	}
	if !idPattern.MatchString(taskID) || generation == 0 {
		return Workspace{}, ErrInvalid
	}
	if generation <= p.state.Fences[taskID] {
		return Workspace{}, ErrPaused
	}
	if existing, ok := p.state.Workspaces[taskID]; ok {
		if existing.ProjectID != projectID || generation < existing.Generation {
			return Workspace{}, ErrInvalid
		}
		info, err := os.Lstat(filepath.Join(existing.Path, ".git"))
		if err != nil || !info.Mode().IsRegular() {
			return Workspace{}, ErrInvalid
		}
		// Updating generation never replaces the pinned executable/environment.
		existing.Generation, existing.Paused = generation, false
		existing.Environment = append([]string(nil), p.state.Environments[taskID]...)
		p.state.Workspaces[taskID] = existing
		return existing, p.save()
	}
	project, ok := p.state.Projects[projectID]
	if !ok || project.Archived {
		return Workspace{}, ErrInvalid
	}
	base, err := p.gitCommand(ctx, project.Path, "rev-parse", "--verify", "--end-of-options", project.Baseline+"^{commit}")
	if err != nil {
		return Workspace{}, ErrInvalid
	}
	parent := filepath.Join(p.root, ".frost-worktrees", projectID)
	root, err := os.OpenRoot(p.root)
	if err != nil {
		return Workspace{}, ErrInvalid
	}
	defer root.Close()
	// Create through the root handle: a pre-existing junction/symlink must
	// not create directories outside the configured root before validation.
	if err := root.MkdirAll(filepath.Join(".frost-worktrees", projectID), 0700); err != nil {
		return Workspace{}, ErrInvalid
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil || !inside(p.root, resolvedParent) {
		return Workspace{}, ErrInvalid
	}
	parent = resolvedParent
	path := filepath.Join(parent, taskID)
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return Workspace{}, errors.New("worktree path already exists; reconcile explicitly")
	}
	branch := "frostagent/" + taskID
	if _, err := p.gitCommand(ctx, project.Path, "worktree", "add", "-b", branch, path, strings.TrimSpace(base)); err != nil {
		return Workspace{}, errors.New("could not create task worktree; inspect existing Git state")
	}
	env := pinnedEnvironment(project.Environment)
	w := Workspace{TaskID: taskID, ProjectID: projectID, Path: path, Branch: branch, Shell: project.Shell, Environment: env, Generation: generation}
	p.state.Workspaces[taskID] = w
	p.state.Environments[taskID] = env
	if err := p.save(); err != nil {
		return Workspace{}, err
	}
	return w, nil
}

func pinnedEnvironment(overrides map[string]string) []string {
	values := map[string]string{}
	names := map[string]string{}
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if ok && environmentAllowed(name) {
			key := strings.ToUpper(name)
			values[key] = value
			names[key] = name
		}
	}
	for name, value := range overrides {
		if environmentAllowed(name) && !strings.ContainsRune(value, 0) {
			key := strings.ToUpper(name)
			values[key] = value
			names[key] = name
		}
	}
	env := make([]string, 0, len(values))
	for key, value := range values {
		if key == "PATH" {
			var pinned []string
			for _, dir := range filepath.SplitList(value) {
				if !filepath.IsAbs(dir) {
					continue
				}
				if real, err := filepath.EvalSymlinks(dir); err == nil && !mutableShimDirectory(real) {
					pinned = append(pinned, real)
				}
			}
			value = strings.Join(pinned, string(os.PathListSeparator))
		} else if filepath.IsAbs(value) {
			if real, err := filepath.EvalSymlinks(value); err == nil {
				value = real
			}
		}
		env = append(env, names[key]+"="+value)
	}
	sort.Strings(env)
	return env
}

// Version-manager shims consult mutable defaults outside PATH. Omit them
// rather than silently changing an existing task when a default is switched.
// Projects should prepend immutable version-specific bin directories.
func mutableShimDirectory(dir string) bool {
	name := strings.ToLower(filepath.Base(dir))
	if name == "shims" || name == "windowsapps" {
		return true
	}
	if name == "bin" && strings.EqualFold(filepath.Base(filepath.Dir(dir)), ".cargo") {
		return true
	}
	for _, name := range []string{"rustup", "rustup.exe"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	for _, entry := range entries {
		// File-level aliases are mutable even when the containing directory is
		// already canonical. Exclude the directory rather than follow defaults.
		if entry.Type()&os.ModeSymlink != 0 {
			if runtime.GOOS != "windows" {
				return true
			}
			switch strings.ToLower(filepath.Ext(entry.Name())) {
			case ".exe", ".com", ".cmd", ".bat", ".ps1":
				return true
			}
		}
	}
	return false
}

func validRemote(remote string) bool {
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			return false
		}
		if u.Scheme != "https" && u.Scheme != "ssh" {
			return false
		}
		if u.User != nil {
			if _, password := u.User.Password(); password || u.Scheme != "ssh" {
				return false
			}
		}
		return true
	}
	// Explicit local repositories and SSH scp-style paths are supported.
	if strings.ContainsAny(remote, "?#%") {
		return false
	}
	if at := strings.IndexByte(remote, '@'); at >= 0 {
		return at > 0 && !strings.Contains(remote[:at], ":") && strings.Contains(remote[at+1:], ":")
	}
	return !strings.Contains(remote, "@")
}

func (p *Projects) Workspace(taskID string, generation uint64) (Workspace, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blocked {
		return Workspace{}, ErrPaused
	}
	w, ok := p.state.Workspaces[taskID]
	if !ok || w.Generation != generation {
		return Workspace{}, ErrInvalid
	}
	if w.Paused {
		return Workspace{}, ErrPaused
	}
	w.Environment = append([]string(nil), p.state.Environments[taskID]...)
	return w, nil
}

func (p *Projects) Pause(taskID string, generation uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blocked {
		return ErrPaused
	}
	w, ok := p.state.Workspaces[taskID]
	if !idPattern.MatchString(taskID) || generation == 0 || generation < p.state.Fences[taskID] || (ok && generation < w.Generation) {
		return ErrInvalid
	}
	p.state.Fences[taskID] = generation
	if !ok {
		return p.save()
	}
	w.Paused = true
	w.Generation = generation
	p.state.Workspaces[taskID] = w
	return p.save()
}

// Inherit only OS and toolchain location settings, never ambient API tokens or
// arbitrary service secrets. Git credentials remain in the VM credential store.
func environmentAllowed(name string) bool {
	switch strings.ToUpper(name) {
	case "PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "COMSPEC", "TEMP", "TMP", "TMPDIR",
		"HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA",
		"PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMDATA", "LANG", "LC_ALL",
		"GOROOT", "GOPATH", "GOTOOLCHAIN", "JAVA_HOME", "DOTNET_ROOT", "NVM_HOME",
		"NVM_SYMLINK", "VIRTUAL_ENV", "PYENV_ROOT", "PYTHONHOME",
		"NODE_HOME", "PYTHONPATH", "RUSTUP_HOME", "CARGO_HOME":
		return true
	}
	return false
}

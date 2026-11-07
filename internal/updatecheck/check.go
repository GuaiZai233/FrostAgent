// Package updatecheck reads FrostAgent's public update channels from GitHub.
package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const Repository = "GuaiZai233/FrostAgent"
const repositoryURL = "https://github.com/" + Repository
const cacheTTL = time.Minute

var revisionPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)

type Build struct {
	Display string `json:"display"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

type Channel struct {
	Value  string `json:"value,omitempty"`
	URL    string `json:"url,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type Result struct {
	Current   Build     `json:"current"`
	Stable    Channel   `json:"stable"`
	Latest    Channel   `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

type Source struct {
	Client     *http.Client
	APIBaseURL string // Only overridden by tests, never by request/config input.
	Current    Build
}

type releaseResponse struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

type commitResponse struct {
	SHA string `json:"sha"`
}

func (s Source) Check(ctx context.Context) Result {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	// A fresh request sends no control-plane credentials or cookies.
	client := http.Client{}
	if s.Client != nil {
		client = *s.Client
	}
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	s.Client = &client
	if s.APIBaseURL == "" {
		s.APIBaseURL = "https://api.github.com"
	}
	result := Result{Current: s.Current}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); result.Stable = s.stable(ctx) }()
	go func() { defer wg.Done(); result.Latest = s.latest(ctx) }()
	wg.Wait()
	result.CheckedAt = time.Now().UTC()
	return result
}

func (s Source) stable(ctx context.Context) Channel {
	var release releaseResponse
	if err := s.get(ctx, "/repos/"+Repository+"/releases/latest", &release); err != nil {
		if errors.Is(err, githubStatus(http.StatusNotFound)) {
			return Channel{Status: "no-release", Error: "暂无正式 Release"}
		}
		return unavailable(err)
	}
	tag := release.TagName
	if strings.TrimSpace(tag) != tag || tag == "" || len(tag) > 200 || strings.ContainsAny(tag, "\r\n\t") || release.Draft || release.Prerelease {
		return unavailable(errors.New("invalid release"))
	}
	status := "unknown"
	if s.Current.Version != "" {
		status = "different"
		if strings.TrimPrefix(s.Current.Version, "v") == strings.TrimPrefix(tag, "v") {
			status = "up-to-date"
		}
	}
	return Channel{Value: tag, URL: repositoryURL + "/releases/tag/" + url.PathEscape(tag), Status: status}
}

func (s Source) latest(ctx context.Context) Channel {
	var commits []commitResponse
	// Omitting sha uses the repository's default branch, even if it is renamed.
	if err := s.get(ctx, "/repos/"+Repository+"/commits?per_page=1", &commits); err != nil {
		return unavailable(err)
	}
	if len(commits) != 1 || !revisionPattern.MatchString(commits[0].SHA) {
		return unavailable(errors.New("invalid commit"))
	}
	sha := strings.ToLower(commits[0].SHA)
	status := "unknown"
	if revisionPattern.MatchString(s.Current.Commit) {
		status = "different"
		if strings.EqualFold(s.Current.Commit, sha) {
			status = "up-to-date"
		}
	}
	return Channel{Value: sha, URL: repositoryURL + "/commit/" + sha, Status: status}
}

func VersionHandler(source Source) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !readRequest(w, r) || r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(source.Current)
	})
}

type handler struct {
	source  Source
	mu      sync.Mutex
	cached  *Result
	expires time.Time
	now     func() time.Time
}

// Handler coalesces concurrent checks and caches both channels for one minute.
func Handler(source Source) http.Handler {
	return &handler{source: source, now: time.Now}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !readRequest(w, r) || r.Method == http.MethodHead {
		return
	}
	h.mu.Lock()
	if r.Context().Err() != nil {
		h.mu.Unlock()
		return
	}
	var result Result
	if h.cached != nil && h.now().Before(h.expires) {
		result = *h.cached
	} else {
		result = h.source.Check(r.Context())
		if r.Context().Err() == nil {
			h.cached = &result
			h.expires = h.now().Add(cacheTTL)
		}
	}
	h.mu.Unlock()
	_ = json.NewEncoder(w).Encode(result)
}

func readRequest(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return false
	}
	return true
}

type githubStatus int

func (s githubStatus) Error() string { return fmt.Sprintf("GitHub HTTP %d", s) }

func (s Source) get(ctx context.Context, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.APIBaseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "FrostAgent-update-check")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := s.Client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return githubStatus(response.StatusCode)
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return err
	}
	if len(body) > maxBody {
		return errors.New("GitHub response too large")
	}
	return json.Unmarshal(body, target)
}

func unavailable(err error) Channel {
	message := "暂时无法从 GitHub 获取信息，请稍后重试"
	if errors.Is(err, githubStatus(http.StatusForbidden)) || errors.Is(err, githubStatus(http.StatusTooManyRequests)) {
		message = "GitHub 拒绝请求或触发限流，请稍后重试"
	}
	return Channel{Status: "unavailable", Error: message}
}

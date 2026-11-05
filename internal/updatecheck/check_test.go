package updatecheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandlerChecksStableReleaseAndLatestCommit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" || r.Header.Get("Accept") == "" {
			t.Error("GitHub request headers are missing")
		}
		switch r.URL.Path {
		case "/repos/GuaiZai233/FrostAgent/releases/latest":
			_ = json.NewEncoder(w).Encode(releaseResponse{TagName: "v1.2.3"})
		case "/repos/GuaiZai233/FrostAgent/commits":
			if r.URL.RawQuery != "per_page=1" {
				t.Errorf("unexpected query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]commitResponse{{SHA: strings.Repeat("a", 40)}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	response := Handler(Source{
		Client: server.Client(), APIBaseURL: server.URL,
		Current: Build{Display: "1.2.3", Version: "1.2.3", Commit: strings.Repeat("a", 40)},
	}).ServeHTTP
	recorder := httptest.NewRecorder()
	response(recorder, httptest.NewRequest(http.MethodGet, "/api/update-check", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var result Result
	if err := json.NewDecoder(recorder.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Stable.Status != "up-to-date" || result.Stable.Value != "v1.2.3" {
		t.Fatalf("stable = %+v", result.Stable)
	}
	if result.Latest.Status != "up-to-date" || len(result.Latest.Value) != 40 {
		t.Fatalf("latest = %+v", result.Latest)
	}
}

func TestMissingReleaseDoesNotHideLatestCommit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]commitResponse{{SHA: strings.Repeat("b", 40)}})
	}))
	defer server.Close()

	result := Source{Client: server.Client(), APIBaseURL: server.URL, Current: Build{Commit: strings.Repeat("a", 40)}}.Check(context.Background())
	if result.Stable.Status != "no-release" || result.Stable.Error != "暂无正式 Release" {
		t.Fatalf("stable = %+v", result.Stable)
	}
	if result.Latest.Status != "different" {
		t.Fatalf("latest = %+v", result.Latest)
	}
}

func TestComparisonsRequireFullBuildIdentity(t *testing.T) {
	sha := strings.Repeat("a", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			_ = json.NewEncoder(w).Encode(releaseResponse{TagName: "v1.2.3"})
		} else {
			_ = json.NewEncoder(w).Encode([]commitResponse{{SHA: sha}})
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		name, version, commit, stable, latest string
	}{
		{"release", "v1.2.3", sha, "up-to-date", "up-to-date"},
		{"prefix collision", "1.2.3", sha[:7] + strings.Repeat("b", 33), "up-to-date", "different"},
		{"short hash", "", sha[:7], "unknown", "unknown"},
		{"unknown", "", "", "unknown", "unknown"},
		{"uppercase", "", strings.ToUpper(sha), "unknown", "up-to-date"},
		{"newer local release", "2.0.0", sha, "different", "up-to-date"},
		{"prerelease", "1.2.3-rc.1", sha, "different", "up-to-date"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := Source{Client: server.Client(), APIBaseURL: server.URL, Current: Build{Version: tc.version, Commit: tc.commit}}.Check(context.Background())
			if result.Stable.Status != tc.stable || result.Latest.Status != tc.latest {
				t.Fatalf("unexpected comparison: %+v", result)
			}
		})
	}
}

func TestFailurePathsAreNotReportedAsCurrent(t *testing.T) {
	for _, tc := range []struct {
		name, release, commits string
		status                 int
	}{
		{"rate limited", `{}`, `[]`, 429},
		{"forbidden", `{}`, `[]`, 403},
		{"network unavailable", `{}`, `[]`, 503},
		{"invalid JSON", `{`, `[`, 200},
		{"empty", `{}`, `[]`, 200},
		{"short hash", `{"tag_name":"v1"}`, `[{"sha":"abcdefg"}]`, 200},
		{"oversize", strings.Repeat(" ", (1<<20)+1), `[]`, 200},
		{"draft", `{"tag_name":"v1.0.0","draft":true}`, `[]`, 200},
		{"prerelease", `{"tag_name":"v1.0.0-rc.1","prerelease":true}`, `[]`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if strings.HasSuffix(r.URL.Path, "/releases/latest") {
					_, _ = w.Write([]byte(tc.release))
				} else {
					_, _ = w.Write([]byte(tc.commits))
				}
			}))
			defer server.Close()
			result := Source{Client: server.Client(), APIBaseURL: server.URL}.Check(context.Background())
			if result.Stable.Status == "up-to-date" || result.Latest.Status != "unavailable" {
				t.Fatalf("result: %+v", result)
			}
			if tc.name != "short hash" && result.Stable.Status != "unavailable" {
				t.Fatalf("stable: %+v", result.Stable)
			}
		})
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer server.Close()
	result := Source{Client: server.Client(), APIBaseURL: server.URL}.Check(context.Background())
	if forwarded.Load() != 0 || result.Stable.Status != "unavailable" || result.Latest.Status != "unavailable" {
		t.Fatalf("redirect followed: %+v", result)
	}
}

func TestCacheCoalescesChecksAndExpires(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("management credentials sent to GitHub")
		}
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]commitResponse{{SHA: strings.Repeat("a", 40)}})
	}))
	defer server.Close()
	h := Handler(Source{Client: server.Client(), APIBaseURL: server.URL}).(*handler)
	now := time.Now()
	h.now = func() time.Time { return now }
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodGet, "/api/update-check?repository=attacker&sha=other", nil)
			r.Header.Set("Authorization", "Bearer synthetic-control-token")
			r.Header.Set("Cookie", "synthetic-cookie=value")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Errorf("status: %d", w.Code)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
	now = now.Add(cacheTTL)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/update-check", nil))
	if calls.Load() != 4 {
		t.Fatalf("cache failed to expire: %d", calls.Load())
	}
}

func TestVersionAndHEADNeverContactGitHub(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	source := Source{Client: server.Client(), APIBaseURL: server.URL, Current: Build{Display: "unknown"}}
	for _, h := range []http.Handler{Handler(source), VersionHandler(source)} {
		for _, method := range []string{http.MethodHead, http.MethodPost} {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, "/", nil))
		}
	}
	w := httptest.NewRecorder()
	VersionHandler(source).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if !strings.Contains(w.Body.String(), `"display":"unknown"`) || calls.Load() != 0 {
		t.Fatalf("unexpected response/network: %s, %d", w.Body.String(), calls.Load())
	}
}

func TestCancellationDoesNotPoisonCache(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := Handler(Source{}).(*handler)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/update-check", nil).WithContext(ctx))
	if h.cached != nil {
		t.Fatal("cancelled request was cached")
	}
	result := Source{}.Check(ctx)
	if result.Stable.Status != "unavailable" || result.Latest.Status != "unavailable" {
		t.Fatalf("cancelled check: %+v", result)
	}
}

func TestSlowReleaseDoesNotHideLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			<-r.Context().Done()
			return
		}
		_ = json.NewEncoder(w).Encode([]commitResponse{{SHA: strings.Repeat("a", 40)}})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	result := Source{Client: server.Client(), APIBaseURL: server.URL}.Check(ctx)
	if result.Stable.Status != "unavailable" || result.Latest.Value != strings.Repeat("a", 40) {
		t.Fatalf("slow release hid latest: %+v", result)
	}
}

func TestHandlerRejectsWrites(t *testing.T) {
	recorder := httptest.NewRecorder()
	Handler(Source{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/update-check", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", recorder.Code)
	}
}

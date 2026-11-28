package frostctl

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// runAgainst executes Run with its HTTP client pointed at server.
func runAgainst(t *testing.T, server *httptest.Server, args ...string) (int, string, string) {
	t.Helper()
	previous := openClient
	openClient = func(context.Context, Invocation) (*client, error) {
		return &client{base: server.URL, http: server.Client()}, nil
	}
	t.Cleanup(func() { openClient = previous })
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

type toolEntryRequest struct {
	RefID         string `json:"refId"`
	SessionID     string `json:"sessionId"`
	IncludeResult bool   `json:"includeResult"`
	ResultOffset  int    `json:"resultOffset"`
}

// toolHistoryServer pages a synthetic result in 8000-character chunks.
func toolHistoryServer(t *testing.T, total int, requests *[]toolEntryRequest) *httptest.Server {
	var mu sync.Mutex
	result := strings.Repeat("r", total)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "GetToolHistoryEntry") {
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var req toolEntryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		mu.Lock()
		*requests = append(*requests, req)
		mu.Unlock()
		response := map[string]any{"refId": req.RefID, "tool": "synthetic_tool", "argumentsAvailable": true}
		if !req.IncludeResult {
			response["argumentsRaw"] = `{"query":"synthetic"}`
		} else {
			end := min(req.ResultOffset+8000, total)
			response["resultRaw"] = result[req.ResultOffset:end]
			response["resultAvailable"] = true
			response["resultOffset"] = req.ResultOffset
			response["resultTotalChars"] = total
			if end < total {
				response["resultHasMore"] = true
				response["resultNextOffset"] = end
			}
		}
		json.NewEncoder(w).Encode(response)
	}))
}

func TestSessionToolShowOmitsResultUnlessRequested(t *testing.T) {
	for _, args := range [][]string{{}, {"--include-result=false"}} {
		var requests []toolEntryRequest
		server := toolHistoryServer(t, 100, &requests)
		command := append([]string{"session", "tool", "show", "group:1", "call-1", "--instance", "synthetic", "--output", "json"}, args...)
		code, stdout, stderr := runAgainst(t, server, command...)
		server.Close()
		if code != 0 {
			t.Fatalf("%v exit=%d stderr=%s", args, code, stderr)
		}
		if len(requests) != 1 || requests[0].IncludeResult {
			t.Fatalf("%v sent %+v", args, requests)
		}
		if strings.Contains(stdout, "resultRaw") || !strings.Contains(stdout, "argumentsRaw") {
			t.Fatalf("%v default output exposed the tool result: %s", args, stdout)
		}
	}
	var requests []toolEntryRequest
	server := toolHistoryServer(t, 100, &requests)
	defer server.Close()
	code, stdout, _ := runAgainst(t, server, "session", "tool", "show", "group:1", "call-1", "--instance", "synthetic", "--include-result", "--output", "json")
	if code != 0 || len(requests) != 2 || !requests[1].IncludeResult || !strings.Contains(stdout, "resultRaw") {
		t.Fatalf("explicit result request failed: exit=%d requests=%+v", code, requests)
	}
}

func TestSessionToolShowFollowsServerOffsetsToTheEnd(t *testing.T) {
	var requests []toolEntryRequest
	server := toolHistoryServer(t, 20000, &requests)
	defer server.Close()
	offset, read := 0, 0
	for page := 0; page < 5; page++ {
		args := []string{"session", "tool", "show", "group:1", "call-1", "--instance", "synthetic", "--include-result", "--output", "json"}
		if offset > 0 {
			args = append(args, "--offset", strconv.Itoa(offset))
		}
		code, stdout, stderr := runAgainst(t, server, args...)
		if code != 0 {
			t.Fatalf("offset %d rejected: exit=%d %s", offset, code, stderr)
		}
		var decoded struct {
			Data struct {
				Result struct {
					ResultRaw        string `json:"resultRaw"`
					ResultHasMore    bool   `json:"resultHasMore"`
					ResultNextOffset int    `json:"resultNextOffset"`
				} `json:"result"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
			t.Fatal(err)
		}
		read += len(decoded.Data.Result.ResultRaw)
		if !decoded.Data.Result.ResultHasMore {
			if page != 2 || read != 20000 {
				t.Fatalf("read %d characters over %d pages", read, page+1)
			}
			return
		}
		offset = decoded.Data.Result.ResultNextOffset
	}
	t.Fatal("result pagination did not finish")
}

func TestAuditOffsetKeepsItsOwnLimit(t *testing.T) {
	if _, err := Parse([]string{"security", "audit", "list", "--instance", "synthetic", "--offset", "10001"}); err == nil {
		t.Fatal("audit offset above 10000 accepted")
	}
	if _, err := Parse([]string{"session", "tool", "show", "group:1", "call-1", "--instance", "synthetic", "--offset", "-1"}); err == nil {
		t.Fatal("negative result offset accepted")
	}
}

func TestUnknownWriteTableOutputShowsRecoveryDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "UpdateEnvVars") {
			w.WriteHeader(503)
			io.WriteString(w, `{"state":"unknown","operationId":"synthetic-operation"}`)
			return
		}
		io.WriteString(w, `{"envVars":[]}`)
	}))
	defer server.Close()
	for _, output := range []string{"table", "json"} {
		args := []string{"config", "set", "BOT_NAME", "--value", "Synthetic", "--instance", "synthetic-instance"}
		if output == "json" {
			args = append(args, "--output", "json")
		}
		code, stdout, stderr := runAgainst(t, server, args...)
		if code != 3 {
			t.Fatalf("%s exit=%d stdout=%s stderr=%s", output, code, stdout, stderr)
		}
		combined := stdout + stderr
		wants := []string{`"config"`, `"list"`, "synthetic-instance", "synthetic-operation"}
		if output == "table" {
			wants = []string{"verify: frostctl config list --instance synthetic-instance", "operationId: synthetic-operation"}
		}
		for _, want := range wants {
			if !strings.Contains(combined, want) {
				t.Fatalf("%s output lacks %q:\nstdout=%s\nstderr=%s", output, want, stdout, stderr)
			}
		}
	}
}

package onebot

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot/content"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/sandbox"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
)

const fixtureFileURL = "https://multimedia.nt.qq.com.cn/download?token=fixture-private-token"

type inboundRoundTripper func(*http.Request) (*http.Response, error)

func (f inboundRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Emulates only the sandbox transport; the production Python writer itself is
// exercised separately on Linux. Files remain separated by the backend session.
type inboundMemoryBackend struct {
	mu     sync.Mutex
	files  map[string][]byte
	stages map[string][]byte
	calls  int
}

func newInboundBackend() *inboundMemoryBackend {
	return &inboundMemoryBackend{files: map[string][]byte{}, stages: map[string][]byte{}}
}
func (*inboundMemoryBackend) Health(context.Context) error          { return nil }
func (*inboundMemoryBackend) Release(context.Context, string) error { return nil }
func (b *inboundMemoryBackend) Exec(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	if err := ctx.Err(); err != nil {
		return sandbox.ExecResult{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	code := 0
	result := func(value any) (sandbox.ExecResult, error) {
		data, _ := json.Marshal(value)
		return sandbox.ExecResult{Stdout: string(data), ExitCode: &code}, nil
	}
	if strings.HasPrefix(req.Command, "read-docx ") {
		data := b.files[req.SessionID+":"+strings.TrimPrefix(req.Command, "read-docx ")]
		reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return sandbox.ExecResult{}, err
		}
		for _, file := range reader.File {
			if file.Name == "word/document.xml" {
				r, err := file.Open()
				if err != nil {
					return sandbox.ExecResult{}, err
				}
				defer r.Close()
				text, _ := io.ReadAll(r)
				return sandbox.ExecResult{Stdout: string(text), ExitCode: &code}, nil
			}
		}
		return sandbox.ExecResult{}, errors.New("document XML missing")
	}
	i := strings.LastIndex(req.Command, " '")
	if i < 0 || !strings.HasSuffix(req.Command, "'") {
		return sandbox.ExecResult{}, errors.New("invalid fixture command")
	}
	encoded, err := base64.StdEncoding.DecodeString(req.Command[i+2 : len(req.Command)-1])
	if err != nil {
		return sandbox.ExecResult{}, err
	}
	var q struct {
		Op, Path, Stage, Data, Hash string
		Offset                      int
		Size                        int64
	}
	if err := json.Unmarshal(encoded, &q); err != nil {
		return sandbox.ExecResult{}, err
	}
	key, stage := req.SessionID+":/sandbox/"+q.Path, req.SessionID+":"+q.Stage
	switch q.Op {
	case "check":
		hash := sha256.Sum256(b.files[key])
		return result(map[string]any{"present": int64(len(b.files[key])) == q.Size && hex.EncodeToString(hash[:]) == q.Hash})
	case "write":
		data, err := base64.StdEncoding.DecodeString(q.Data)
		if err != nil || len(b.stages[stage]) != q.Offset {
			return sandbox.ExecResult{}, errors.New("invalid upload chunk")
		}
		b.stages[stage] = append(b.stages[stage], data...)
	case "commit":
		data := b.stages[stage]
		hash := sha256.Sum256(data)
		if int64(len(data)) != q.Size || hex.EncodeToString(hash[:]) != q.Hash {
			return sandbox.ExecResult{}, errors.New("invalid upload hash")
		}
		b.files[key] = append([]byte(nil), data...)
		delete(b.stages, stage)
	case "abort":
		delete(b.stages, stage)
	default:
		return sandbox.ExecResult{}, errors.New("unsupported fixture operation")
	}
	return result(map[string]any{"ok": true})
}

func inboundFixtureClient(data []byte, count *atomic.Int32) *http.Client {
	return &http.Client{Transport: inboundRoundTripper(func(r *http.Request) (*http.Response, error) {
		count.Add(1)
		return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header), Request: r}, nil
	})}
}

func fileEvent(mid int32, peer int64, name string, size int64) model.OneBotEvent {
	raw, _ := json.Marshal([]content.MessageSegment{{Type: "file", Data: map[string]any{"name": name, "file_id": "fixture-file-id", "url": fixtureFileURL, "file_size": size}}})
	return model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: 99, UserID: peer, MessageID: mid, Message: raw}
}

func fileTestConnection(t *testing.T, data []byte) (*wsConnection, *inboundMemoryBackend, *atomic.Int32) {
	t.Helper()
	backend := newInboundBackend()
	engine := newTestEngine(&mockLLMProvider{})
	engine.SandboxBackend = backend
	c := newWSConnection(nil)
	c.engine = engine
	count := &atomic.Int32{}
	c.fileHTTPClient = inboundFixtureClient(data, count)
	t.Cleanup(func() { c.cancel() })
	return c, backend, count
}

func parseReceivedFiles(t *testing.T, prompt string) []inboundFileInfo {
	t.Helper()
	i, j := strings.Index(prompt, "\n["), strings.Index(prompt, "\n</received_files>")
	if i < 0 || j < i {
		t.Fatalf("missing received files: %s", prompt)
	}
	var infos []inboundFileInfo
	if err := json.Unmarshal([]byte(prompt[i+1:j]), &infos); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "fixture-private-token") || strings.Contains(prompt, "fixture-file-id") || strings.Contains(prompt, "base64://") {
		t.Fatal("credentials leaked into attachment prompt")
	}
	return infos
}

func TestInboundFilesReuseIsolationAndExpiry(t *testing.T) {
	data := bytes.Repeat([]byte("document fixture"), 5000)
	c, b, count := fileTestConnection(t, data)
	event := fileEvent(101, 11, "../复习题.docx", int64(len(data)))
	session := c.engine.SessionManager.GetOrCreate(c.historyKey(event))
	turn := session.ReserveSequenceTurn("sender:11", true, false)
	turn.Done()
	c.rememberInboundFiles(event)
	for pass := 0; pass < 2; pass++ {
		infos := parseReceivedFiles(t, c.prepareInboundFiles(context.Background(), event, session, nil))
		if len(infos) != 1 || infos[0].Status != "ready" || infos[0].Name != "复习题.docx" || !strings.HasPrefix(infos[0].Path, "/sandbox/received/") {
			t.Fatalf("import: %+v", infos)
		}
		b.mu.Lock()
		got := b.files[c.historyKey(event)+":"+infos[0].Path]
		b.mu.Unlock()
		if !bytes.Equal(got, data) {
			t.Fatal("attachment bytes were not preserved")
		}
	}
	if count.Load() != 1 {
		t.Fatalf("cached file downloaded %d times", count.Load())
	}
	b.mu.Lock()
	b.files = map[string][]byte{}
	b.mu.Unlock()
	infos := parseReceivedFiles(t, c.prepareInboundFiles(context.Background(), event, session, nil))
	if infos[0].Status != "ready" || count.Load() != 2 {
		t.Fatal("sandbox expiry did not reimport attachment")
	}
	other := event
	other.UserID = 12
	otherSession := c.engine.SessionManager.GetOrCreate(c.historyKey(other))
	if got := c.prepareInboundFiles(context.Background(), other, otherSession, nil); got != "" {
		t.Fatal("file crossed private sessions")
	}
	foreign := &resolvedReplyContext{MessageID: "101", SessionID: c.historyKey(event), Segments: ParseMessageSegments(event.Message)}
	if got := c.prepareInboundFiles(context.Background(), other, otherSession, []*resolvedReplyContext{foreign}); got != "" {
		t.Fatal("foreign quote imported")
	}
	if err := session.ResetSession(nil); err != nil {
		t.Fatal(err)
	}
	if got := c.prepareInboundFiles(context.Background(), event, session, nil); got != "" {
		t.Fatal("file survived session reset")
	}
	fresh := newWSConnection(nil)
	fresh.engine = c.engine
	defer fresh.cancel()
	if got := fresh.prepareInboundFiles(context.Background(), event, session, nil); got != "" {
		t.Fatal("file crossed connections")
	}
}

func TestInboundFilesQuoteAndSameName(t *testing.T) {
	c, _, count := fileTestConnection(t, []byte("fixture"))
	event := fileEvent(101, 11, "same.docx", 7)
	session := c.engine.SessionManager.GetOrCreate(c.historyKey(event))
	c.rememberInboundFiles(event)
	second := fileEvent(102, 11, "same.docx", 7)
	c.rememberInboundFiles(second)
	infos := parseReceivedFiles(t, c.prepareInboundFiles(context.Background(), second, session, nil))
	if len(infos) != 2 || infos[0].Path == infos[1].Path {
		t.Fatal("same filenames overwrote each other")
	}
	quoted := fileEvent(103, 11, "quoted.docx", 7)
	quote := &resolvedReplyContext{MessageID: "103", SessionID: historyKey(event), Segments: ParseMessageSegments(quoted.Message)}
	infos = parseReceivedFiles(t, c.prepareInboundFiles(context.Background(), event, session, []*resolvedReplyContext{quote}))
	if infos[0].Name != "quoted.docx" || infos[0].Status != "ready" || count.Load() != 3 {
		t.Fatalf("quote import: %+v count=%d", infos, count.Load())
	}
}

func TestInboundFileLimitsCancellationAndDisabled(t *testing.T) {
	c, b, count := fileTestConnection(t, []byte("fixture"))
	event := fileEvent(101, 11, "file.docx", sandbox.MaxFileBytes+1)
	c.rememberInboundFiles(event)
	session := c.engine.SessionManager.GetOrCreate(c.historyKey(event))
	info := parseReceivedFiles(t, c.prepareInboundFiles(context.Background(), event, session, nil))[0]
	if info.Status != "unavailable" || count.Load() != 0 || b.calls != 0 {
		t.Fatal("oversize file was downloaded or imported")
	}
	event = fileEvent(102, 11, "file.docx", 7)
	c.rememberInboundFiles(event)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	info = parseReceivedFiles(t, c.prepareInboundFiles(ctx, event, session, nil))[0]
	if info.Status != "unavailable" || count.Load() != 0 {
		t.Fatal("canceled import performed a download")
	}
	c.engine.SandboxBackend = nil
	info = parseReceivedFiles(t, c.prepareInboundFiles(context.Background(), event, session, nil))[0]
	if info.Status != "unavailable" || !strings.Contains(info.Error, "禁用") || count.Load() != 0 {
		t.Fatal("disabled sandbox downloaded a file")
	}
	for i := 0; i < maxInboundFileRecords+5; i++ {
		c.rememberInboundFiles(fileEvent(int32(1000+i), 11, "file.docx", 7))
	}
	if len(c.inboundFiles) > maxInboundFileRecords {
		t.Fatal("attachment cache is unbounded")
	}
	c.inboundFiles[0].seen = time.Now().Add(-2 * inboundFileLifetime)
	c.rememberInboundFiles(fileEvent(9999, 11, "new.docx", 7))
	for _, f := range c.inboundFiles {
		if time.Since(f.seen) > inboundFileLifetime {
			t.Fatal("expired descriptor retained")
		}
	}
}

func TestInboundGroupQuoteCanReuseAnotherSendersObservedFile(t *testing.T) {
	c, _, count := fileTestConnection(t, []byte("fixture"))
	event := fileEvent(101, 11, "group.docx", 7)
	event.MessageType = "group"
	event.GroupID = 22
	c.rememberInboundFiles(event)
	session := c.engine.SessionManager.GetOrCreate(c.historyKey(event))
	_ = c.prepareInboundFiles(context.Background(), event, session, nil)
	question := event
	question.UserID = 12
	question.MessageID = 102
	if got := c.prepareInboundFiles(context.Background(), question, session, nil); got != "" {
		t.Fatal("unquoted file selected for another sender")
	}
	quote := &resolvedReplyContext{MessageID: "101", SessionID: historyKey(event), Segments: ParseMessageSegments(event.Message)}
	infos := parseReceivedFiles(t, c.prepareInboundFiles(context.Background(), question, session, []*resolvedReplyContext{quote}))
	if len(infos) != 1 || infos[0].Status != "ready" || count.Load() != 1 {
		t.Fatalf("same-group quote did not reuse file: %+v", infos)
	}
}

func TestInboundFileURLPolicy(t *testing.T) {
	for _, raw := range []string{fixtureFileURL, "http://sh.example.ftn.qq.com/ftn_handler/fixture", "https://gchat.qpic.cn/a"} {
		if !allowedInboundFileURL(raw) {
			t.Fatalf("trusted URL rejected: %s", raw)
		}
	}
	for _, raw := range []string{"file:///etc/passwd", "https://127.0.0.1/a", "https://10.0.0.1/a", "https://multimedia.nt.qq.com.cn.evil.invalid/a", "https://evil.invalid/a", "https://multimedia.nt.qq.com.cn@evil.invalid/a", "https://multimedia.nt.qq.com.cn:8080/a", "https://user:pass@multimedia.nt.qq.com.cn/a", "https://multimedia.nt.qq.com.cn/a#fragment"} {
		if allowedInboundFileURL(raw) {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
	client := newInboundFileHTTPClient()
	for _, target := range []string{"http://127.0.0.1/a", "https://evil.invalid/a"} {
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		if client.CheckRedirect(req, []*http.Request{{}}) == nil {
			t.Fatal("unsafe redirect allowed")
		}
	}
	c, _, count := fileTestConnection(t, []byte("fixture"))
	if _, err := c.downloadInboundFile(context.Background(), "http://127.0.0.1/a", 10); err == nil || count.Load() != 0 {
		t.Fatal("unsafe source reached HTTP client")
	}
	if _, err := c.downloadInboundFile(context.Background(), fixtureFileURL, 2); err == nil {
		t.Fatal("oversize response accepted")
	}
}

func TestInboundUploadNoticesStayInSourceConversation(t *testing.T) {
	for _, notice := range []string{"group_upload", "offline_file"} {
		t.Run(notice, func(t *testing.T) {
			c, _, count := fileTestConnection(t, []byte("fixture"))
			file, _ := json.Marshal(map[string]any{"name": "notice.docx", "id": "fixture-id", "url": fixtureFileURL, "size": 7})
			event := model.OneBotEvent{PostType: "notice", NoticeType: notice, SelfID: 99, UserID: 11, File: file}
			if notice == "group_upload" {
				event.GroupID = 22
			}
			c.rememberInboundFileNotice(event)
			c.rememberInboundFileNotice(event)
			if len(c.inboundFiles) != 1 || count.Load() != 0 {
				t.Fatal("notice duplicated or started download")
			}
			question := model.OneBotEvent{PostType: "message", MessageType: "private", SelfID: 99, UserID: 11, MessageID: 102}
			if notice == "group_upload" {
				question.MessageType = "group"
				question.GroupID = 22
			}
			session := c.engine.SessionManager.GetOrCreate(c.historyKey(question))
			infos := parseReceivedFiles(t, c.prepareInboundFiles(context.Background(), question, session, nil))
			if len(infos) != 1 || infos[0].Status != "ready" || infos[0].MessageID != "" {
				t.Fatalf("notice attachment: %+v", infos)
			}
			other := question
			other.UserID = 12
			if got := c.prepareInboundFiles(context.Background(), other, session, nil); got != "" {
				t.Fatal("unquoted notice leaked to a different sender")
			}
		})
	}
}

func TestInboundMissingURLResolvesGateway(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			backend := newInboundBackend()
			var count atomic.Int32
			provider := &mockLLMProvider{customChat: func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				prompt, _ := req.Messages[len(req.Messages)-1].Content.(string)
				infos := parseReceivedFiles(t, prompt)
				if len(infos) != 1 || infos[0].Status != "ready" {
					t.Errorf("file URL resolution failed: %+v", infos)
				}
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "文件已读取。"}}, nil
			}}
			engine := newTestEngine(provider)
			engine.SandboxBackend = backend
			server, address := startInboundServer(t, engine, inboundFixtureClient([]byte("fixture"), &count))
			defer server.Close()
			ws, _, err := websocket.DefaultDialer.Dial(address, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			event := fileEvent(101, 11, "file.docx", 7)
			event.MessageType = kind
			segments := ParseMessageSegments(event.Message)
			delete(segments[0].Data, "url")
			if kind == "group" {
				event.GroupID = 22
				segments = append(segments, content.MessageSegment{Type: "at", Data: map[string]any{"qq": "99"}})
			}
			event.Message, _ = json.Marshal(segments)
			if err := ws.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
			resolved := false
			for {
				var action model.OneBotAction
				if err := ws.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				response := map[string]any{"message_id": 102}
				switch action.Action {
				case "get_private_file_url", "get_group_file_url":
					want := "get_" + kind + "_file_url"
					if action.Action != want || action.Params.(map[string]any)["file_id"] != "fixture-file-id" {
						t.Errorf("wrong URL request: %s", action.Action)
					}
					response = map[string]any{"url": fixtureFileURL}
					resolved = true
				case "get_group_info":
					response = map[string]any{"group_id": 22, "group_name": "fixture group"}
				case "send_private_msg", "send_group_msg":
					_ = ws.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": response})
					if !resolved || count.Load() != 1 {
						t.Fatalf("resolution=%v downloads=%d", resolved, count.Load())
					}
					return
				default:
					t.Fatalf("unexpected action: %s", action.Action)
				}
				_ = ws.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": response})
			}
		})
	}
}

func fixtureDOCX(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(file, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>第一题：计算 2+3。</w:t></w:r></w:p></w:body></w:document>`)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func startInboundServer(t *testing.T, engine *llm.Engine, client *http.Client) (*httptest.Server, string) {
	t.Helper()
	disableTestThinkingStatus(t, engine)
	adapter := NewAdapter(engine)
	adapter.fileHTTPClient = client
	server := httptest.NewServer(http.HandlerFunc(adapter.Handler()))
	return server, "ws" + strings.TrimPrefix(server.URL, "http")
}

func TestInboundFileThenImmediateQuestionThroughTools(t *testing.T) {
	data := fixtureDOCX(t)
	backend := newInboundBackend()
	downloadStarted := make(chan struct{})
	var count atomic.Int32
	client := &http.Client{Transport: inboundRoundTripper(func(r *http.Request) (*http.Response, error) {
		if count.Add(1) == 1 {
			close(downloadStarted)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header), Request: r}, nil
	})}
	calls := 0
	provider := &mockLLMProvider{customChat: func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		calls++
		if calls == 1 {
			prompt, _ := req.Messages[len(req.Messages)-1].Content.(string)
			infos := parseReceivedFiles(t, prompt)
			if len(infos) != 1 || infos[0].Status != "ready" {
				t.Fatalf("file missing after injection: %+v", infos)
			}
			arguments, _ := json.Marshal(map[string]string{"command": "read-docx " + infos[0].Path})
			return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "read", Type: "function", Function: core.ToolCallFunction{Name: "execute_command", Arguments: string(arguments)}}}}}, nil
		}
		result, _ := req.Messages[len(req.Messages)-1].Content.(string)
		if !strings.Contains(result, "第一题") {
			t.Errorf("document contents unavailable: %s", result)
		}
		return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "第一题答案为 5。"}}, nil
	}}
	engine := newTestEngine(provider)
	engine.SandboxBackend = backend
	engine.ToolRegistry["execute_command"] = tools.ExecuteCommandTool(backend)
	server, address := startInboundServer(t, engine, client)
	defer server.Close()
	ws, _, err := websocket.DefaultDialer.Dial(address, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	event := fileEvent(101, 11, "复习题1.docx", int64(len(data)))
	if err := ws.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	select {
	case <-downloadStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("file download did not start")
	}
	event.MessageID = 102
	event.Message = json.RawMessage(`[{"type":"text","data":{"text":"第一题怎么写"}}]`)
	if err := ws.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var action model.OneBotAction
	if err := ws.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	serialized, _ := json.Marshal(action.Params)
	if action.Action != "send_private_msg" || !bytes.Contains(serialized, []byte("第一题答案为 5")) {
		t.Fatalf("unexpected response: %s %s", action.Action, serialized)
	}
	_ = ws.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 103}})
	if calls != 2 || count.Load() != 2 {
		t.Fatalf("model calls=%d downloads=%d", calls, count.Load())
	}
}

func TestInboundQuotedFileViaGetMsg(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			data := fixtureDOCX(t)
			backend := newInboundBackend()
			var count atomic.Int32
			provider := &mockLLMProvider{customChat: func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				prompt, _ := req.Messages[len(req.Messages)-1].Content.(string)
				infos := parseReceivedFiles(t, prompt)
				if len(infos) != 1 || infos[0].Status != "ready" || infos[0].MessageID != "101" {
					t.Errorf("quoted file not imported: %+v", infos)
				}
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "附件可读取。"}}, nil
			}}
			engine := newTestEngine(provider)
			engine.SandboxBackend = backend
			server, address := startInboundServer(t, engine, inboundFixtureClient(data, &count))
			defer server.Close()
			ws, _, err := websocket.DefaultDialer.Dial(address, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			event := fileEvent(102, 11, "unused.docx", 0)
			event.MessageType = kind
			if kind == "group" {
				event.GroupID = 22
			}
			event.Message = json.RawMessage(`[{"type":"reply","data":{"id":"101"}},{"type":"at","data":{"qq":"99"}},{"type":"text","data":{"text":"读一下引用文件"}}]`)
			if err := ws.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
			for {
				var action model.OneBotAction
				if err := ws.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				if action.Action == "get_msg" {
					file := fileEvent(101, 11, "quoted.docx", int64(len(data)))
					response := map[string]any{"message_id": 101, "message_type": kind, "user_id": 11, "message": file.Message}
					if kind == "group" {
						response["group_id"] = 22
					}
					if err := ws.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": response}); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if action.Action == "get_group_info" {
					_ = ws.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"group_id": 22, "group_name": "fixture group"}})
					continue
				}
				if strings.HasPrefix(action.Action, "send_") {
					_ = ws.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 103}})
					break
				}
				t.Fatalf("unexpected action: %s", fmt.Sprint(action.Action))
			}
			if count.Load() != 1 {
				t.Fatalf("quoted file downloads=%d", count.Load())
			}
		})
	}
}

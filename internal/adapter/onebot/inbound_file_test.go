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
	"github.com/GuaiZai233/FrostAgent/internal/security"
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
	health error
}

func newInboundBackend() *inboundMemoryBackend {
	return &inboundMemoryBackend{files: map[string][]byte{}, stages: map[string][]byte{}}
}
func (b *inboundMemoryBackend) Health(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.health
}
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
	case "begin", "write":
		data, err := base64.StdEncoding.DecodeString(q.Data)
		if q.Op == "begin" {
			if _, exists := b.stages[stage]; exists || q.Offset != 0 {
				return sandbox.ExecResult{}, errors.New("duplicate upload")
			}
			b.stages[stage] = make([]byte, q.Size)
		}
		if err != nil || int64(len(b.stages[stage])) != q.Size || q.Offset < 0 || q.Offset+len(data) > len(b.stages[stage]) {
			return sandbox.ExecResult{}, errors.New("invalid upload chunk")
		}
		copy(b.stages[stage][q.Offset:], data)
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
	for _, info := range infos {
		if info.Status != "available" || info.Path != "" || !strings.HasPrefix(info.Ref, "file_") {
			t.Fatalf("listing must be metadata only: %+v", info)
		}
	}
	return infos
}

// listTurn builds the per-turn attachment state and returns its listing.
func listTurn(t *testing.T, c *wsConnection, event model.OneBotEvent, session *llm.SessionContext, quotes ...*resolvedReplyContext) (*inboundFileTurn, []inboundFileInfo) {
	t.Helper()
	turn := c.inboundFileTurn(event, session, quotes)
	prompt := turn.prompt()
	if prompt == "" {
		return turn, nil
	}
	return turn, parseReceivedFiles(t, prompt)
}

func TestInboundFilesListedWithoutDownload(t *testing.T) {
	c, b, count := fileTestConnection(t, []byte("fixture"))
	event := fileEvent(101, 11, "notes.docx", 7)
	session := c.engine.SessionManager.GetOrCreate(c.historyKey(event))
	c.rememberInboundFiles(event)
	_, infos := listTurn(t, c, event, session)
	if len(infos) != 1 || infos[0].Name != "notes.docx" || infos[0].MessageID != "101" || infos[0].Size != 7 {
		t.Fatalf("listing: %+v", infos)
	}
	_, again := listTurn(t, c, event, session)
	if again[0].Ref != infos[0].Ref {
		t.Fatal("attachment reference is not stable across turns")
	}
	if count.Load() != 0 || b.calls != 0 {
		t.Fatalf("listing downloaded=%d sandbox calls=%d", count.Load(), b.calls)
	}
}

func TestInboundFilesReuseIsolationAndExpiry(t *testing.T) {
	data := bytes.Repeat([]byte("document fixture"), 5000)
	c, b, count := fileTestConnection(t, data)
	event := fileEvent(101, 11, "../复习题.docx", int64(len(data)))
	session := c.engine.SessionManager.GetOrCreate(c.historyKey(event))
	seq := session.ReserveSequenceTurn("sender:11", true, false)
	seq.Done()
	c.rememberInboundFiles(event)
	var ref string
	for pass := 0; pass < 2; pass++ {
		turn, infos := listTurn(t, c, event, session)
		if len(infos) != 1 || infos[0].Name != "复习题.docx" {
			t.Fatalf("listing: %+v", infos)
		}
		ref = infos[0].Ref
		result := turn.importFile(context.Background(), ref)
		if result.Status != "ready" || result.Ref != ref || result.Size != int64(len(data)) || !strings.HasPrefix(result.Path, "/sandbox/received/") {
			t.Fatalf("import: %+v", result)
		}
		b.mu.Lock()
		got := b.files[c.historyKey(event)+":"+result.Path]
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
	turn, _ := listTurn(t, c, event, session)
	if result := turn.importFile(context.Background(), ref); result.Status != "ready" || count.Load() != 2 {
		t.Fatal("sandbox expiry did not reimport attachment")
	}
	other := event
	other.UserID = 12
	otherSession := c.engine.SessionManager.GetOrCreate(c.historyKey(other))
	if otherTurn, infos := listTurn(t, c, other, otherSession); infos != nil || otherTurn.importFile(context.Background(), ref).Status == "ready" {
		t.Fatal("file crossed private sessions")
	}
	foreign := &resolvedReplyContext{MessageID: "101", SessionID: c.historyKey(event), Segments: ParseMessageSegments(event.Message)}
	if foreignTurn, infos := listTurn(t, c, other, otherSession, foreign); infos != nil || foreignTurn.importFile(context.Background(), ref).Status == "ready" {
		t.Fatal("foreign quote imported")
	}
	if err := session.ResetSession(nil); err != nil {
		t.Fatal(err)
	}
	if result := turn.importFile(context.Background(), ref); result.Status == "ready" || result.Path != "" {
		t.Fatal("file imported after session reset within the same turn")
	}
	if _, infos := listTurn(t, c, event, session); infos != nil {
		t.Fatal("file survived session reset")
	}
	fresh := newWSConnection(nil)
	fresh.engine = c.engine
	defer fresh.cancel()
	if freshTurn, infos := listTurn(t, fresh, event, session); infos != nil || freshTurn.importFile(context.Background(), ref).Status == "ready" {
		t.Fatal("file crossed connections")
	}
	if count.Load() != 2 {
		t.Fatalf("rejected imports downloaded: %d", count.Load())
	}
}

func TestInboundFileImportRejectsExpiredAndUnknownRefs(t *testing.T) {
	c, _, count := fileTestConnection(t, []byte("fixture"))
	event := fileEvent(101, 11, "old.docx", 7)
	session := c.engine.SessionManager.GetOrCreate(c.historyKey(event))
	c.rememberInboundFiles(event)
	turn, infos := listTurn(t, c, event, session)
	if result := turn.importFile(context.Background(), "file_000000000000"); result.Status != "unavailable" || result.Error == "" {
		t.Fatalf("unknown ref: %+v", result)
	}
	c.inboundFileMu.Lock()
	c.inboundFiles[0].seen = time.Now().Add(-2 * inboundFileLifetime)
	c.inboundFileMu.Unlock()
	if result := turn.importFile(context.Background(), infos[0].Ref); result.Status != "unavailable" || result.Path != "" {
		t.Fatalf("expired attachment imported: %+v", result)
	}
	if _, listed := listTurn(t, c, event, session); listed != nil {
		t.Fatal("expired attachment listed")
	}
	if count.Load() != 0 {
		t.Fatal("rejected import downloaded a file")
	}
}

func TestInboundFilesQuoteAndSameName(t *testing.T) {
	c, _, count := fileTestConnection(t, []byte("fixture"))
	event := fileEvent(101, 11, "same.docx", 7)
	session := c.engine.SessionManager.GetOrCreate(c.historyKey(event))
	c.rememberInboundFiles(event)
	second := fileEvent(102, 11, "same.docx", 7)
	c.rememberInboundFiles(second)
	turn, infos := listTurn(t, c, second, session)
	if len(infos) != 2 || infos[0].Ref == infos[1].Ref {
		t.Fatalf("same filenames share a reference: %+v", infos)
	}
	first, next := turn.importFile(context.Background(), infos[0].Ref), turn.importFile(context.Background(), infos[1].Ref)
	if first.Status != "ready" || next.Status != "ready" || first.Path == next.Path {
		t.Fatal("same filenames overwrote each other")
	}
	quoted := fileEvent(103, 11, "quoted.docx", 7)
	quote := &resolvedReplyContext{MessageID: "103", SessionID: historyKey(event), Segments: ParseMessageSegments(quoted.Message)}
	turn, infos = listTurn(t, c, event, session, quote)
	if infos[0].Name != "quoted.docx" || count.Load() != 2 {
		t.Fatalf("quote listing: %+v count=%d", infos, count.Load())
	}
	if result := turn.importFile(context.Background(), infos[0].Ref); result.Status != "ready" || count.Load() != 3 {
		t.Fatalf("quote import: %+v count=%d", result, count.Load())
	}
}

func TestInboundFileImportBudgetAcrossCalls(t *testing.T) {
	c, _, count := fileTestConnection(t, []byte("fixture"))
	event := fileEvent(101, 11, "a.docx", 7)
	session := c.engine.SessionManager.GetOrCreate(c.historyKey(event))
	c.rememberInboundFiles(event)
	c.rememberInboundFiles(fileEvent(102, 11, "b.docx", 7))
	turn, infos := listTurn(t, c, event, session)
	if len(infos) != 2 {
		t.Fatalf("listing: %+v", infos)
	}
	turn.remaining = 10
	a, b := infos[0], infos[1]
	if result := turn.importFile(context.Background(), a.Ref); result.Status != "ready" {
		t.Fatalf("first import: %+v", result)
	}
	if result := turn.importFile(context.Background(), a.Ref); result.Status != "ready" || turn.remaining != 3 {
		t.Fatalf("repeated import charged twice: %+v remaining=%d", result, turn.remaining)
	}
	if result := turn.importFile(context.Background(), b.Ref); result.Status != "unavailable" || !strings.Contains(result.Error, "额度") {
		t.Fatalf("budget not shared across calls: %+v", result)
	}
	if count.Load() != 1 {
		t.Fatalf("downloads=%d", count.Load())
	}
	nextTurn, _ := listTurn(t, c, event, session)
	if result := nextTurn.importFile(context.Background(), b.Ref); result.Status != "ready" {
		t.Fatalf("budget leaked into the next turn: %+v", result)
	}
}

func TestInboundFileLimitsCancellationAndDisabled(t *testing.T) {
	c, b, count := fileTestConnection(t, []byte("fixture"))
	event := fileEvent(101, 11, "file.docx", sandbox.MaxFileBytes+1)
	c.rememberInboundFiles(event)
	session := c.engine.SessionManager.GetOrCreate(c.historyKey(event))
	turn, infos := listTurn(t, c, event, session)
	if result := turn.importFile(context.Background(), infos[0].Ref); result.Status != "unavailable" || count.Load() != 0 || b.calls != 0 {
		t.Fatal("oversize file was downloaded or imported")
	}
	event = fileEvent(102, 11, "file.docx", 7)
	c.rememberInboundFiles(event)
	turn, infos = listTurn(t, c, event, session)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := turn.importFile(ctx, infos[0].Ref); result.Status != "unavailable" || count.Load() != 0 {
		t.Fatal("canceled import performed a download")
	}
	b.mu.Lock()
	b.health = errors.New("synthetic sandbox outage")
	b.mu.Unlock()
	if result := turn.importFile(context.Background(), infos[0].Ref); result.Status != "unavailable" || !strings.Contains(result.Error, "暂不可用") || count.Load() != 0 {
		t.Fatalf("unhealthy sandbox: %+v", result)
	}
	b.mu.Lock()
	b.health = sandbox.ErrSandboxDisabled
	b.mu.Unlock()
	if result := turn.importFile(context.Background(), infos[0].Ref); result.Status != "unavailable" || !strings.Contains(result.Error, "禁用") || count.Load() != 0 {
		t.Fatalf("disabled sandbox: %+v", result)
	}
	c.engine.SandboxBackend = nil
	if result := turn.importFile(context.Background(), infos[0].Ref); result.Status != "unavailable" || !strings.Contains(result.Error, "禁用") || count.Load() != 0 {
		t.Fatal("disabled sandbox downloaded a file")
	}
	c.engine.SandboxBackend = (*sandbox.DynamicBackend)(nil)
	if result := turn.importFile(context.Background(), infos[0].Ref); !strings.Contains(result.Error, "禁用") {
		t.Fatalf("typed nil sandbox: %+v", result)
	}
	for i := 0; i < maxInboundFileRecords+5; i++ {
		c.rememberInboundFiles(fileEvent(int32(1000+i), 11, "file.docx", 7))
	}
	if len(c.inboundFiles) > maxInboundFileRecords {
		t.Fatal("attachment cache is unbounded")
	}
	if _, infos := listTurn(t, c, event, session); len(infos) != maxInboundTurnFiles {
		t.Fatalf("listing not bounded: %d", len(infos))
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
	turn, infos := listTurn(t, c, event, session)
	if result := turn.importFile(context.Background(), infos[0].Ref); result.Status != "ready" {
		t.Fatalf("sender import: %+v", result)
	}
	question := event
	question.UserID = 12
	question.MessageID = 102
	otherTurn, listed := listTurn(t, c, question, session)
	if listed != nil || otherTurn.importFile(context.Background(), infos[0].Ref).Status == "ready" {
		t.Fatal("unquoted file selected for another sender")
	}
	quote := &resolvedReplyContext{MessageID: "101", SessionID: historyKey(event), Segments: ParseMessageSegments(event.Message)}
	quotedTurn, listed := listTurn(t, c, question, session, quote)
	if len(listed) != 1 || listed[0].Ref != infos[0].Ref {
		t.Fatalf("quoted listing: %+v", listed)
	}
	if result := quotedTurn.importFile(context.Background(), infos[0].Ref); result.Status != "ready" || count.Load() != 1 {
		t.Fatalf("same-group quote did not reuse file: %+v", result)
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
			turn, infos := listTurn(t, c, question, session)
			if len(infos) != 1 || infos[0].MessageID != "" || count.Load() != 0 {
				t.Fatalf("notice attachment: %+v", infos)
			}
			if result := turn.importFile(context.Background(), infos[0].Ref); result.Status != "ready" || result.MessageID != "" {
				t.Fatalf("notice import: %+v", result)
			}
			other := question
			other.UserID = 12
			if otherTurn, listed := listTurn(t, c, other, session); listed != nil || otherTurn.importFile(context.Background(), infos[0].Ref).Status == "ready" {
				t.Fatal("unquoted notice leaked to a different sender")
			}
		})
	}
}

// fileToolProvider imports the first listed attachment through the registered
// tool. next may choose the follow-up response for the import result; otherwise
// the model answers once the tool returns.
func fileToolProvider(t *testing.T, onList func([]inboundFileInfo), next func(llm.ReceivedFileImport) *core.ChatResponse) *mockLLMProvider {
	reply := func(text string) *core.ChatResponse {
		return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: text}}
	}
	return &mockLLMProvider{customChat: func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		last := req.Messages[len(req.Messages)-1]
		text, _ := last.Content.(string)
		if last.Role != core.RoleTool {
			var infos []inboundFileInfo
			if strings.Contains(text, "<received_files>") {
				infos = parseReceivedFiles(t, text)
			}
			if onList != nil {
				onList(infos)
			}
			if len(infos) == 0 {
				return reply("没有可用附件。"), nil
			}
			arguments, _ := json.Marshal(map[string]string{"ref": infos[0].Ref})
			return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "import", Type: "function", Function: core.ToolCallFunction{Name: tools.ImportReceivedFileToolName, Arguments: string(arguments)}}}}}, nil
		}
		if i := strings.Index(text, `{"ref"`); i >= 0 && next != nil {
			var result llm.ReceivedFileImport
			if err := json.NewDecoder(strings.NewReader(text[i:])).Decode(&result); err == nil {
				if response := next(result); response != nil {
					return response, nil
				}
			}
		}
		return reply("文件已读取。"), nil
	}}
}

func newFileToolEngine(provider core.LLMProvider, backend sandbox.Backend) *llm.Engine {
	engine := newTestEngine(provider)
	engine.MaxIterations = 5
	engine.SandboxBackend = backend
	engine.ToolRegistry[tools.ImportReceivedFileToolName] = tools.ImportReceivedFileTool()
	return engine
}

func TestInboundMissingURLResolvesGateway(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			backend := newInboundBackend()
			var count atomic.Int32
			provider := fileToolProvider(t, nil, func(result llm.ReceivedFileImport) *core.ChatResponse {
				if result.Status != "ready" {
					t.Errorf("file URL resolution failed: %+v", result)
				}
				return nil
			})
			engine := newFileToolEngine(provider, backend)
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

func TestInboundFilePassiveGroupThenMention(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprintf("security-failure=%v", blocked), func(t *testing.T) {
			t.Setenv("PROACTIVE_REPLY_PROBABILITY", "0")
			data := fixtureDOCX(t)
			var downloads, calls, inspected atomic.Int32
			provider := fileToolProvider(t, func(infos []inboundFileInfo) {
				calls.Add(1)
				if blocked {
					if infos != nil {
						t.Error("security-rejected file was registered")
					}
				} else if len(infos) != 1 || infos[0].MessageID != "101" {
					t.Errorf("passive group file unavailable: %+v", infos)
				}
			}, func(result llm.ReceivedFileImport) *core.ChatResponse {
				if result.Status != "ready" || result.MessageID != "101" {
					t.Errorf("passive group file import failed: %+v", result)
				}
				return nil
			})
			engine := newFileToolEngine(provider, newInboundBackend())
			engine.Security = security.NewController(t.TempDir())
			engine.Security.SetMode(security.ControlModeAggressive)
			engine.Security.SetClassifier(&mockClassifier{fn: func(_ context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
				if input.Stage == security.StageIngress && strings.Contains(input.Content, "passive.docx") {
					inspected.Add(1)
					if blocked {
						return security.ClassificationResult{}, errors.New("synthetic classifier unavailable")
					}
				}
				return security.ClassificationResult{Category: security.RiskCategoryNone, RiskLevel: security.RiskLevelNone}, nil
			}})
			server, address := startInboundServer(t, engine, inboundFixtureClient(data, &downloads))
			defer server.Close()
			ws, _, err := websocket.DefaultDialer.Dial(address, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			event := fileEvent(101, 11, "passive.docx", int64(len(data)))
			event.MessageType, event.GroupID = "group", 22
			if err := ws.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			event.MessageID = 102
			event.Message = json.RawMessage(`[{"type":"at","data":{"qq":"99"}},{"type":"text","data":{"text":"读取刚才的文件"}}]`)
			if err := ws.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
			for {
				var action model.OneBotAction
				if err := ws.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				if action.Action == "get_group_info" {
					_ = ws.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"group_id": 22, "group_name": "fixture group"}})
					continue
				}
				if action.Action != "send_group_msg" {
					t.Fatalf("unexpected action: %s", action.Action)
				}
				_ = ws.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 103}})
				break
			}
			wantDownloads := int32(1)
			if blocked {
				wantDownloads = 0
			}
			if inspected.Load() < 1 || calls.Load() != 1 || downloads.Load() != wantDownloads {
				t.Fatalf("inspected=%d dialogue=%d downloads=%d", inspected.Load(), calls.Load(), downloads.Load())
			}
		})
	}
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
	var lists, reads atomic.Int32
	provider := fileToolProvider(t, func(infos []inboundFileInfo) {
		lists.Add(1)
		if len(infos) != 1 || infos[0].Name != "复习题1.docx" {
			t.Errorf("file missing after cancellation: %+v", infos)
		}
	}, func(result llm.ReceivedFileImport) *core.ChatResponse {
		if result.Status != "ready" {
			return nil
		}
		arguments, _ := json.Marshal(map[string]string{"command": "read-docx " + result.Path})
		return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "read", Type: "function", Function: core.ToolCallFunction{Name: "execute_command", Arguments: string(arguments)}}}}}
	})
	listAndImport := provider.customChat
	provider.customChat = func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		last := req.Messages[len(req.Messages)-1]
		if text, _ := last.Content.(string); last.Role == core.RoleTool && strings.Contains(text, "第一题") {
			reads.Add(1)
			return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "第一题答案为 5。"}}, nil
		}
		return listAndImport(ctx, req)
	}
	engine := newFileToolEngine(provider, backend)
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
	if lists.Load() != 2 || reads.Load() != 1 || count.Load() != 2 {
		t.Fatalf("listings=%d reads=%d downloads=%d", lists.Load(), reads.Load(), count.Load())
	}
}

func TestInboundQuotedFileViaGetMsg(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			data := fixtureDOCX(t)
			backend := newInboundBackend()
			var count atomic.Int32
			provider := fileToolProvider(t, func(infos []inboundFileInfo) {
				if len(infos) != 1 || infos[0].MessageID != "101" {
					t.Errorf("quoted file not listed: %+v", infos)
				}
			}, func(result llm.ReceivedFileImport) *core.ChatResponse {
				if result.Status != "ready" || result.MessageID != "101" || result.Name != "quoted.docx" {
					t.Errorf("quoted file not imported: %+v", result)
				}
				return nil
			})
			engine := newFileToolEngine(provider, backend)
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

func TestInboundFileNotDownloadedWithoutToolCall(t *testing.T) {
	var downloads, listed atomic.Int32
	provider := &mockLLMProvider{customChat: func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
		prompt, _ := req.Messages[len(req.Messages)-1].Content.(string)
		if infos := parseReceivedFiles(t, prompt); len(infos) == 1 {
			listed.Add(1)
		}
		return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "收到文件。"}}, nil
	}}
	backend := newInboundBackend()
	engine := newFileToolEngine(provider, backend)
	server, address := startInboundServer(t, engine, inboundFixtureClient([]byte("fixture"), &downloads))
	defer server.Close()
	ws, _, err := websocket.DefaultDialer.Dial(address, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := ws.WriteJSON(fileEvent(101, 11, "unread.docx", 7)); err != nil {
		t.Fatal(err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var action model.OneBotAction
	if err := ws.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	if action.Action != "send_private_msg" {
		t.Fatalf("unexpected action before reply: %s", action.Action)
	}
	_ = ws.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 102}})
	backend.mu.Lock()
	calls := backend.calls
	backend.mu.Unlock()
	if listed.Load() != 1 || downloads.Load() != 0 || calls != 0 {
		t.Fatalf("listed=%d downloads=%d sandbox calls=%d", listed.Load(), downloads.Load(), calls)
	}
}

// A quoted attachment that is not cached yet must be registered under the
// quoted message's sender, never under the user who quoted it.
func TestInboundQuotedUncachedFileKeepsRealSender(t *testing.T) {
	groupEvent := func(mid int32, sender int64) model.OneBotEvent {
		event := fileEvent(mid, sender, "a.docx", 7)
		event.MessageType, event.GroupID = "group", 22
		return event
	}
	t.Run("known sender", func(t *testing.T) {
		c, _, count := fileTestConnection(t, []byte("fixture"))
		original := groupEvent(101, 11)
		question := groupEvent(102, 12)
		question.Message = json.RawMessage(`[{"type":"text","data":{"text":"read the quoted file"}}]`)
		session := c.engine.SessionManager.GetOrCreate(c.historyKey(question))
		quote := &resolvedReplyContext{MessageID: "101", SessionID: historyKey(question), SenderID: 11, Segments: ParseMessageSegments(original.Message)}
		turn, infos := listTurn(t, c, question, session, quote)
		if len(infos) != 1 || infos[0].MessageID != "101" {
			t.Fatalf("quoted listing: %+v", infos)
		}
		ref := infos[0].Ref
		if result := turn.importFile(context.Background(), ref); result.Status != "ready" {
			t.Fatalf("quoted import: %+v", result)
		}
		later := question
		later.MessageID = 103
		laterTurn, listed := listTurn(t, c, later, session)
		if listed != nil || laterTurn.importFile(context.Background(), ref).Status == "ready" {
			t.Fatal("quoting user gained sender access to another user's file")
		}
		owner := groupEvent(104, 11)
		owner.Message = json.RawMessage(`[{"type":"text","data":{"text":"my file"}}]`)
		ownerTurn, listed := listTurn(t, c, owner, session)
		if len(listed) != 1 || listed[0].Ref != ref || ownerTurn.importFile(context.Background(), ref).Status != "ready" {
			t.Fatalf("real sender lost access: %+v", listed)
		}
		if count.Load() != 1 {
			t.Fatalf("downloads=%d", count.Load())
		}
	})
	t.Run("unknown sender", func(t *testing.T) {
		c, _, _ := fileTestConnection(t, []byte("fixture"))
		original := groupEvent(101, 11)
		question := groupEvent(102, 12)
		question.Message = json.RawMessage(`[{"type":"text","data":{"text":"read the quoted file"}}]`)
		session := c.engine.SessionManager.GetOrCreate(c.historyKey(question))
		quote := &resolvedReplyContext{MessageID: "101", SessionID: historyKey(question), Segments: ParseMessageSegments(original.Message)}
		turn, infos := listTurn(t, c, question, session, quote)
		if len(infos) != 1 || turn.importFile(context.Background(), infos[0].Ref).Status != "ready" {
			t.Fatalf("quote-only import: %+v", infos)
		}
		ref := infos[0].Ref
		for _, user := range []int64{11, 12} {
			later := groupEvent(103, user)
			later.Message = json.RawMessage(`[{"type":"text","data":{"text":"again"}}]`)
			laterTurn, listed := listTurn(t, c, later, session)
			if listed != nil || laterTurn.importFile(context.Background(), ref).Status == "ready" {
				t.Fatalf("unknown-sender record reachable without a quote by %d", user)
			}
		}
		// Seeing the original message directly completes the unknown owner,
		// without granting the quoting user anything.
		c.rememberInboundFiles(original)
		ownerTurn, listed := listTurn(t, c, groupEvent(104, 11), session)
		if len(listed) < 1 || ownerTurn.importFile(context.Background(), ref).Status != "ready" {
			t.Fatalf("real sender not recorded after direct observation: %+v", listed)
		}
		later := groupEvent(105, 12)
		later.Message = json.RawMessage(`[{"type":"text","data":{"text":"again"}}]`)
		if laterTurn, listed := listTurn(t, c, later, session); listed != nil || laterTurn.importFile(context.Background(), ref).Status == "ready" {
			t.Fatal("quoting user gained access after owner completion")
		}
		c.inboundFileMu.Lock()
		c.inboundFiles[0].senderID = 11
		c.inboundFileMu.Unlock()
		quote.SenderID = 12
		c.inboundFileTurn(question, session, []*resolvedReplyContext{quote})
		c.inboundFileMu.Lock()
		owner := c.inboundFiles[0].senderID
		c.inboundFileMu.Unlock()
		if owner != 11 {
			t.Fatalf("known owner replaced by a later quote: %d", owner)
		}
	})
}

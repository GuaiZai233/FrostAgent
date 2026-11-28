package llm

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
)

// BillingRunState tracks request-level billing context and accumulates consumption across tool loop iterations.
type BillingRunState struct {
	mu               sync.Mutex
	nextCall         uint64
	pending          int
	idle             chan struct{}
	usage            core.Usage
	Platform         string
	ExternalID       string
	DisplayName      string
	TaskID           string
	WelcomeGranted   bool
	TotalBilledMinor int64
	LastBalanceMinor int64
	BillingActive    bool
	IterationsBilled int
}

// RunContext contains request-local state that tools must not read from the
// shared Engine, otherwise concurrent sessions can cross-send or mix owners.
type RunContext struct {
	ToolHistoryRunID string
	Turn             *SessionTurn // Sender-local execution authority; SessionID remains the shared sandbox key.
	SkillState       *SessionContext
	Context          context.Context // Optional request-local cancellation context (falls back to Engine.Context)
	SessionID        string
	Owner            string
	OwnerType        memory.OwnerType
	ActorUserID      string              // Trusted sender ID supplied by the platform adapter.
	MemorySource     memory.GroupMessage // Current message text and vision output supplied by the adapter.
	ActorPlatform    string              // Trusted platform namespace; never populated from model content.
	AdapterID        string              // Trusted transport adapter used for later delivery.
	InstanceID       string              // Runtime instance used for audit/evaluation attribution.
	SendHook         func(toolResultJSON string) error
	// RefreshPrompt rebuilds the current adapter-owned input after time_sleep.
	// The callback retains original user text, quotes, metadata and image parts.
	RefreshPrompt       func(context.Context) (PromptRefresh, error)
	PlatformActionHook  func(context.Context, string) (core.PlatformActionResult, error)
	LoadObservedSticker func(context.Context, string, int) ([]byte, error)
	// LoadObservedImage resolves a trusted image attachment from a message that
	// the adapter observed in the current session. The adapter enforces message
	// ownership; the model only supplies an opaque message ID and image index.
	LoadObservedImage func(context.Context, string, int) ([]byte, error)
	// ImportReceivedFile imports one attachment listed in the current prompt
	// into the session sandbox. The adapter re-checks authorization and owns the
	// per-turn import budget; the model only supplies the opaque file reference.
	ImportReceivedFile   func(context.Context, string) ReceivedFileImport
	ImageInspectionQuota *atomic.Int32 // Per-request image inspection budget shared by tool calls.
	ObservationScope     string        // Connection-scoped generation/identity to prevent cross-connection stale message handle lookups
	Billing              *BillingRunState
	RouteScope           modelrouter.Scope
	RouteSnapshot        *modelrouter.Snapshot
	SecurityNotice       string // Temporary security notice injected into main LLM prompt for current turn; does not pollute session history.
	Mock                 bool   // Mock / ephemeral session: do not write to persistent memory or group summary
	Proactive            bool   // Trusted unaddressed turn: only narrow current-session text replies may send; unsolicited errors remain suppressed.
	Awareness            bool   // Trusted timer trigger; text and native mentions are restricted to the current conversation.
	ProactiveStickerOnly bool   // Adapter-classified current message; backend supplies stay_silent only when Proactive is also true.
	// ReplyToBotMentionOnly is set by a trusted adapter when a group message only
	// @mentions the bot while replying to one of the bot's own messages. The backend
	// then runs a fixed time_sleep before the model turn; the model does not choose it.
	ReplyToBotMentionOnly bool
	Epoch                 uint64

	// TempChat marks a "/tempchat" run. Nothing it reads is written back: no
	// session history, group buffer, memory, extraction, tool history or prompt
	// trace. nil for every other run.
	TempChat *TempChat

	// CanReplyToMessage validates an opaque message ID against trusted,
	// adapter-owned current-session observations, never model-supplied text.
	CanReplyToMessage func(messageID string) bool

	// FinalReplyText returns the adapter's rendered text for a text-only final
	// message. False retains the original output, including media and rich content.
	FinalReplyText func(string) (string, bool)
}

// TempChat selects what a temporary conversation may read. Whatever the
// options, a temporary conversation never writes to backend history.
type TempChat struct {
	// NoPersonality drops SYSTEM_PROMPT, the dialogue persona (including its
	// example dialogues) and the memory catalog index.
	NoPersonality bool
	// NoMemory drops recalled memory, the session history, group summaries and
	// recent group messages, and the tools that read memory or past chats.
	NoMemory bool
}

// TempChatQuotedGuidance is appended to the prompt when the user quotes an
// Assistant message that was produced in a temporary conversation.
func TempChatQuotedGuidance(m logs.TempChatMark) string {
	var disabled []string
	if m.NoPersonality {
		disabled = append(disabled, "禁用了人格提示词")
	}
	if m.NoMemory {
		disabled = append(disabled, "禁用了记忆")
	}
	text := "用户引用的消息，为 Assistant 处于“临时对话”下所产生的，内容原则上不影响正常消息队列，仅供参考。"
	if len(disabled) > 0 {
		text += "此次临时对话(" + strings.Join(disabled, "、") + ")"
	}
	return text
}

// ReceivedFileImport reports the outcome of one on-demand attachment import.
// It never carries file bytes or platform download credentials.
type ReceivedFileImport struct {
	Ref       string `json:"ref"`
	MessageID string `json:"message_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Path      string `json:"path,omitempty"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

type PromptRefresh struct {
	Content        any
	SecurityNotice string
}

type runContextKey struct{}

func withRunContext(ctx context.Context, runContext RunContext) context.Context {
	runContext.OwnerType = memory.NormalizeOwnerType(runContext.OwnerType)
	if runContext.ImageInspectionQuota == nil {
		runContext.ImageInspectionQuota = new(atomic.Int32)
	}
	return context.WithValue(ctx, runContextKey{}, runContext)
}

// WithRunContext attaches RunContext to a context.
func WithRunContext(ctx context.Context, runContext RunContext) context.Context {
	return withRunContext(ctx, runContext)
}

// RunContextFromContext returns the request-local tool state, if the current
// agent entry point supplied one.
func RunContextFromContext(ctx context.Context) (RunContext, bool) {
	runContext, ok := ctx.Value(runContextKey{}).(RunContext)
	return runContext, ok
}

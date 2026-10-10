package backup

import (
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/mcp"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/sticker"
	"time"
)

// FormatVersion identifies the structure of exported instance backups.
const FormatVersion = 1

const SecretNotice = "密钥、凭据及其来源不包含在导出文件中；还原后请重新配置。"

type Manifest struct {
	FormatVersion int       `json:"format_version"`
	Kind          string    `json:"kind"`
	ExportedAt    time.Time `json:"exported_at"`
	SecretNotice  string    `json:"secret_notice"`
}

type Settings struct {
	FormatVersion int                       `json:"format_version"`
	Values        map[string]string         `json:"settings"`
	ModelRouter   modelrouter.Configuration `json:"model_router"`
	MCP           mcp.Config                `json:"mcp"`
	Dialogues     []llm.DialogueExample     `json:"dialogues"`
	SecretNotice  string                    `json:"secret_notice"`
}

type GroupMemory struct {
	Platform string                      `json:"platform"`
	GroupID  string                      `json:"group_id"`
	Profile  *memory.GroupProfile        `json:"profile"`
	Entries  []memory.MemoryEntry        `json:"entries"`
	Archives []memory.MemoryMergeArchive `json:"merge_archives"`
}

type Memories struct {
	FormatVersion    int                         `json:"format_version"`
	PrivateEntries   []memory.MemoryEntry        `json:"private_entries"`
	PrivateArchives  []memory.MemoryMergeArchive `json:"private_merge_archives"`
	Groups           []GroupMemory               `json:"groups"`
	DuplicateWarning string                      `json:"duplicate_warning"`
}

type Stickers struct {
	FormatVersion int             `json:"format_version"`
	Entries       []sticker.Entry `json:"entries"`
}

type Summaries struct {
	FormatVersion int                   `json:"format_version"`
	Records       []groupsummary.Record `json:"records"`
}

package backup

import (
	"github.com/GuaiZai233/FrostAgent/internal/groupsummary"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/mcp"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"time"
)

// FormatVersion identifies the structure of exported instance backups.
const FormatVersion = 1

const SecretNotice = "独立保存的 API Key、凭据引用不包含在导出文件中；还原后请重新配置。端点完整 URL 原样保留，可能包含查询参数或内嵌凭据，请妥善保管备份。"

type Manifest struct {
	FormatVersion int       `json:"format_version"`
	Kind          string    `json:"kind"`
	InstanceName  string    `json:"instance_name,omitempty"`
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

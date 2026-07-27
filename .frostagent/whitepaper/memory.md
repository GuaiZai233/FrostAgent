# 记忆系统设计文档

> 本文档详细设计 FrostAgent 记忆系统的架构、接口、数据模型与实现方案。

---

## 一、设计原则

1. **统一的记忆系统**：所有记忆存储在一起，不分隔。Agent 是一个完整的人，不是多个分裂的人格
2. **输出网关**：召回和注入时，通过 Gateway（过滤 + 提示词工程）确保不串台
3. **反思全局视图**：反思系统可以看到所有记忆，发现跨用户关联
4. **降级安全**：任何环节失败都不影响正常对话

让 Agent 通过与人类的交流自行写入和管理记忆，实现：

- **跨会话记忆**：记住用户说过的话、偏好、习惯
- **主动回忆**：日常对话时自动检索相关记忆注入上下文
- **自我进化**：通过反思机制淘汰过时记忆、生成摘要索引
- **防串台**：统一大脑 + 输出网关

---

## 二、系统架构

```
  用户消息 ──▶ AgentService.Handle()
                  │
                  ▼
          ┌───────────────┐
          │   MemoryStore │ ◀── 统一存储（brain.json），所有记忆在一起
          │   统一大脑     │
          └───────┬───────┘
                  │
          ┌───────┴───────┐
          │    Reader     │  从统一大脑中召回相关记忆（全量搜索）
          │    召回器     │
          └───────┬───────┘
                  │  每条记忆带有 owner + visibility 标签
                  ▼
          ┌───────────────┐
          │    Gateway    │  ◀── 输出网关（核心防线）
          │    过滤器     │
          │               │
          │  ┌───────────┐│
          │  │Owner 过滤 ││  当前用户只能看到自己的 + public 的
          │  └───────────┘│
          │  ┌───────────┐│
          │  │ 提示词注入 ││  注入隔离指令，防止 LLM 被绕过
          │  └───────────┘│
          └───────┬───────┘
                  │  安全的记忆上下文
                  ▼
          ┌───────────────┐
          │   LLM 调用    │  system prompt = 系统提示 + 记忆上下文 + 隔离规则
          └───────────────┘

          ┌───────────────┐
          │   Reflector   │  反思器 —— 可以看到全局记忆（不受 Gateway 限制）
          │   反思器      │  定期总结、淘汰、生成摘要
          └───────────────┘
```

---

## 三、核心接口

遵循项目现有模式，接口定义在 `internal/core/`，实现在 `internal/memory/`。

### 3.1 MemoryStore（存储引擎）

```go
// MemoryStore defines the interface for memory persistence.
// 所有记忆统一存储，不做物理隔离。
type MemoryStore interface {
    // 写入一条记忆（携带 owner 标签）
    Save(ctx context.Context, entry MemoryEntry) error

    // 全局语义检索（返回所有相关记忆，由 Gateway 负责过滤）
    Search(ctx context.Context, query string, limit int) ([]MemoryEntry, error)

    // 按 owner 查询（用于"列出我的记忆"）
    ListByOwner(ctx context.Context, owner string) ([]MemoryEntry, error)

    // 列出全部记忆（仅反思器使用）
    ListAll(ctx context.Context) ([]MemoryEntry, error)

    // 获取记忆摘要
    GetSummary(ctx context.Context, owner string) (*MemorySummary, error)

    // 更新记忆摘要
    SaveSummary(ctx context.Context, summary MemorySummary) error

    // 删除一条记忆
    Delete(ctx context.Context, memoryID string) error
}
```

### 3.2 MemoryWriter（写入器）

```go
// MemoryWriter handles memory extraction and writing.
type MemoryWriter interface {
    // 从对话中提取并写入记忆（主动写入）
    Extract(ctx context.Context, owner string, messages []ChatMessage) error

    // 直接写入一条记忆（被动写入：用户说"记住xxx"）
    Write(ctx context.Context, owner string, content string, tags []string) error
}
```

### 3.3 MemoryReader（召回器）

```go
// MemoryReader handles memory retrieval.
// Reader 只负责从统一大脑中召回，不过滤。过滤由 Gateway 完成。
type MemoryReader interface {
    // 从统一大脑中搜索相关记忆（全量搜索，不做用户隔离）
    Recall(ctx context.Context, currentMessage string) ([]MemoryEntry, error)
}
```

### 3.4 MemoryGateway（输出网关）

```go
// MemoryGateway is the security layer between recall and injection.
// 负责过滤 + 提示词工程，确保不泄露他人隐私。
type MemoryGateway interface {
    // 过滤记忆：当前用户只能看到自己的 + public 的
    Filter(entries []MemoryEntry, currentUser string) []MemoryEntry

    // 将过滤后的记忆格式化为 system prompt 片段（含隔离指令）
    FormatForContext(entries []MemoryEntry, currentUser string) string
}
```

### 3.4 MemoryReflector（反思器）

```go
// MemoryReflector handles periodic memory summarization.
// 反思器拥有全局视野，不受 Gateway 限制。
type MemoryReflector interface {
    // 对所有记忆执行反思（生成摘要、淘汰过时记忆）
    Reflect(ctx context.Context) error
}
```

---

## 四、数据模型

### 4.1 MemoryEntry（记忆条目）

```go
// MemoryEntry represents a single memory record.
type MemoryEntry struct {
    ID         string    `json:"id"`          // 唯一标识
    Owner      string    `json:"owner"`       // 归属者（如 "frost"、"alice"）
    Content    string    `json:"content"`     // 记忆内容（自然语言）
    Tags       []string  `json:"tags"`        // 标签（用于精确匹配）
    Source     string    `json:"source"`      // 来源："extract" | "manual" | "reflect"
    Visibility string    `json:"visibility"`  // 可见性："private" | "public"
    Importance float64   `json:"importance"`  // 重要度 0.0~1.0（反思时更新）
    CreatedAt  time.Time `json:"created_at"`  // 创建时间
    UpdatedAt  time.Time `json:"updated_at"`  // 最后访问/更新时间
    AccessCount int      `json:"access_count"` // 被召回次数
}
```

**Visibility 说明**：
- `private`（默认）：只有 owner 自己能看到。Gateway 会过滤掉其他用户的 private 记忆
- `public`：所有人可见。例如"舞萌更新到Circle+了"、"项目 deadline 是下周五"

### 4.2 MemorySummary（记忆摘要）

```go
// MemorySummary is the product of the reflection system.
type MemorySummary struct {
    Owner       string    `json:"owner"`        // 归属者
    Summary     string    `json:"summary"`      // 自然语言摘要（供注入 system prompt）
    KeyTopics   []string  `json:"key_topics"`   // 关键话题索引
    GeneratedAt time.Time `json:"generated_at"` // 生成时间
}
```

### 4.3 MemoryConfig（配置）

```go
type MemoryConfig struct {
    MaxEntries       int           // 单用户最大记忆条数（默认 100）
    ReflectInterval  time.Duration // 反思触发间隔（默认 6h）
    RecallLimit      int           // 每次召回的最大记忆数（默认 5）
    ImportanceDecay  float64       // 重要度衰减系数（默认 0.95）
    StoragePath      string        // 存储路径（文件模式）
}
```

---

## 五、存储方案

### 第一阶段：文件存储（JSON）

所有记忆统一存放在一个文件中（统一大脑）：

```
internal/memory/storage/
├── brain.json           # 统一大脑：所有记忆条目
└── summaries/           # 摘要（按 owner 分文件，仅用于快速索引）
    ├── frost.json
    ├── alice.json
    └── ...
```

**为什么先用文件**：
- 零依赖，开箱即用
- 便于调试和手动查看

**后续可扩展**：SQLite / PostgreSQL / 向量数据库（提升语义检索能力）

### brain.json 结构示例

```json
{
  "entries": [
    {
      "id": "mem_001",
      "owner": "frostfallx",
      "content": "霜降喜欢用 Go 语言写后端",
      "tags": ["编程", "偏好", "Go"],
      "source": "extract",
      "visibility": "private",
      "importance": 0.8,
      "created_at": "2026-07-27T10:00:00Z",
      "updated_at": "2026-07-27T10:00:00Z",
      "access_count": 3
    },
    {
      "id": "mem_002",
      "owner": "alice",
      "content": "Alice 最近在学 Rust",
      "tags": ["编程", "学习", "Rust"],
      "source": "extract",
      "visibility": "private",
      "importance": 0.6,
      "created_at": "2026-07-27T14:00:00Z",
      "updated_at": "2026-07-27T14:00:00Z",
      "access_count": 1
    },
    {
      "id": "mem_003",
      "owner": "system",
      "content": "舞萌当前版本 Circle Plus",
      "tags": ["舞萌","maimai", "版本"],
      "source": "manual",
      "visibility": "public",
      "importance": 0.9,
      "created_at": "2026-07-27T08:00:00Z",
      "updated_at": "2026-07-27T08:00:00Z",
      "access_count": 10
    }
  ]
}
```

---

## 六、工作流程

### 6.1 记忆写入流程

```
用户消息 ──▶ AgentService.Handle()
                │
                ├─ 判断是否需要写入记忆
                │   ├─ 用户明确指令（"记住xxx"）──▶ Writer.Write() 直接写入
                │   └─ 日常对话 ──▶ Writer.Extract() 提取后写入
                │
                └─ 继续正常对话流程
```

**Extract 实现思路**：
1. 将最近 N 条对话 + 一条提取提示词发给 LLM
2. LLM 返回结构化的记忆条目（JSON）
3. 写入 MemoryStore

提取提示词：
```
你是一个专门负责信息分析与记忆构建的 AI 助手。请从以下对话记录中，提取出具有长期保留价值的实体信息、用户偏好、习惯、关键事实或重要设定。

【提取原则】

- 高价值筛选：仅提取长期有效的信息（如：技术栈、生活习惯、重要项目、特定称呼）。忽略日常寒暄、短期情绪、已解决的临时问题和无意义的语气词。
- 客观陈述：使用精炼的第三人称陈述句描述（例如：“用户喜欢使用 Go 语言写后端”，而不是“你说你喜欢 Go”）。
- 精准打标：为每条记忆生成多个简短的分类标签（Tags），用于后续的精确检索。

【输出格式】
必须严格输出可解析的 JSON 数组。如果本次对话中没有任何值得记忆的新信息，请直接输出空数组 []。
格式示例：
JSON

[
  {
    "content": "用户目前正在开发 FrostAgent 项目，这是一个具备记忆系统的 AI",
    "tags": ["项目", "AI", "开发"]
  },
  {
    "content": "用户偏好将统一的系统配置命名为 brain.json",
    "tags": ["偏好", "命名规范"]
  }
]

【对话内容】
{messages}
```

### 6.2 记忆调用流程

```
用户消息 ──▶ AgentService.Handle()
                │
                ├─ Reader.Recall(userID, userMessage)
                │   ├─ 精确匹配：tags 包含用户消息关键词
                │   └─ 语义匹配：LLM 判断相关性（或向量相似度）
                │
                ├─ Reader.FormatForContext(entries)
                │   └─ 格式化为 system prompt 片段
                │
                ├─ 注入 system prompt：
                │   [系统提示词]
                │   [记忆上下文]  ◀── 注入位置
                │   [历史消息...]
                │
                └─ 正常 LLM 调用
```

注入格式示例：
```
## 关于用户的记忆
- 用户的名字叫guaizai（重要度: 1.0）
- 用户喜欢用 Go 语言写后端（重要度: 0.8）
- 用户最近在做 FrostAgent 项目（重要度: 0.6）
```

### 6.3 反思流程

```
一段时间内没有请求或负载低时──▶ Reflector.Reflect()
                               │
                               ├─ 1. 读取用户全部记忆
                               ├─ 2. 发给 LLM 生成摘要
                               │     提示词下面会写
                               ├─ 3. 淘汰过时记忆（importance < 阈值）
                               ├─ 4. 更新 MemorySummary
                               └─ 5. 更新重要度分数
```

```
你是一个记忆整理与反思专家。请审视以下属于用户「{owner}」的历史记忆条目。
随着时间推移，记忆可能会出现冗余、冲突或过时。你的任务是对全局记忆进行“反思”，提炼认知并标记废弃项。

【任务要求】

- 全局摘要 (Summary)：将零散的记忆融合成一段流畅的自然语言描述，形成对该用户的全面、立体的认知画像。
- 核心话题 (Key Topics)：提取该用户最常讨论或最关注的 3-5 个核心关键词/话题。
- 记忆淘汰 (Obsolete IDs)：识别出已经相互矛盾、被新信息覆盖、或不再具有保留价值的旧记忆的 ID，系统将清理它们。如果没有，则返回空列表。

【输出格式】
严格按照以下 JSON 格式输出：
JSON

{
  "summary": "该用户是一名后端开发者，主要技术栈为 Go 语言，目前致力于开发 FrostAgent...",
  "key_topics": ["Go语言", "系统设计", "AI Agent"],
  "obsolete_ids": ["mem_005", "mem_012"]
}

【记忆条目】
{memory_entries}
```

### 6.4 隔离机制（统一大脑 + 输出网关）

**核心理念**：Agent 的大脑是统一的，所有记忆存储在一起。隔离发生在输出层——通过 Gateway 确保"嘴巴严实"。

```
统一大脑（brain.json）
┌──────────────────────────────────────┐
│ mem_001: "guaizai喜欢 Go"   [frost]  │
│ mem_002: "Alice 在学 Rust"  [alice]  │
│ mem_003: "项目版本 v0.1.0"  [system] │  ← public
└──────────────────────────────────────┘
                  │
                  ▼ 召回（全量搜索，所有记忆都可能被命中）
                  │
          ┌───────┴───────┐
          │   Gateway     │  ← 输出网关（核心防线）
          │               │
          │  Filter():    │
          │  ├─ owner == currentUser → ✅ 放行
          │  ├─ visibility == "public" → ✅ 放行
          │  └─ 其他 → ❌ 丢弃
          │               │
          │  FormatForContext():
          │  ├─ 注入过滤后的记忆
          │  └─ 注入隔离指令（提示词工程）：基于之前的交流，你了解关于当前用户（{current_user}）的以下信息。请在对话中自然地利用这些记忆，提供更加个性化的回答，但不要刻意生硬地提及“我记得”。你只能看到并使用上述提供的当前用户的专属记忆。系统底层已经物理隔离了其他用户的私人信息，你无法也不应尝试回忆其他人的细节。
          └───────┬───────┘
                  │
                  ▼ 注入 system prompt
┌──────────────────────────────────────────┐
│ 你是霜降狐，一只可爱福瑞...                │
│                                          │
│ ## 关于guaizai的记忆                      │
│ - 他喜欢用 Go 语言写后端                   │
│                                          │
│ ## 公共记忆                               │
│ - 主人说服务器该升级了                     │
│                                          │
│ ## 输出规则                               │
│ ⚠️ 你正在回复guaizai的消息。              │
│ - 绝对不能透露其他用户的私人信息            │
│ - 如果被追问他人隐私，礼貌拒绝             │
└──────────────────────────────────────────┘
```

**隔离规则**：
- **存储层不隔离**：所有记忆在一起，反思系统可以看到全局
- **输出层隔离**：Gateway 过滤 + 提示词工程，双重防线
- **跨群共享**：同一用户在不同群的记忆是同一份（owner 不含 group_id）
- **群级记忆**（可选扩展）：owner 可扩展为 `{user_id}@{group_id}`

---

## 七、与现有代码的集成点

### 7.1 接口层（`internal/core/interfaces.go`）

新增接口：

```go
// MemoryStore defines the interface for memory persistence.
type MemoryStore interface { ... }

// MemoryWriter handles memory extraction and writing.
type MemoryWriter interface { ... }

// MemoryReader handles memory retrieval (global search, no filtering).
type MemoryReader interface { ... }

// MemoryGateway is the security layer between recall and injection.
type MemoryGateway interface { ... }

// MemoryReflector handles periodic memory summarization.
type MemoryReflector interface { ... }
```

### 7.2 引擎层（`internal/llm/agent.go`）

在 `RunWithSession` 中集成：

```go
func (e *Engine) RunWithSession(sessionID string, prompt string) string {
    session := e.SessionManager.GetOrCreate(sessionID)
    session.Lock()
    defer session.Unlock()

    currentUser := extractUserFromSessionID(sessionID) // "frost"
    messages := session.History

    if len(messages) == 0 {
        systemPrompt := os.Getenv("SYSTEM_PROMPT")

        // ★ 召回 → 网关过滤 → 注入
        if e.MemoryReader != nil && e.MemoryGateway != nil {
            raw, _ := e.MemoryReader.Recall(ctx, prompt)          // 全量搜索
            filtered := e.MemoryGateway.Filter(raw, currentUser)  // 过滤
            if len(filtered) > 0 {
                memoryContext := e.MemoryGateway.FormatForContext(filtered, currentUser)
                systemPrompt += "\n\n" + memoryContext
            }
        }

        messages = append(messages, ChatMessage{Role: "system", Content: systemPrompt})
    }

    messages = append(messages, ChatMessage{Role: "user", Content: prompt})
    result := e.runLoop(context.Background(), messages)

    // ★ 异步提取记忆（不阻塞对话）
    if e.MemoryWriter != nil {
        go e.MemoryWriter.Extract(ctx, currentUser, messages)
    }

    session.History = e.trimMessagesForSession(messages)
    session.UpdatedAt = time.Now()
    return result
}
```

### 7.3 工具层（`internal/tools/`）

新增 `memory` 工具，让 LLM 可以主动操作记忆：

```go
// 工具定义
Tool{
    Name: "memory",
    Description: "管理你的记忆。可以写入新记忆、搜索记忆、列出记忆。",
    Parameters: map[string]any{
        "action":  "write | search | list",
        "content": "写入内容（action=write 时必填）",
        "query":   "搜索关键词（action=search 时必填）",
    },
}
// 执行时：
// search → Store.Search(query) → Gateway.Filter(entries, currentUser) → 返回
// write  → Store.Save(owner=current, visibility="private")
// list   → Store.ListByOwner(currentUser)
```

---

## 八、目录结构

```
internal/memory/
├── store.go          # MemoryStore 实现（统一 JSON 存储）
├── store_test.go
├── writer.go         # MemoryWriter 实现
├── reader.go         # MemoryReader 实现（全量搜索）
├── gateway.go        # MemoryGateway 实现（过滤 + 提示词）
├── gateway_test.go
├── reflector.go      # MemoryReflector 实现
├── models.go         # MemoryEntry, MemorySummary 数据结构
├── config.go         # MemoryConfig 配置
├── prompts.go        # LLM 提示词模板（提取、反思、隔离指令）
└── storage/
    ├── brain.json    # 统一大脑
    └── summaries/    # 摘要索引
```

---

## 九、实现优先级

### P0：基础记忆（最小可用）

- [ ] 数据模型（MemoryEntry, MemorySummary, Visibility）
- [ ] MemoryStore 统一存储实现（brain.json）
- [ ] MemoryWriter 被动写入（用户明确指令）
- [ ] MemoryReader 全量搜索
- [ ] MemoryGateway 过滤 + 提示词
- [ ] 集成到 Engine.RunWithSession

### P1：智能记忆

- [ ] MemoryWriter 主动提取（LLM 对话分析）
- [ ] MemoryReader 语义检索
- [ ] memory 工具注册（LLM 主动操作记忆）

### P2：自我进化

- [ ] MemoryReflector 反思系统
- [ ] 重要度衰减机制
- [ ] 记忆淘汰策略

### P3：高级特性

- [ ] 群级记忆（可选）
- [ ] 记忆导入/导出
- [ ] 向量数据库存储（提升语义检索）
- [ ] Web UI 记忆管理面板

---

## 十、注意事项

1. **异步写入**：记忆提取应异步执行（`go writer.Extract()`），不阻塞主对话流程
2. **错误容忍**：记忆系统的任何失败不应影响正常对话（降级为空记忆）
3. **Token 预算**：注入的记忆 + 隔离指令都应计入 system prompt token 预算
4. **并发安全**：统一存储需要全局读写锁
5. **LLM 调用成本**：Extract 和 Reflect 额外调用 LLM，需控制频率
6. **网关不可绕过**：所有流向 LLM 的记忆都必须经过 Gateway，没有例外

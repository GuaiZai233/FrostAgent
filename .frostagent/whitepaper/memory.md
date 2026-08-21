# 记忆系统设计文档

> 本文档详细设计 FrostAgent 记忆系统的架构、接口、数据模型与实现方案。

---

## 一、设计原则

1. **统一的记忆系统**：所有记忆存储在一起，不分隔。Agent 是一个完整的人，不是多个分裂的人格
2. **输出网关**：召回和注入时，通过 Gateway（过滤 + 提示词工程）确保不串台
3. **反思按用户隔离**：全量反思任务按 owner 分批处理，避免跨用户主题污染
4. **降级安全**：任何环节失败都不影响正常对话

让 Agent 通过与人类的交流自行写入和管理记忆，实现：

- **跨会话记忆**：记住用户说过的话、偏好、习惯
- **主动回忆**：日常对话时自动检索相关记忆注入上下文
- **自我进化**：通过反思机制淘汰过时记忆、生成主题索引
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
          │   Reflector   │  反思器 —— 按 owner 分批整理记忆
          │   反思器      │  更新重要度、淘汰过时项、生成主题目录
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

    // 删除一条记忆
    Delete(ctx context.Context, memoryID string) error

    // 递增记忆被召回次数并更新访问时间
    IncrementAccessCount(memoryIDs ...string) error
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

    // 记录被召回的记忆（递增被召回次数及更新访问时间）
    RecordRecall(entries []MemoryEntry) error
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
// MemoryReflector rebuilds a compact topic catalog per owner.
type MemoryReflector interface {
    // 后台整理全部 owner 的记忆
    Reflect(ctx context.Context) error

    // 只整理指定 owner 的记忆
    ReflectOwner(ctx context.Context, owner string) error
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

**AccessCount 说明**：
- `access_count`：记录记忆被成功召回的累计次数。
- 在主动召回注入系统上下文（`Reader.Recall`）以及工具搜索命中（`memory` 工具 `action=search`）时触发自增，并同步刷新 `updated_at` 为当前时间。
- 在反思合并（`Reflect`）时，新生成的合并记忆继承各来源条目的最大 `access_count`。

### 4.2 UserMemoryCatalog（记忆主题目录）

```go
type MemoryTopic struct {
    Name       string   `json:"name"`
    Aliases    []string `json:"aliases,omitempty"`
    Importance float64  `json:"importance,omitempty"`
}

type UserMemoryCatalog struct {
    Owner       string        `json:"owner"`
    Topics      []MemoryTopic `json:"topics"`
    MemoryCount int           `json:"memory_count"`
    GeneratedAt time.Time     `json:"generated_at"`
}
```

主题目录只是帮助模型决定何时调用 memory 搜索工具的索引，不代表具体事实。
它独立保存并可随时覆写，不进入 `brain.json`，也不随记忆导入导出。

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

原始记忆和可重建的主题目录分开保存：

```
data/
├── brain.json            # 统一大脑：原始记忆条目
├── memory_catalog.json   # 可覆写的按 owner 主题目录
└── vectors.json          # 可选向量索引
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
1. 将最近 1~2 轮对话 + 当前系统时间 + 一条提取提示词发给 LLM
2. LLM 返回结构化的记忆条目（JSON）
3. 写入 MemoryStore
4. 时间敏感的信息（计划、预约、即将发生的事件等）会记录明确日期（如 2026-08-02），避免使用"明天""下周"这类相对时间，便于反思时判断时效

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
当前系统时间：2026-08-05 17:50 星期三

## 关于用户的记忆
- 用户的名字叫guaizai（重要度: 1.0）
- 用户喜欢用 Go 语言写后端（重要度: 0.8）
- 用户最近在做 FrostAgent 项目（重要度: 0.6）
```

系统提示词开头会注入当前系统时间字段（带中文星期），让模型能理解对话中的相对时间（今天/明天/本周）。

### 6.3 反思流程

```
Web 或聊天手动触发 ──▶ ReflectionManager.Start(owner)
                         │  立即返回，不阻塞对话
                         ▼
                    后台 goroutine
                         ├─ 按 owner 读取记忆
                         ├─ 注入当前系统时间（判断相对日期：今天/明天/下周）
                         ├─ LLM 提取主题、别名和重要度
                         ├─ 校验 LLM 返回的记忆 ID
                         ├─ 淘汰明确过时或已过时效性的记忆
                         ├─ 更新重要度
                         └─ 覆写 memory_catalog.json
```

反思提示词会携带当前系统时间，并要求对照时间清理已过时效的内容：

```
你是一个记忆整理助手。请只分析下面属于同一用户的记忆，完成以下任务：

当前系统时间：{current_time}

1. 提取 3～20 个便于未来检索的主题。主题应简短、具体，合并同义表达，并把别名放入 aliases
2. 标记已经明确过时、被新事实取代或不再有保留价值的记忆 ID
3. 为每条记忆评估重要度（0.0～1.0）
4. 将描述同一主体、内容兼容且合并后更利于检索的记忆整合成一条

时效性规则（对照当前系统时间判断）：
- 仅在某时间点之前/当天有效、且该时间已过的记忆（如"用户明天要去极兽聚"、"用户本周在成都"），属于已过时效性，应放入 outdated_ids 删除
- 长期偏好仍成立、只是夹带已过去临时信息的记忆（如"用户喜欢打舞萌，下周要去参赛"），不要整条删除：用 importance_updates 适当降低重要度，可把已失效的临时部分拆出后用 outdated_ids 删除
- 不要因为内容提及过去的日期就一律删除；只有当日期已过且该事实本身已失效时才删除

【输出格式】返回 JSON：
{
  "topics": [ {"name": "FrostAgent", "aliases": ["霜降狐项目"], "importance": 1.0} ],
  "outdated_ids": ["mem_005", "mem_012"],
  "importance_updates": {"mem_001": 0.9}
}
```

配合提取侧的改动，时间敏感的记忆会记录明确日期（见 6.1），反思才能准确判断时效。

### 6.4 隔离机制（统一大脑 + 输出网关）

**核心理念**：原始记忆统一存储；召回结果通过 Gateway 过滤；反思按 owner 分批处理并生成独立主题目录。

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
- **存储统一、处理隔离**：原始记忆在一起，反思按 owner 分批处理
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
├── reflector.go      # 按 owner 的反思执行器
├── reflection_manager.go # 非阻塞后台反思任务
├── catalog.go        # 独立主题目录存储与提示词格式化
├── models.go         # MemoryEntry 数据结构
├── config.go         # MemoryConfig 配置
├── prompts.go        # LLM 提示词模板（提取、反思、隔离指令）
└── data/
    ├── brain.json          # 统一大脑
    └── memory_catalog.json # 可重建主题目录
```

---

## 九、实现优先级

### P0：基础记忆（最小可用）

- [ ] 数据模型（MemoryEntry, UserMemoryCatalog, Visibility）
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

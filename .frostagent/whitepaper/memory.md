# 记忆系统设计文档

> 本文档详细设计 FrostAgent 记忆系统的架构、接口、数据模型、作用域隔离与实现方案。

---

## 一、设计原则与边界

1. **作用域解耦与严格隔离（Scope Decoupling & Isolation）**：
   - **私聊记忆**：以实例为基础，严格按用户 QQ 隔离（存储于 `brain.json`）。
   - **群聊记忆**：以群为安全边界，独立持久化存储（存储于 `groups/<safe_group_key>/`）。群与群之间物理隔离，私聊与群聊之间绝对互不泄漏、互不串台。
2. **废弃 Public / Private 可见性机制**：
   - 彻底废除旧有的 `public` 与 `private` 可见性概念及相关 API / UI 控制。
   - 私聊记忆仅私聊生效；群聊记忆在对应群内作为共享上下文或检索索引生效。
3. **群内归属与引用标注（In-Group Ownership & Attribution）**：
   - 群内记忆的 `owner` 仅作为相关性检索索引，**绝非**访问权限控制标识。
   - 仅当可信发言人明确陈述自身第一人称事实（`is_self: true`）时，`owner` 记录为该发言人 QQ 号。
   - 第三人陈述（如“小红说小明喜欢吃披萨”）必须记录为客观传闻（如“小红称小明喜欢吃披萨”），`owner` 记录为 `"group"`，并通过标签（Tags）索引涉及主体。
   - 群规、多人关系、公共事实与无法确证的主体一律使用显式 `"group"` 归属（`GroupOwnerExplicit = "group"`）。
4. **元数据信任边界（Metadata Trust Boundary）**：
   - `owner`、`sender_id`、`instance_id`、`group_id` 必须严格来自 IM 适配器提供的受信元数据。模型生成的内容绝不能伪造或篡改身份标识。
5. **群档案与成员跟踪（Group Profiles & Member Tracking）**：
   - 自动记录群名称与发言群员档案，持久化于 `profile.json`。
   - 称呼解析优先级严格遵循：`preferred_name`（优先称呼） > `nickname`（群昵称/昵称） > 中性称呼（“群友”）。群名片（`card`）严格仅用于身份识别与消除歧义，**绝不**作为称呼。
6. **双输入路径与滚动压缩提炼（Dual Input Paths & Rolling Compact）**：
   - **路径一（实时对话提炼）**：群内所有消息均触发成员观测；正常回复及真实调用 `stay_silent` 工具的成功交互触发单轮记忆提取。安全拦截、路由禁用、取消等非主动沉默状态不触发记忆提取。
   - **路径二（滚动压缩提炼）**：群聊消息缓冲区触发滚动压缩（Running Compact）时，提取快照中的原始文本结构，比对现有群记忆去重后进行尽力而为的长期事实提炼（`SourceDistill`）。
7. **Windows 文件系统安全存储与单射映射**：
   - 群组持久化目录统一使用单射哈希方案 `SafeGroupKey` 进行标准化，彻底避免冒号、斜杠导致的跨群碰撞或目录穿越，规避 Windows 保留设备名称冲突。

---

## 二、系统架构

```
                     ┌───────────────────────────────────┐
                     │          IM 消息事件入口          │
                     │  (OneBot / AstrBot / Mock)        │
                     └─────────────────┬─────────────────┘
                                       │
                 ┌─────────────────────┴─────────────────────┐
                 │                                           │
         [私聊事件 private]                          [群聊事件 group]
                 │                                           │
                 ▼                                           ▼
      ┌──────────────────────┐                   ┌───────────────────────┐
      │     私聊记忆存储     │                   │     群聊记忆管理器    │
      │      Store          │                   │     GroupManager      │
      │  (data/brain.json)   │                   └───────────┬───────────┘
      └──────────┬───────────┘                               │
                 │                                           ▼
                 │                               ┌───────────────────────┐
                 │                               │ 规范化安全群目录      │
                 │                               │ groups/<safe_key>/    │
                 │                               ├─ profile.json (群档案)│
                 │                               ├─ memory.json  (群记忆)│
                 │                               └─ catalog.json (主题表)│
                 │                               └───────────────────────┘
                 │                                           │
                 └─────────────────────┬─────────────────────┘
                                       │
                                       ▼
                       ┌───────────────────────────────┐
                       │     记忆输出网关 (Gateway)    │
                       │   - 私聊按用户与主题召回      │
                       │   - 群聊严格限定本群范围召回  │
                       │   - 注入防串台系统提示词      │
                       └───────────────┬───────────────┘
                                       │
                                       ▼
                       ┌───────────────────────────────┐
                       │      智能体推理 (LLM Engine)  │
                       └───────────────────────────────┘
```

---

## 三、群聊记忆与档案持久化结构

每个群聊在其专属安全目录下维护独立的三份文件，确保并发安全与原子写入：

```
instance_<id>/
├── brain.json                  # 私聊统一大脑记忆
├── memory_catalog.json         # 私聊主题目录
└── groups/
    └── <safe_group_key>/       # Windows 平台安全目录名 (如 g_123456_a1b2c3d4e5f60718)
        ├── profile.json        # 群聊档案与成员列表
        ├── memory.json         # 群聊独立记忆条目
        └── catalog.json        # 群聊主题目录索引
```

### 3.1 安全群目录命名规则 (`SafeGroupKey`)

采用**前缀规范化 + 确定性 SHA-256 单射哈希 + 路径边界约束**方案：
- 剥离各适配器平台协议前缀（如 `group:`、`qq:group:`、`onebot:group:`、`astrbot:group:`）。
- 计算规范化群号的 SHA-256 哈希取前 16 位十六进制字符。
- 提取前 24 位安全字母数字构成前缀，组合为 `g_<safe_prefix>_<hashHex>`。
- 保证任意不同的原始群号（如 `abc:def`、`abc/def`、`abc_def`）绝不会映射到同一物理目录；绝对杜绝 `..` 路径穿越；规避 Windows 设备保留名（`CON`、`PRN`、`NUL`、`AUX` 等）。
- 并在 `NewGroupStore` 中通过 `filepath.Rel` 执行硬边界检查，杜绝越界访问。

### 3.2 群档案模型 (`GroupProfile` & `MemberProfile`)

`profile.json` 结构示例：

```json
{
  "group_id": "123456789",
  "group_name": "霜降狐的技术交流群",
  "updated_at": "2026-10-08T14:30:00Z",
  "members": {
    "10001": {
      "user_id": "10001",
      "nickname": "霜降",
      "card": "群主·霜降",
      "role": "owner",
      "preferred_name": "霜霜",
      "aliases": ["狐狸", "呆毛狐"],
      "source": "onebot",
      "created_at": "2026-10-08T10:00:00Z",
      "updated_at": "2026-10-08T14:30:00Z",
      "last_spoke_at": "2026-10-08T14:30:00Z"
    }
  }
}
```

#### 成员角色同步机制
- `role` 标准化取值：`owner`（群主）、`admin`（管理员）、`member`（群员）、`unknown`（未知）。
- 适配器在监听消息和群管理通知（如 OneBot `notice_type == "group_admin"`）时自动同步角色变更。
- 群员退群或离开时不删除本地档案，以保证历史陈述归属与上下文连续性。

#### 称呼解析优先级 (`ResolveCallingName`)
```go
func (m *MemberProfile) ResolveCallingName() string {
    if m.PreferredName != "" {
        return m.PreferredName
    }
    if m.Nickname != "" {
        return m.Nickname
    }
    return "群友"
}
```
**安全准则**：群名片（`card`）严格用于身份核实与歧义消除，**严禁**作为对该成员的呼称。

---

## 四、双输入路径提炼架构

群聊记忆来源分为两条正交且互补的输入路径：

### 路径一：实时对话提炼（Turn Extraction）
1. **全员全消息观测**：群内收到的所有消息（包括未唤醒消息、纯表情、图片）均触发成员发言观测，更新 `last_spoke_at`、`nickname` 与群名称。
2. **单轮对话提炼触发条件**：
   - 智能体给出正常文本回复（1 轮对话）。
   - 智能体主动且成功调用 `stay_silent` 工具（1 轮对话，助手内容记为 `[stay_silent]`）。
3. **终端静默状态分类（Terminal Silence Classification）**：
   - 智能体执行结果 `AgentRunResult` 明确区分终端状态：`StaySilentCalled` 与 `SilenceReason`（如 `security_block`、`route_disabled`、`canceled`、`epoch_changed`、`provider_fallback`）。
   - 仅当真正成功调用 `stay_silent` 时触发提炼，因安全拦截或路由故障导致的静默绝对不触发记忆提取。

### 路径二：滚动压缩提炼（Rolling Compact Distillation）
1. **快照抓取**：当群聊消息缓冲达到阈值触发滚动压缩时，生成 `GroupCompactSnapshot`。
2. **防注入消息溯源绑定**：
   - 消息以严格 JSON 数组格式（包含 `msg_index`、`sender_name`、`role`、`content`）呈现给模型，杜绝聊天头伪造与提示词注入。
   - 提炼模型输出仅需返回 `source_msg_index` 与 `is_self`。
   - 后端根据 `source_msg_index` 严格由快照绑定对应的可信 `SenderID`，模型无法伪造或窜改归属。若非第一人称自述或索引无效，归属强制为 `"group"`。
3. **长期记忆区分与网关可达性**：
   - 滚动压缩生成的是群长期记忆事实，来源标识为 `SourceDistill`（区别于旧版废弃的会话段落总结 `SourceCompact`）。
   - `Gateway.FilterGroup` 保留 `SourceDistill` 记忆供召回与工具检索，排除废弃的 `SourceCompact` 临时段落。
4. **降级与容错**：提炼过程失败时以实例 Logger 输出 `WARN` 日志（格式含 `[Instance: <name>]`），绝不阻塞或中断滚动压缩上下文更新流程。

---

## 五、群聊反思（Group-Scoped Reflection）与历史数据热迁移

### 5.1 群聊独立反思机制 (`ReflectGroup` & `StartGroup`)
- 群反思完全作用于群自身的 `GroupStore`，直接读取 `groups/<safe_key>/memory.json` 并调用模型进行合并提炼与过期淘汰。
- 反思产生的记忆合并归档直接保存在群的 `memory.json` 中，主题目录写入专属的 `groups/<safe_key>/catalog.json`。
- 绝不触碰或修改私聊的 `brain.json` 和 `memory_catalog.json`，确保物理隔离与安全边界。

### 5.2 旧版本群聊记忆热迁移 (`MigrateLegacyGroupMemories`)
- 实例启动时自动检查 `brain.json` 中是否残留 `owner: group:<id>`、`OwnerGroup` 或 `ScopeGroup` 的旧条目与合并归档。
- 发现旧群数据时，先建立带时间戳的完整备份文件 `brain.json.bak.<timestamp>`。
- 按群号自动分发导入到对应的 `GroupStore` 中，并实现条目 ID 去重以保证迁移的完全幂等性。
- 原子重写 `brain.json`，清理群聊条目，仅保留纯私聊用户记忆。

---

## 六、记忆条目数据模型与 Protobuf 演进规范

```go
type MemoryEntry struct {
    ID          string    `json:"id"`
    Owner       string    `json:"owner"`        // 私聊为用户QQ号；群聊中个人自述为发言人QQ，客观事实/第三人传闻为 "group"
    Content     string    `json:"content"`      // 记忆内容客观表述
    Tags        []string  `json:"tags"`         // 检索标签（包含主体、领域等）
    Source      string    `json:"source"`       // "extract" | "manual" | "reflect" | "compact" | "distill"
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
    AccessCount int       `json:"access_count"`
    Scope       string    `json:"scope"`        // "private" 或 "group"
    GroupID     string    `json:"group_id"`     // Scope 为 "group" 时记录群号
}
```

### 6.1 Protobuf 向前兼容性规范
- 所有已发布的 Protobuf 字段 Tag 序号严格保持不可变更（例如 `UpdateMemoryRequest` 中的 `id=1, content=2, tags=3, visibility=4`）。
- 新增字段一律追加在未使用的高位 Tag 编号（如 `scope=5, group_id=6`），严格防止客户端二进制反序列化错位与崩溃。

---

## 七、Web 管理面板与 ConnectRPC API

Web 管理面板提供多维度管理界面：

1. **分域视图切换**：顶栏提供“私聊记忆”与“群聊记忆”独立切换面板。
2. **KPI 状态统计**：展示总记忆数、私聊记忆数、群聊记忆数、已观测群组数。
3. **群聊档案管理**：
   - 群列表快速切换，展示群号、群名、成员数与记忆数。
   - 支持直接编辑与修改群聊显示名称。
4. **群成员与称呼编辑**：
   - 表格清晰展示成员 QQ、群内昵称、群名片、群身份、当前生效称呼（调用 `ResolveCallingName` 算法展示）。
   - 支持在线编辑群成员的“优先称呼”（`preferred_name`）与“别名”（`aliases`）。
5. **分域导入、导出与反思**：
   - 私聊与群聊分别支持导出独立作用域的 JSON 文件。
   - 导入时严格导入到当前选中的作用域，杜绝跨群交叉污染。
   - 支持触发群聊专属记忆反思。

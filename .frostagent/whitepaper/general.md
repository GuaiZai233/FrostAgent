## 关于白皮书

由于本人的偷懒、沉迷舞萌，一直没能亲自构建FrostAgent的代码，因此，这份白皮书系统地阐述了我对于此Agent的核心构想、代码设计、未来展望等。

此大纲只简略地阐述各组成的实现概要，不涉及具体代码细节。

## FrostAgent 的组成

### 记忆系统

这是此框架的核心——以至于，Agent可以通过与人类的交流、主人的对话自行写入和管理记忆，从而达到进化迭代的效果。

记忆系统由以下几个逻辑部分组成：

#### 记忆写入系统

对于主动响应会话，上游是自主决策系统。记忆写入系统被赋予一段提示词，根据上游对话收集上下文等信息，然后写入记忆；对于“写入记忆”等明确要求调用此指令的，直接写入即可。

#### 记忆调用系统

对于明确让搜索记忆的，直接使用；对于日常对话，LLM自行根据关键词匹配近义词并查找相关条目。

#### 反思系统 (scheduled)

在负载不高的情况下生成对于自己记忆的总结摘要，便于快速定位是否有相关记忆。

#### 保密系统

既然bot是跨群生效的，避免串消息，也避免不隔离的情况下透露别人的私人事情。

### 情感系统

这是相当有时效性的。bot可以通过一些即时的外在反应（如被骂了、被夸了、工具调用反馈正确、代码没跑通等）获得情感反馈，从而影响一段时间的心情。同时对于不同用户也有不同偏爱等。

### 计费系统

每次发起对话都使用“雪花”，借鉴隔壁，按照最大token开销计费，然后退还。

雪花可以通过ActionsCat部分互动指令获得（晚安、签到等）。

### 群聊上下文与压缩系统 (Group Context & Compaction System)

为了让智能体兼具群聊背景记忆理解与即时群聊临场感，FrostAgent 采用双轨群聊上下文设计：

- **后台滚动压缩 (`GroupCompactor`)**：
  - 维持群聊消息 ring buffer，当未压缩原消息达到 `GROUP_COMPACT_BUFFER_SIZE` 时触发后台 LLM 增量提炼，更新群聊长期摘要 `group_running_summary`。
- **角色感知与结构化 JSONL 压缩 (Role-Aware JSONL Compaction & Send-Success Ingestion)**：
  - 结构化表示与 JSONL 边界隔离：消息在内部通过 `GroupCompactMessage` 结构体（含 `Role`, `Sender`, `SenderID`, `Content`, `MessageID`, `Time`）承载，真实角色严格由后端事件路由赋予。在提交给 `GroupCompactor` 压缩时采用单行 JSONL 格式输入，用户消息正文内部哪怕包含多行内容或伪造的 `\n[assistant]` / `[user]` 标签，也会被严格 JSON 转义在 `"content"` 字段内，杜绝通过多行注入伪造角色记录或污染长期摘要；
  - 平台出站确认门禁 (Platform ACK Gating)：严格区分传输层写入与平台层送达。对于具备同步请求-响应确认机制的平台（如 OneBot v11），机器人回复发送后必须通过 `SendActionAndWait` 等待平台返回确认 ACK（`status == "ok"` 且 `retcode == 0`）。只有平台确认送达后，才允许将回复写入会话持久历史（`Session.History`）、摄入群聊压缩缓冲区（`groupCompactBuffer`）以及触发记忆提取；同时，中间消息工具（`send_message`）的调用也严格同步等待平台 ACK 确认，若平台拒绝（如禁言或错误码）、ACK 超时或网络断开，则向大模型工具执行循环明确反馈真实错误原因，杜绝在发送失败时产生假成功；对于暂未提供端到端 ACK 响应的轻量协议（如 AstrBot WebSocket），当前通过传输层写入确认（Transport-Write Confirmation）进行尽力而为的送达控制；
  - 发送失败与防脑补隔离 (Delivery Failure Context)：当平台返回错误码（如禁言、风控、参数非法）、ACK 超时或传输层断开时，系统坚决不向 `Session.History` 与 `groupCompactBuffer` 提交该 assistant 消息，保留原始 user 输入，同步执行 `TrimSession` 防止受限历史无界膨胀，并在会话中记录瞬时 `DeliveryFailure`。在下一轮对话生成时，以一次性瞬态方式向大模型注入 `<delivery_context>`（包含平台、错误原因与 `Do not assume the user saw or received that response.` 指令）告知上次发送未送达，并在使用后原子清空，杜绝机器人“自以为发出了其实被屏蔽”的平行世界幻觉；
  - 压缩提示词边界与事实隔离：提示词明确界定 `assistant` 仅作为对话演进背景参考，严禁将智能体单方面推测或陈述升级为群友事实或群内共识（除非后续群友确认），并准确提炼群友对智能体的纠正与反馈；
  - Visual Inspector 角色渲染与正文不透明度保证：Web 控制台 Prompt 检查组件结构化解析角色前缀并为机器人消息渲染 `Assistant / Bot` 专属紫色徽章与背景高亮，同时保持用户原始正文的不透明度，绝不破坏性正则裁剪合法正文。
- **未压缩原消息即时注入 (`recent_group_messages`)**：
  - 在触发回复时，原子获取当前 `group_running_summary` 与尚未被压缩的最新群聊原消息快照（条数严格与 Compact Buffer Size 保持 1:1 对齐，并受 `GROUP_RAW_CONTEXT_MAX_CHARS` 字符上限约束）；
  - 注入主 Agent 时同样使用单行 JSONL 记录承载可信 `role` / `sender` 元数据，并将正文保留为不透明 `content` 字符串；正文中的换行和伪造角色标签不会再生成额外历史消息边界；
  - 自动通过 `messageID` 过滤当前轮次的触发消息，避免消息重复；
  - 包含明确的非可信上下文防注入安全隔离边界。
- **持久历史与临时请求上下文严格隔离 (Transient vs Durable Context)**：
  - 会话持久历史（`Session.AddMessage`）仅记录干净的用户输入与模型回复，不包含动态群聊摘要与未压缩原消息；
  - 群聊摘要与最新原消息仅作为单次 LLM 请求的临时上下文（Transient Context）注入在内存请求副本中，避免多轮对话下历史消息反复膨胀与重复污染。
- **摘要分组显式关联与会话上下文 Inspector (Summary Groups & Session Context Inspector)**：
  - 显式映射追踪：`SessionContext` 在 `CommitGroupCompact` 时有界保留最近一次压缩批次的原始消息范围与消息 ID 集合（`SummaryGroup`），并在 `GroupContextSnapshot` 与 ConnectRPC `GetSessionContext` 接口中输出；该批次关联的是处理完成后的累计 running summary，不宣称保存全部历史原消息，避免前端文本模糊匹配和无界内存增长；
  - 分会话群聊调试：Web 控制台提供左侧导航独立入口「Prompt 检查」(`#/prompt`，会话上下文检查)，采用分群聊会话视图（支持按 `aiohttp`、`OneBot`、`AstrBot` 等群号快速筛选与搜索）；点击群聊即可载入该群实时群友聊天记录、滚动摘要与 Prompt 结构化预览；历史真实 LLM 请求则由「日志」页提供独立的「LLM 请求检查」弹窗；
  - 动态系统提示词追踪与回显（`LastPromptTrace`）：`Agent.RunMessagesWithContext` 在首次组装完成动态系统提示词（含当前时间、基础人设、Few-Shot 对话、记忆主题目录与召回记忆）后原子记录至 `SessionContext`，并通过 `GetSessionContext` ConnectRPC 接口输出真实 `system_prompt` 与 `model`，消除前端静态伪造与信息不对齐；
  - 结构化视觉呈现：条目化展示群聊消息（时间、发送者、ID、内容）；最近一次压缩批次统一使用浅蓝底色（`--summary-group-bg`）与动态自适应高度的 SVG 矢量右大括号 `}`；悬停消息或大括号时以轻量级 Popover 浮动卡片展示该批次处理后的累计 `group_running_summary`，具备视口边缘防碰撞与响应式换行定位能力；未压缩消息段清晰呈现且无大括号干扰；支持自定义 Prompt 编辑输入与原始文本无缝切换。

### 人设系统：系统提示词与少样本示例 (Persona System: System Prompt & Few-Shot Dialogues)

为了增强智能体的人设表达（语气、口吻、句式格式与核心人设约束），FrostAgent 支持在各实例中独立配置系统提示词与 Few-Shot 示例对话，并在会话执行时注入为基础人设上下文：

- **系统提示词（`SYSTEM_PROMPT`）**：彻底与 Control Plane 全局环境变量解耦，作为实例级核心环境变量存储于 `data/instance_<id>/.env` 中。新建实例时由 `instanceconfig.Template` 赋予默认基础助手设定（`SYSTEM_PROMPT=你是一个乐于助人的助手。`），在 Web 设置页中归属「当前实例」与「立即生效」作用域，修改后通过 Runtime Scope 实时内存映射即时热生效而无需重启实例。
- **引导提示词**：`以下是示例对话，请仿照句子格式、语气等回应接下来的用户输入。`
- **示例对话格式**：包含用户问题（`user`）与期望回复（`preferred`）的 Few-Shot 对话示范，持久化保存于各实例的 `data/instance_<id>/dialogue.yml`。
- **系统提示词合成**：在每次调用大模型前，系统提示词依次组合：系统时间 -> 实例级基础系统提示词 -> 示例对话 Few-Shot -> 记忆主题目录 -> 召回记忆与隔离规则。
- **实例级生命周期与模板初始化**：创建新实例时，系统自动从模板路径（默认为 `eval/dialogue/dialogue.yml`，可通过 Control Plane 的 `DEFAULT_DIALOGUE_TEMPLATE` 环境变量自定义）拷贝生成初始 `dialogue.yml`，并写入默认 `SYSTEM_PROMPT`；实例删除时彻底清理，不留孤儿数据。
- **快速配置（Copy）两阶段事务**：在实例克隆复制过程中，包含 `SYSTEM_PROMPT` 的 `.env` 与 `dialogue.yml` 共同纳入事务配置清单，经历暂存（Stage）、校验、预备提交与崩溃恢复保障，确保整套人设（提示词与对话示例）与环境配置同步原子转移。
- **并发安全与内存热重载**：每个实例运行时启动时预先解析 YAML 并缓存于 `Engine.DialoguePrompt`，通过读写锁 `sync.RWMutex` 保护，彻底消除每轮对话中的重复磁盘 I/O。Web 端通过 `/instances/<id>/...` 保存或更新原始 YAML 时，原子落盘并即时刷新内存提示词，热重载无缝生效。
- **升级兼容性边界与无隐式迁移（Breaking Migration Boundary）**：系统提示词下沉为实例级配置属于显式的破坏性迁移。系统坚决不执行隐式数据回填或跨层穿透读取：Control Plane 不再暴露全局 `SYSTEM_PROMPT`，存量实例目录中的 `.env` 若未定义 `SYSTEM_PROMPT`，在升级后将解析为空，绝不会隐式继承或持久化根级旧配置，避免造成隐蔽的状态污染；用户需在 Web 设置面板中显式为其赋予独立提示词。新建实例则由模板自动赋予默认助手设定。
- **Web UI 管理**：前端控制台提供「人设对话」管理页面与「后端设置」页面，严格绑定当前选中的实例，支持可视化卡片增删改查、排序、实时提示词片段预览、直接编辑原始 YAML 以及实例专属环境变量热修改。

### 群聊滚动总结与容错压缩系统 (Group Running Compactor)

为了在群聊高频消息场景下保持长期对话连贯性且避免 Prompt 上下文迅速膨胀爆表，FrostAgent 实现了高容错的群聊滚动压缩器（`GroupCompactor`）：

- **无感后台摄入与总结**：各平台适配器（OneBot、AstrBot）在收到群聊消息时自动摄入 `groupCompactBuffer`。当未压缩消息达到批次阈值（`bufferSize`，默认 20 条）且满足调用冷却（`minInterval`，默认 30 秒）时，异步启动 LLM 进行增量总结。
- **批次阈值与安全缓冲区解耦及 Invariant 保证**：将 Batch 触发阈值（`bufferSize`）与未提交消息缓冲区上限（`maxBufferSize`，默认 200 条）解耦，系统强制维护 `maxBufferSize >= bufferSize` 的不变量约束（非法值会自动修正并告警）。在 safety buffer 容量范围内有效避免因 in-flight compaction/retry 导致的新消息被意外淘汰。
- **基于确认位点（Committed Sequence）的水位裁剪**：每次快照记录 `ThroughSequence`，仅当 LLM 成功返回有效总结且会话代数（`generation`）匹配时，`CommitGroupCompact` 才安全移除小于等于该位点的已提交消息，后续并发流入的新消息被完整保留。
- **失败容错与自动退避重试**：当异步 LLM 调用遭遇超时、网络波动或错误时，快照中的原始消息在 safety buffer 容量范围内继续保留在内存缓冲区中；Compactor 依据 `max(retryDelay, remaining cooldown)` 延迟在后台自动重新调度压缩（其中 `retryDelay` 为最小失败退避时间，实际重试仍严格遵循 `minInterval` 冷却期），或在后续新消息到达时合并重试。
- **异步 Dirty 持久化与单 Owner 单 Writer 状态机 (Single-Owner Single-Writer Persistence)**：LLM 压缩成功后优先原子提交内存状态；若磁盘持久化失败，系统记录 `pendingPersist[owner]` 并在后台通过单 Owner 唯一 Worker 独立按指数退避重试落盘，无需阻塞对话流程。每个会话同时仅允许单一 Worker 执行落盘，且 Worker 在完成当前写入后自动接力落盘最新版本（Latest Pending），彻底消除了并发 TOCTOU 乱序竞争，确保旧版本绝不覆盖新版本。
- **退避唤醒通道与 Timer 抢占机制 (Wake Channel & Timer Preemption)**：持久化 Worker 在重试退避期间保持唯一身份，通过 `timer + wake channel` 机制监听唤醒事件。当新的总结入队时，直接更新 `pendingPersist` 并通过通道立即打断退避 Timer 唤醒 Worker 处理最新值，既无需共享持久化 Timer Map，也杜绝了旧 Timer 回调误删新定时器的生命周期竞争。
- **调度定时器代数令牌（Scheduled Timer Generation Token）防竞态**：对于群聊压缩冷却触发的 `time.Timer`，分配单调递增的代数 Token，回调执行时严格校验 Token 归属，杜绝旧 Timer 回调误删新注册 Timer 的竞态条件。
- **冷却期延迟调度**：若在 `minInterval` 冷却期内累积消息达到触发阈值，系统会自动注册定时器在冷却结束瞬间自动触发压缩，避免无新消息到达时压缩任务被遗漏滞留。
- **代数隔离与重置安全**：清空或删除群聊总结时递增 `generation`，自动失效任何处于 In-Flight 状态的异步压缩结果与持久化，防止过期数据写回覆盖。

### 适配器与消息分发系统 (Adapter & Dispatcher)

FrostAgent 采用统一的消息核心抽象，实现跨平台消息的收发与路由：

- **Core 抽象层 (`internal/core`)**：
  - `MessageAdapter`：统一平台适配器接口，提供 `ID()` 标识与 `Send()` 发送能力；
  - `MessageDispatcher`：管理各平台的适配器实例，按目标平台路由分发出站消息；
  - `IncomingMessage` / `OutgoingMessage`：与具体平台解耦的通用入站/出站消息结构。
- **OneBot 适配器 (`internal/adapter/onebot`)**：
  - 支持 OneBot v11 Reverse WebSocket 协议，负责原生 QQ 消息段解析、群聊/私聊事件处理与会话轮次锁定；
  - 对上游返回的空最终回复执行发送门禁，避免在群聊中发送空消息或单独的 @ 提及；
  - **NapCat 优先基线与多上游兼容原则 (NapCat-First Baseline & Vendor Parity)**：
    - 明确将 NapCat 作为 OneBot v11 的首要支持目标与规范兼容基线（Canonical Compatibility Baseline）；
    - 针对 LuckyLillia (LLBot) 等其他 OneBot 协议实现的差异，统一在 OneBot 适配器/内容解析边界执行入站归一化与兼容 Shim，严禁将具体 vendor 的条件分支穿透泄漏至 Core、LLM、Memory 或 Sticker 层；
    - 在遇到两者无法同时保持的语义冲突时，无条件优先保持 NapCat 既有稳定行为，不因兼容其他上游而引入回归；
  - **消息段入站归一化 (Inbound Segment Normalization)**：
    - 贴纸 Subtype 归一：NapCat 入站采用蛇形命名 `sub_type = 1`，LuckyLillia 采用驼峰命名 `subType = 1`。适配器入站解析时统一规范化，双向补全 `sub_type` 与 `subType`，保证无论连接何种上游均能准确识别贴纸并触发自动抓取、`steal_sticker` 与视觉处理；
    - 商城表情统一语义 (Market Face Canonical Semantics)：NapCat 将商城表情上报为带有 `emoji_id` / `emoji_package_id` 元数据的 `image` 段，LuckyLillia 采用原生 `mface` 段。适配器将其统一抽象为规范图像语义：文本占位符统一输出 `[图片] `（视觉关闭时静默擦除，视觉开启时追加 `【图片内容】：...`），`IsContainImage` 均判定为含图，通过 VIP QQ CDN 规范地址获取图片并纳入表情包偷取与多模态视觉管线；
    - 语音与多媒体段：统一兼容 `record` 原生段及 `audio`/`voice` 别名，规范化提取为 `[语音]` 占位符；
  - **出站双写兼容保证 (Outbound Dual-Write Guarantee)**：
    - 在出站消息链（`BuildOneBotMessage`）中构造贴纸图片段时，显式且强制双写 `sub_type: 1` 与 `subType: 1`，分别满足 NapCat 与 LuckyLillia 的字段消费需求，严禁删减；
  - **Message ID 生命周期与非跨上游稳定标识 (Vendor-Local Message ID Handle)**：
    - NapCat 基于内存映射生成正数 int32 短 ID，LuckyLillia 基于数据库生成有符号 int32 短 ID。同一条真实 QQ 消息在不同上游下的 `message_id` 不可认为相同，亦不可跨上游连接或进程重启复用；
    - FrostAgent 明确将 OneBot `message_id` 界定为连接级不透明句柄（Opaque Handle），仅用于当轮会话上下文中的 quote/reply 引用查询与临时 sticker 溯源，不将其作为全局持久或跨上游稳定标识；
    - 引用消息查询（`get_msg`）在遇到过期 ID、上游重启后失效、消息已撤回、跨会话消息或查询超时时，严格执行确定性优雅降级（Graceful Degradation），回退为空引用上下文，绝不中断或阻断核心对话事件分发；
  - **发送失败与平台 ACK 语义规范 (Canonical Send Failure Semantics)**：
    - 前置校验：本地文件与贴纸在组装消息链时严格执行前置强校验（Fail-Fast），文件缺失或为空时立即拒绝发送；
    - 上游 ACK 门禁：通过 `SendActionAndWait` 监听平台响应。若上游返回错误码（如 LuckyLillia 在媒体转换抛错时整体报错，或群禁言/风控），FrostAgent 坚决不向持久历史提交该 assistant 消息，记录瞬态 `DeliveryFailure` 并向工具调用返回错误；若上游返回成功（如 NapCat 部分元素转换异常时局部过滤但整体返回成功），FrostAgent 尊重平台 ACK 并正常提交历史；
  - **入站媒体异常可观测性 (Inbound Media Resolution Observability)**：
    - 针对 NapCat“局部容错丢弃媒体但仍上报文本”与 LuckyLillia“媒体异常导致整条事件无法分发”的上游行为差异，适配器在收到媒体段但数据缺失或下载失败时输出明确的诊断日志，杜绝未记录的静默语义漂移。
- **AstrBot 适配器 (`internal/adapter/astrbot` 与 `adapters/astrbot_plugin_frostagent`)**：
  - 基于双向 WebSocket 长连接的轻量 JSON 专有协议；
  - 具备跨平台会话与记忆前缀隔离（如 `astrbot:group:<id>` / `astrbot:user:<id>`）；
  - 支持群聊无感 running compact 实时摄入与记忆反思；
  - 支持 Agent 工具调用阶段的 `sendHook` 中间消息流式即时下发；
  - 群聊回复统一在出站协议边界应用 `ENABLE_REPLY_IN_GROUP_MSG` 与 `ENABLE_AT_IN_GROUP_MSG`：自动引用当前入站消息并提及触发用户，显式 `quote` 组件优先且不会重复注入引用；AstrBot 插件将引用映射为平台原生 `Reply` 消息段；
  - 包含客户端自动断线重连与周期心跳保活。
- **共存与独立控制**：
  - 支持通过环境变量（`ENABLE_ONEBOT_ADAPTER`, `ENABLE_ASTRBOT_ADAPTER` 等）独立开启、关闭或共存运行多个适配器。

### 管理控制台架构 (Web Dashboard Architecture)

FrostAgent 管理后台采用超轻量、零运行时 UI 框架（Vanilla TypeScript + Vite + 原生 HTML/CSS + ConnectRPC）架构，具备极高的加载速度、极致简单的构建管道与出色的可维护性：

- **轻量原生架构**：
  - 彻底去除臃肿的前端框架运行时（零 Angular/React/Vue 依赖），直接基于原生 DOM、现代 ES 模块以及 HTML5 Web 标准原语（`<dialog>`, `<table>`, `<input type="range">`, `<details>` 等）构建；
  - 采用模块化页面生命周期挂载与清理机制（`mount` / `cleanup` / `router.register`），保证内存管理严谨、无泄漏；
  - 采用 Hash 路由体系（`/#overview`, `/#sessions`, `/#memory`, `/#dialogue`, `/#logs`, `/#settings`），支持页面前进后退、参数隔离与平滑过渡。
- **类型安全 RPC 传输**：
  - 前端基于 `@connectrpc/connect-web` 与 `@frostagent/proto`，实现端到端的 Protobuf 类型安全与请求/响应全量校验；
  - 支持 ConnectRPC Server-Streaming 实时日志长连接订阅与动态取消；
  - 敏感配置自动脱敏与按需显隐。
- **Control Plane 与实例生命周期隔离**：
  - General 日志通过根级 `LogService` 访问，不依赖当前选中实例的 Runtime、启用状态或操作锁；关闭 Control Plane 时会主动终止 General 与所有实例日志流，并拒绝已排队但尚未取得操作锁的生命周期写入；
  - 实例日志流只允许进入仍处于活跃 Scope 的 Runtime，且流请求上下文绑定到其捕获的 Runtime Scope；停止或删除过程中即使旧 Runtime 尚未解除引用，也不会在流终止后重新建立订阅；
  - 进入删除墓碑状态的实例仅允许读取状态和重试删除，禁止重命名、启停或参与快速配置，避免半删除配置被重新创建。
  - 概览分别展示实例管理名称与实例 `BOT_NAME`，并由 Control Plane 下发本次启动实际采用的 `WS_LISTEN_ADDR` 来生成实例专属适配器地址；设置页修改后的待重启值不会提前污染概览。AstrBot 插件要求显式配置该地址，不再回退到无实例路径。
  - MCP 配置、连接管理器与动态工具目录属于具体实例，分别持久化在 `data/instance_<id>/mcp_servers.json`；实例停用时配置仍可编辑，但所有实时 MCP 连接随实例停止，零实例时不暴露根级 `MCPService`。
  - 系统提示词（`SYSTEM_PROMPT`）与人设预设对话（Few-Shot Dialogue）实现实例级彻底隔离：系统提示词解耦自 Control Plane 全局配置，独立保存于各实例的 `data/instance_<id>/.env`，支持修改后内存即时热生效；人设预设对话独立保存于各实例的 `data/instance_<id>/dialogue.yml`，实例构建时载入内存并通过读写锁保证零读盘开销。两者共同构成实例的人设基石，并统一纳入两阶段克隆事务与清理清单。
  - Sandbox Gateway 地址、凭据与基础命名空间属于 Control Plane 配置；启用且配置有效时，每个实例按 `<基础命名空间>/<稳定实例 ID>` 派生独立 worker 命名空间并注册 `execute_command`。启动探测失败只记录告警，执行仍严格 fail-closed，不回退宿主机。
  - `execute_command` 的命令正文、stdout 与 stderr 不进入完整日志或终端摘要；审计元数据写入对应实例日志，并保留长度、哈希、退出码、超时及截断状态。
- **现代化设计令牌与主题系统 (shadcn/ui 风格)**：
  - 基于 Neutral Zinc 阶梯色彩与现代语义 CSS 变量系统（`--background`, `--foreground`, `--card`, `--primary`, `--muted`, `--border`, `--destructive`, `--radius`）；
  - 支持跟随系统（`prefers-color-scheme`）、明亮浅色、深邃暗色三种模式实时无缝切换与持久化；
  - 界面全局采用 110% 基础缩放比例（CSS `zoom: 1.1`），优化桌面视距与字号阅读体验。
- **单二进制静态嵌入与开发体验**：
  - Vite 构建产物直接输出至 `internal/frontend/dist`，由 Go 1.16+ `embed.FS` 单二进制内嵌打包分发；
  - 秒级极速热重载开发服务器与轻量 Makefile 自动化集成。

### 管理面与网络信任边界 (Management API & Network Trust Boundary)

为了防止管理接口与控制台在未经配置的情况下意外暴露至非受信网络环境，并防范恶意网页通过浏览器发起的跨域驱动攻击与 DNS 重绑定攻击，FrostAgent 构建了清晰纵深的管理面网络信任边界：

- **本地回环默认绑定 (Localhost Default Binding)**：
  - HTTP 管理面 (`LISTEN_ADDR`) 默认绑定到 `127.0.0.1:8080`；
  - WebSocket 适配器面 (`WS_LISTEN_ADDR`) 默认绑定到 `127.0.0.1:1234`；
  - 杜绝默认监听 `0.0.0.0` 或通配端口导致的未授权公网暴露。
- **严格同源、CORS 与 DNS Rebinding 边界防御 (Strict Same-Origin, CORS & DNS Rebinding Protection)**：
  - 管理接口通过 `corsMiddleware` 统一校验请求 `Host` 与 `Origin`；
  - **Host 白名单门禁**：仅信任本地回环 Host（`localhost`、`127.0.0.1`、`[::1]` 及 `127.0.0.0/8`），非回环 Host 必须显式属于 `HTTP_ALLOWED_ORIGINS` 声明的受信来源，拦截外部未授权 Host（直接返回 `403 Forbidden`），从传输层阻断 DNS 重绑定攻击；
  - **Origin 校验**：同源自动放行规则严格限制在本地回环 Host 上，杜绝攻击者利用解析至 127.0.0.1 的恶意域名伪造同源；如需远程或跨端口反向代理管理，需通过 `HTTP_ALLOWED_ORIGINS` 显式声明受信任的 Origin 白名单。
- **平台基线与操作系统支持边界 (Platform Security Baseline & OS Boundary)**：
  - **正式生产安全基线 (Linux/POSIX)**：FrostAgent 正式安全基线以 Linux/POSIX 部署（交付容器 Docker / Linux 主机环境）为准。在此类系统上，严格保证所有者独占 `0600` 权限、目录原子重命名替换、及启动时 fail-closed 权限防御（若历史 `.env` 无法收紧至 `0600` 则拒绝启用设置管理服务）；重命名失败时直接报错中断，**绝不回退至截断复制（copyFile）**，彻底避免破坏目标文件的原子性与完整性；
  - **开发环境兼容性 (Windows Dev Best-effort)**：Windows 裸机作为本地开发与测试环境提供 best-effort compatibility，并通过轻量 Windows Smoke CI 防范低级构建与测试回归；由于 Windows 平台文件 ACL 继承体系及文件重命名锁定语义与 POSIX 存在本质差异，Windows 裸机环境不承诺与 POSIX 等价的 `0600` 及原子替换安全语义，其文件锁定引发的重命名回退仅在 `runtime.GOOS == "windows"` 条件下作为开发调试兜底，与正式生产基线严格隔离。
- **单管理员控制台模型与认证现状说明 (Single-Administrator Console & Auth Status)**：
  - **当前真实边界**：当前版本核心安全边界为「默认仅回环绑定 + Host 头校验/DNS Rebinding 防护 + 严格同源/CORS 浏览器隔离」；应用层访问控制（如基于 Token 或密码的管理员登录认证）已规划于后续发布里程碑，当前版本尚未集成；
  - **控制台透明性**：在单管理员自托管架构下，已授权会话拥有实例的完全管理权限，因此设置接口（`ListEnvVars` 与 `GetRawEnvFile`）向管理员提供真实的配置与密钥显隐视图，不进行破坏性的阻断式脱敏，同时保持原始 `.env` 编辑器（`UpdateRawEnvFile`）的可用性；
  - **网络暴露风险警示**：在应用层认证正式落地前，若显式将 `LISTEN_ADDR` 绑定至局域网或公网 IP，属于显式信任网络/自担风险的 opt-in 行为；若必须远程访问，应在前置部署具备身份鉴权的反向代理（如 Nginx / Caddy 配合 Basic Auth 或 OAuth）。
- **环境变量白名单、语句级 Dotenv 解析与防换行/NUL 注入 (Settings API Allowlist, Statement-Aware Parsing & Injection Defense)**：
  - 通过 `knownEnvVars` 注册表对 `UpdateEnvVar` 与 `DeleteEnvVar` 进行严格键名白名单校验，拒绝任意未注册的环境变量写入；
  - **语句级语法解析与多行/重复键安全变异 (Statement-Aware Dotenv Mutation)**：
    - 废弃物理逐行扫描，引入严格镜像 `godotenv v1.5.1` 解析语义的语句级解析器（`parseEnvStatements`）；
    - **语句边界与终止符精准对齐**：完全对齐 `godotenv v1.5.1` 上游解析器的真实行为，遇到前驱字符为反斜杠 `\` 的引号作为转义引号跳过，完整识别跨物理行的单/双引号多行值声明；同时支持闭合引号后即刻将剩余字节交回主循环的同物理行多声明（如 `KEY1="val" KEY2=val`），在修改或删除目标配置时精确定位并单独处理，绝不产生孤儿延续行或误删/遗漏同物理行后续变量定义；
    - **重复键消除与规范化**：当原始 `.env` 存在重复定义键时，更新操作规范化首个匹配语句为 `key=value` 格式，并自动剔除所有后续同名重复声明；删除操作彻底移除全部同名声明；
    - **变体语法识别与注释保真**：原生识别 `export KEY=value` 与冒号分隔符 `KEY: value` 等常见变体语法；未修改的配置项、单行注释（`#`）与空行在变异后无损保真保留；
  - **多模式安全序列化与 godotenv v1.5.1 往返保真**：针对 `godotenv v1.5.1` 解析器的特定行为（如双引号终止符不判断奇偶反斜杠导致尾部反斜杠闭合失效、转义双引号修剪丢失等），采用自适应多模式序列化策略：
    - 普通单行安全值（无换行、无 `$` 变量展开标记、无首尾空格、不以引号开头、无行内注释）采用直接不加引号的格式落盘（`key=value`），原生保真保留 Windows 路径、末尾反斜杠及内部引号；
    - 以引号开头的安全值自适应采用单引号格式（`key='value'`）；
    - 包含空值、前后空白、`$` 变量、换行等多行配置项（如 `SYSTEM_PROMPT`）采用安全转义的双引号格式（`"...\n..."`）；
  - **写入前 Round-Trip 强校验与 Fail-Closed**：在实际落盘前，序列化器即时调用 `godotenv.Unmarshal` 对格式化后的条目进行解析回测，严格验证 `parse(format(value)) == value` 且无键分裂或多余键注入；对于解析器本身无法无歧义表示的非法输入，显式拒绝写入并返回错误，彻底杜绝配置损坏或服务重启后无法解析的风险；
  - **防换行注入与 NUL 字节防护**：单行环境变量在 API 层面严格禁止包含 `\r` 或 `\n` 字符；允许多行的配置项经转义双引号后在 `.env` 中落盘为单行记录，杜绝利用换行注入非受信环境变量；同时对包含空字节（`\x00`）的输入进行前置防御与显式拒绝，并完整捕获与向上传播 `os.Setenv` / `os.Unsetenv` 错误，避免因操作系统底层调用限制（`syscall.EINVAL`）导致内存与磁盘状态不一致。
- **并发互斥与安全原子落盘 (Concurrency Safety & Secure Atomic Writes)**：
  - `SettingsService` 内部维护互斥锁（`sync.Mutex`），全生命周期保护 `UpdateEnvVar`、`DeleteEnvVar`、`GetRawEnvFile` 与 `UpdateRawEnvFile`，避免高并发交错写入或结构化与 Raw 编辑交织导致配置覆盖或数据竞争；
  - 临时文件采用同目录唯一随机命名（`os.CreateTemp`），并在写入与提交前预先赋予 `0600` 权限，避免多协程写入碰撞且消除落盘后再次 `os.Chmod` 的潜在脆弱状态；
  - **权限收紧 Fail-Closed 机制**：服务初始化（`New`）时主动将已有 `.env` 文件权限收紧至 `0600`，若收紧失败则向上返回错误并拒绝挂载设置服务，防止静默运行于不安全权限下。
- **WebSocket 路由独立隔离 (Dedicated WebSocket Mux)**：
  - OneBot 与 AstrBot 协议适配器路由挂载于独立的 `wsMux` 上，避免与 `http.DefaultServeMux` 产生全局路由混淆。

### 表情包摘取与检索系统 (Sticker Stealing & Retrieval System)

为了让智能体兼具趣味性与原生聊天软件拟人化表达，FrostAgent 实现了表情包自主抓取、多模态视觉摘要与基于情绪语境的智能检索系统：

- **概率抓取与非阻塞并发门禁 (Probabilistic Stealing & Non-blocking Concurrency Gating)**：
  - 仅对 QQ 平台（OneBot 与 AstrBot `platform == "aiocqhttp"` 适配器中带有 `sub_type == 1` 贴纸标识）的群聊图片进行 25% 概率随机抓取，私聊与其他平台图片自动忽略；
  - 采用容量为 3 的信号量 Channel（`make(chan struct{}, 3)`）实现非阻塞截断机制。当群聊遭遇表情包刷屏时，超出并发上限的请求立即静默丢弃（Zero-Wait Dropping），杜绝多协程下载耗尽服务器带宽与计算资源；
  - 下载 HTTP 客户端强制 30 秒超时，超过 10 MiB 的图片直接拒绝（非静默截断），持久化写入失败时错误向上传播。
- **存储去重与权重回归机制 (Deduplication & Weight Regression)**：
  - 本地持久化保存于 `data/sticker/<hash>.<ext>`，全局元数据原子维护于 `data/sticker/index.json` 中；
  - 采用 SHA-256 图像哈希去重。若抓取到已存在的表情包，系统自动将其命中权重（`weight`）累加 1，群内热度越高的表情包权重值越高。
- **串行多模态视觉摘要管道 (Serialized Multimodal Vision Pipeline)**：
  - 异步单 Worker FIFO 队列解耦消息链路，避免并发调用多模态模型导致上游 API 速率（RPM/TPM）超限；
  - 调用模型路由器（`ModelRouter`）配置的视觉多模态模型（`VISUAL_MODEL_NAME`），提取表情包视觉描述并提炼情绪语境关键词（如 `["开心", "生气", "无语", "真的假的"]`）；
  - 视觉模型调用强制 60 秒上下文超时，防止请求无限挂起；
  - 当视觉模型不可用或网络异常时，标记状态为 `unsummarized`（未摘要）并隔离保护，绝不向大模型工具输出未摘要表情包；多模态服务恢复后支持批量一键重试并更新状态为 `ready`。
- **工具检索与轮盘赌加权随机选择 (`send_sticker` Tool & Weighted Roulette Selection)**：
  - 注册 `send_sticker` 工具供大模型在合适语境下调用；
  - 注册管理员限定的 `steal_sticker` 工具，用于响应管理员对当前、引用或近期同会话消息中 QQ 贴纸的显式摘取请求。权限仅按 `ADMIN_QQ_IDS` 中配置的发送者 QQ 号精确匹配，不接受模型传入身份；目标通过可信上下文中的消息 ID 与 `sub_type == 1` 附件索引选择，省略消息 ID 时使用最近一条带贴纸的消息，不接受任意 URL/Base64 参数。适配器将入站图片规范化为受限大小的字节数据，并在同会话有界缓存中保留 24 小时；显式摘取跳过随机概率，但继续复用并发上限、哈希去重、权重累加与视觉摘要管道；
  - 支持对表情包关键词和内容描述进行模糊语境匹配（Fuzzy Matching）；
  - 仅在 `ready` 状态的匹配候选集中，依据表情包的累计 `weight` 权重进行轮盘赌比例随机采样（Weighted Roulette Sampling），使高频出现的热门表情包具有更高的召回概率；
  - 出站消息自动注入 `is_sticker: true` 与 `sub_type: 1` / `subType: 1` 贴纸标识，由平台适配器发送为原生聊天贴纸而非普通图片。直连 OneBot 时由 FrostAgent 读取私有贴纸文件并编码为 `base64://...`，同时双写 `sub_type: 1` 与 `subType: 1` 确保 NapCat 与 LuckyLillia 均可正确处理，避免将仅在 FrostAgent 文件系统中存在的 `file://` 路径交给独立进程或容器解析。AstrBot 协议仅接收现有 `/api/sticker/{id}/image` HTTP 端点，不暴露 FrostAgent 私有存储路径；插件通过独立配置的 `http_base_url` 跨容器下载图片并转换为 OneBot 可消费的 `base64://...`。随后使用独立的 `StickerImage` 组件（不继承 AstrBot SDK 的 `Image` 类），绕过 aiocqhttp 对 `Image` 实例的强制 base64 转换与字段剥离（`_from_segment_to_dict` 的 `isinstance(segment, Image)` 分支），使 `toDict()` 返回的 OneBot 段（含 `sub_type: 1`）完整保留于通用 `segment.toDict()` 回退路径中；
  - Agent 工具执行循环通过检测工具返回结果中的 `messages` 载荷自动触发 `SendHook` 实际发送，无需按工具名硬编码分发。
- **Web 控制台表情包管理 (Web Dashboard Management)**：
  - Web 控制台在「Prompt 检查」下方提供「表情包摘取」独立管理页面（`/#stickers`）；
  - 提供表情包总数、就绪数、未摘要数与累计抓取总权重等统计卡片；
  - 支持按情绪关键词/描述实时模糊过滤与状态快速筛选；
  - 支持本地表情包手动上传入库、表情包关键词与描述在线编辑、单张删除以及一键重试所有未摘要表情包；
  - 提供 ConnectRPC `StickerService` 契约及 `/api/sticker/{id}/image` 原生 HTTP 缩略图流式直链服务；
  - `ListStickers` 接口支持基于 `page_token` 的偏移量分页，保证超出首页的数据可通过翻页访问。

### 模型上下文协议外部工具系统 (MCP Host Subsystem)

为了支持智能体动态接入更广泛的外部生态能力（如代码执行、文件系统操作、外部知识库、第三方 API 集成等），FrostAgent 实现了标准模型上下文协议（Model Context Protocol, MCP）的主机端（Host / Client）子系统：

- **定位与系统边界 (Role & Boundary)**：
  - FrostAgent 严格扮演标准 MCP Host（Client 角色），将外部 MCP Server 视为动态工具提供者（External Tool Provider）；
  - **普通工具语义对齐**：对于大模型及智能体循环（Agent Loop），MCP 工具在调用流程、参数组织与执行协议上与系统内置工具（`memory`, `send_msg`, `send_sticker` 等）完全等价，统一归入 `core.ChatRequest.Tools` 并在执行时由 `ToolExecutor` 统一调度；
  - **编译期静态适配器与运行期动态发现**：系统通过编译期静态编写的通用工具适配器（`ToolAdapter`），结合运行期动态拉取的工具目录（`ToolCatalog`），兼具 Go 语言的静态类型安全与 MCP 外部服务的热插拔灵活性；
  - **实例作用域配置**：MCP 服务器配置依附于具体运行实例（`data/instance_<id>/mcp_servers.json`），不设全局主开关，实例之间的配置、连接与工具状态完全隔离。
- **官方 SDK 与多传输协议支持 (Official Go SDK & Multi-Transport Implementations)**：
  - 全面基于官方 Go SDK（`github.com/modelcontextprotocol/go-sdk/mcp`）构建，废弃私有 JSON-RPC 解析；
  - **Stdio 子进程传输 (`officialmcp.CommandTransport`)**：支持本地命令行子进程模式，托管 stdin/stdout 标准流管道交互与 SIGTERM 优雅退出；支持自定义可执行命令、参数列表、工作目录以及环境变量；
  - **Streamable HTTP 传输 (`officialmcp.StreamableClientTransport`)**：全面支持 2025-03-26 MCP 传输协议标准规范；
  - **SSE 传输 (`officialmcp.SSEClientTransport`) 与传输层精细化控制**：支持 2024-11-05 标准服务器推送流，并通过自定义 `HeaderTransport` 装饰器实现请求头（如认证 Token）注入。流式 HTTP 客户端显式配置 `Timeout: 0` 保证长挂起流不被底层自动中断，结合精细化底层超时（`DialContext` 15s、`ResponseHeaderTimeout` 30s、`TLSHandshakeTimeout` 15s、`IdleConnTimeout` 90s）确保连接稳健；
  - **会话生命周期解耦 (`lifecycleCtx`)**：建立会话时将其与短期 RPC 握手上下文完全解耦，仅在显式停止、重启或代数更迭时取消，防止瞬时请求超时意外掐断常驻 SSE 流；
  - **生命周期协商与能力同步**：启动时由官方 SDK 完成 `initialize` 握手与 `notifications/initialized`，随后自动拉取 `tools/list` 建立动态工具目录；同时注册 `ToolListChangedHandler` 监听外部服务端工具变更通知并自动异步热更新。
- **实例生命周期加载 (Instance Lifecycle Loading)**：
  - Control Plane 构造实例时先同步载入该实例的持久化 MCP 配置，保证实例管理接口就绪时配置可读写；
  - 实例启用后异步连接其已启用的外部 MCP 服务器；实例停用时同步断开实时连接但保留期望启用状态，重新启用后自动恢复连接。
- **平台专属原生崩溃安全持久化 (Platform-Native Crash-Safe Atomic Persistence)**：
  - 配置存储（`ConfigStore`）负责将服务器配置与工具策略安全持久化至 `data/instance_<id>/mcp_servers.json`；
  - **Windows NTFS 原生原子替换**：在 Windows 平台采用 `golang.org/x/sys/windows` 直接调用 Win32 核心 API `MoveFileEx(MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH)`，实现文件系统层级的原子覆盖落盘，消除传统 `os.Rename` 在 Windows 上的文件占用与删除空窗期风险；
  - **Unix POSIX 原子重命名**：在 Linux/macOS 环境下采用标准 `os.Rename` 结合父目录 `fsync` 实现原子落盘与掉电保护。
- **控制平面安全门禁、跨源防御与敏感凭据脱敏 (Control Plane Auth, CSRF Defense & Secret Masking)**：
  - **统一网关跨源防护与 DNS 重绑定防御 (CORS, CSRF & DNS Rebinding Defense)**：针对恶意网页利用浏览器向 `localhost` 发起跨域请求或利用 DNS 重绑定（攻击者域名解析至 `127.0.0.1`，制造 `Origin == Host == attacker.example`）绕过回环认证的安全隐患，系统在 HTTP 全局入口层（`corsMiddleware`）实施严格的 Host 与 Origin 校验。网关强制验证请求 Host 头仅限本地回环（`localhost`, `127.0.0.1`, `[::1]`）或显式配置的 `HTTP_ALLOWED_ORIGINS`，且同源自动信任仅在请求 Host 本身为回环地址时成立。非受信跨源请求直接由网关层拒绝并返回 `403 Forbidden`，从传输层杜绝网页端逃逸执行本地 stdio 进程（RCE）的风险；MCP 服务层则专注管控本地回环与远程 Bearer Token 鉴权边界；
  - **网络边界认证与本地控制台防锁死机制 (Local Same-Origin vs. Remote Bearer Token)**：远程访问（非回环 IP）强制要求配置 `MCP_CONTROL_TOKEN` 或 `ADMIN_TOKEN` 并在请求中提供合法 Bearer 认证；本地同源访问（本地回环 + 受信同源）默认放行以保证内置控制台开箱即用（支持配置 `MCP_ENFORCE_LOCAL_TOKEN=true` 开启本地严格鉴权）。同时前端 ConnectRPC 客户端内置 `authInterceptor` 拦截器，支持在 Web 端外观设置中配置与持久化访问 Token，确保远程部署和受保护环境下控制台顺畅交互；
  - **敏感凭据全面脱敏防护**：在环境变量及请求头中，对包含 `KEY`, `TOKEN`, `SECRET`, `PASSWORD`, `PASSWD`, `AUTH`, `CREDENTIAL`, `PRIVATE`, `COOKIE` 以及 `Authorization` 的敏感信息在读取接口（`ListMCPServers`, `GetMCPServer`）中统一脱敏展示为 `"******"`；
  - **更新保全机制**：前端在提交配置修改时若传回脱敏占位符（`"******"`），后端自动从现有配置中保全并还原原始密钥，杜绝密钥因回传占位符而被意外覆盖破坏；
  - **上下文透传与启动错误真实反馈**：`SetServerEnabled` 完整透传 RPC 请求上下文并设置启动超时保护，当外部子进程或网络传输握手失败时，将真实错误向上传播给控制台 RPC 响应，杜绝将启动失败伪报为“成功”的操作误导。
- **代数令牌、状态幂等与生命周期竞态消除 (Generation Tokens & State Idempotency)**：
  - **启动/关闭代数令牌 (`generation uint64`)**：为每次服务器启动分配单调递增的代数令牌，启动前强制取消前序上下文，连接建立后校验代数。当用户在慢连接建立过程中点击停止或删除时，迟到的连接会因代数不匹配被直接丢弃并关闭，杜绝已停止进程“僵尸复活”；
  - **启停幂等性防护与资源即时回收**：`SetEnabled` 实现严格幂等防护，防止重复启用造成子进程与网络会话多重泄漏；新启动时显式异步关闭前序存活会话；
  - **并发安全策略隔离**：在更新工具策略或重新同步目录时，使用 `maps.Clone(s.cfg.Tools)` 隔离读写副本，配合互斥锁彻底消除了目录同步协程与控制台开关并发时的 Go map 数据竞态。
- **进程退出监控与动态下线 (Process Termination Watcher & Dynamic Removal)**：
  - 每个连接会话在后台协程中主动监听 `session.Wait()`，一旦外部子进程异常崩溃或断开，立即将服务器运行时标记为 `StatusFailed` 并捕获退出错误信息，自动从后续轮次的 `EffectiveTools()` 中摘除全部失效工具，杜绝向大模型暴露死工具。
- **大模型工具命名规范化与双向持久映射 (LLM Tool Name Sanitization & Persistent Mapping)**：
  - **统一字符集约束**：严格遵守所有主流 LLM 提供商对函数名称的字符集与长度约束（`^[a-zA-Z0-9_]{1,64}$`）；
  - **自动清洗、截断与 SHA-256 防碰撞**：对超出 64 字符或包含非法字符（如 `:`, `-`, `/`, `@`）的工具名自动执行合法化清洗，并在截断时附加 8 字符 SHA-256 唯一哈希后缀（`mcp__<srv>__<tool>_<hash>`），彻底规避名称冲突；
  - **全局持久双向映射引擎**：`mcp.Manager` 内置维护全量已知工具的 `exposedToTarget` 与 `targetToExposed` 双向稳定映射。即使工具因开关被临时禁用（不在 `EffectiveTools` 中），其哈希命名反查映射仍稳固存在，并发在途执行请求能够精准反解出目标工具并优雅返回语义化禁用说明，避免路由失效。
- **两级粒度控制与轮次动态快照 (Two-Level Toggles & Dynamic Snapshotting)**：
  - **服务器级开关 (`enabled`)**：控制单个 MCP 外部服务的启停。停用时自动断开连接并安全清理回收子进程资源；
  - **工具级开关 (`enabled`)**：细粒度控制单个工具的可用性，用户的开关决策保存在持久化策略中，不随重新拉取目录而丢失；
  - **轮次动态刷新 (`EffectiveTools`)**：Agent Loop（`runLoopWithResult`）在每次向大模型发送对话前（包括多轮推理的后续轮次），原子重新计算当前可用且已启用的工具快照。管理员在控制台动态开关工具或服务后无需重启，下一轮推理即可无缝生效。
- **执行阶段双重校验与优雅降级 (Execution Double-Check & Semantic Degradation)**：
  - **执行即时校验**：不仅在发送大模型前过滤无效工具，在模型发起 `tools/call` 执行阶段，适配器与运行时再次执行三重校验（服务器启用状态、工具启用状态、连接活跃状态）；
  - **语义化错误反馈**：若在模型思考与执行的微小窗口期内服务器断开或被手动停用，系统向大模型返回明确的人类可读解释（如 `mcp server is disabled` 或 `tool is currently disabled`），大模型获知原因后可平滑降级（如向用户说明原因或改用其他策略），绝不崩溃或中断 Agent 循环。
- **安全性防护设计 (Security Boundaries)**：
  - 明确界定大模型自身的调用权限：大模型仅能调用已暴露的 MCP 业务工具，严禁大模型通过会话直接添加、修改或配置 MCP 服务器，彻底阻断利用 Stdio 子进程命令参数进行任意代码执行（RCE）的安全漏洞。
- **Web 控制台管理界面 (Web Dashboard Management)**：
  - Web 控制台在「表情包摘取」下方提供独立的「MCP 服务器」管理页面（`/#mcp`）；
  - 沉浸式空状态与紧凑型状态统计：0 Server 时呈现轻量引导视图；已配置 Server 时提供一目了然的服务器/工具连接与启用状态概要；
  - 层次化服务器管理列表：展示连接状态标签（已连接、启动中、异常、已停止）、运行命令/URL、最后错误排查横幅；
  - 支持服务器一键启用/停用、重新连接/同步目录、配置修改与删除（含二次确认）；
  - 工具目录面板：支持单个工具独立开关、完整命名空间名称复制、以及 OpenAI 兼容参数 JSON Schema 检查器；
  - 新增/编辑服务器对话框：支持 Stdio、Streamable HTTP 与 SSE 三种传输模式；
  - **可视化表单与 JSON 实时双向识别与同步 (Bi-directional Form-JSON Sync)**：提供「表单配置」与「JSON 编辑」双模式视图。以 `DraftServerConfig` 状态为单一事实源（Single Source of Truth），采用状态机词法扫描器（Quote-aware Scanner）在保护 URL 内双斜杠（如 `https://...`）的前提下精准剥除 JSONC 注释与尾随逗号；命令行参数采用原生数组结构（独立 argv 动态输入行），确保含空格、引号与空参数在 Form ↔ `string[]` ↔ JSON 之间 100% 无损可逆，并保证在 JSON 编辑中删减字段时完全重置对应配置为干净初始状态；支持一键识别 Claude Desktop 格式（`mcpServers`）、单服务包裹对象与标准 MCP 配置，提供格式化、一键复制与粘贴校验。
  - 基于 ConnectRPC 的 `MCPService` 端到端类型安全接口交互。

### 沙箱隔离与命令执行系统 (Sandbox Backend & Isolated Execution System)

FrostAgent 为智能体赋予执行 Shell 命令的能力，同时严格维持核心安全不变量（Security Invariant）：**FrostAgent 绝不在宿主机上直接执行任何由大模型生成的任意命令**。

- **分层执行架构 (Tiered Execution Architecture)**：
  执行链条严格遵循单向隔离调用管道：
  ```
  LLM (Agent Loop)
   ↓
  execute_command Tool
   ↓
  SandboxBackend (中立接口)
   ↓ HTTP
  code-interpreter Gateway
   ↓
  Worker (Docker / MicroVM)
   ↓
  non-root sandbox user
   ↓
  bash
  ```
- **中立后端与实现隔离 (Neutral SandboxBackend)**：
  - `internal/sandbox.Backend` 定义中立抽象接口（`Exec`、`Release`、`Health`），解耦 FrostAgent 核心与具体的沙箱运行时技术；
  - 当前实现为 `codeinterpreter.Client`，通过 HTTP 协议与外部 `code-interpreter` Gateway 交互；
  - Control Plane 通过共享的 `ConfigManager` 管理原子配置快照，每个实例的 `DynamicBackend` 在基础命名空间后追加稳定实例 ID，并在运行时动态感知管理面板的启停状态；
  - 架构中不存在 `LocalBackend` 或 `HostBackend`，彻底消除由于实现冗余带来的配置绕过风险。
- **Fail-Closed 与无本地回退 (Fail-Closed & No Local Fallback)**：
  - `execute_command` 工具常驻注册于 `ToolRegistry`；当沙箱运行时未启用（`SANDBOX_ENABLED=false`）时，工具在被模型调用时明确返回友好提示（“沙箱功能已被禁用，请前往 FrostAgent 管理面板启用它。”），避免弱模型因缺失工具而产生幻觉虚构执行结果；
  - 当沙箱服务离线、响应超时、认证失败或发生协议异常时，工具执行严格失败中断（Fail-Closed），向模型返回清晰错误提示；
  - 严禁在沙箱不可用时回退到宿主机的 PowerShell、Bash 或 Cmd 执行。
- **会话级文件系统隔离与状态持久化 (Session-Scoped Filesystem Persistence)**：
  - 工具调用依赖请求级上下文（`llm.RunContext.SessionID`），缺失 `SessionID` 时直接拒绝执行；
  - 通过固定命名空间与 `SANDBOX_SESSION_NAMESPACE` 对 FrostAgent `SessionID` 进行确定性 RFC4122 v5 UUID 映射，作为 Gateway `user_uuid`；
  - 同一 FrostAgent 会话内的多次命令执行保持文件系统状态（如创建文件、编译中间产物），但每次调用均为干净独立的 Shell 进程；
  - 不同会话严格对应不同的 Worker/用户隔离空间，互不可见且杜绝跨会话状态穿透；禁止在单次命令调用后自动释放沙箱。
- **凭据隔离与有界输出保护 (Credential Isolation & Bounded Output)**：
  - 沙箱网关的 `X-Auth-Token` 仅存在于控制面 HTTP 请求头，绝不作为环境变量或参数传递给沙箱容器，日志中对令牌自动脱敏；
  - `SANDBOX_BASE_URL`、`SANDBOX_AUTH_TOKEN` 与 `SANDBOX_SESSION_NAMESPACE` 作为同一 Control Plane 配置快照加载，修改后仅在重启 FrostAgent 时整体生效；运行期只允许热切换 `SANDBOX_ENABLED`，防止网关迁移或密钥轮换期间产生混合端点与凭据泄露窗口；
  - 针对 Agent 循环的 64 KiB（`MaxToolOutputBytes`）限制，`execute_command` 工具层在返回前对 stdout/stderr 进行双向前后截断保护（保留头部与包含报错堆栈的尾部，中间填充标记），确保模型接收到的始终是合法可解析的结构化 JSON。
- **安全边界划分 (Safety Boundary Separation)**：
  - 明确区分结构化受限工具（Structured Bounded Tools，如 GitHub API、HTTP Fetch）与任意命令执行（Arbitrary Shell）；
  - 任意 Shell 命令必须且只能受限于沙箱沙盒生命周期，宿主机仅作为控制面运行。

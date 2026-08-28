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

### 人设与少样本示例系统 (Persona & Few-Shot Dialogues)

为了增强智能体的人设表达（语气、口吻、句式格式），FrostAgent 支持通过 YAML 文件配置示例对话（默认为 `eval/dialogue/dialogue.yml`），并在会话执行时注入为系统提示词：

- **引导提示词**：`以下是示例对话，请仿照句子格式、语气等回应接下来的用户输入。`
- **示例对话格式**：包含用户问题（`user`）与期望回复（`preferred`）的 Few-Shot 对话示范。
- **系统提示词合成**：在每次调用大模型前，系统提示词依次组合：系统时间 -> 基础系统提示词 -> 示例对话 Few-Shot -> 记忆主题目录 -> 召回记忆与隔离规则。
- **Web UI 与热更新**：前端控制台提供独立的「示例对话」管理页面（入口位于「记忆」下方），支持无限增删改查对话卡片、调整提示词顺序、实时提示词片段预览以及直接编辑原始 YAML。后端提供 `DialogueService` ConnectRPC 服务，修改后自动原子更新 YAML 文件并实时热重载生效至全局智能体引擎 `GlobalEngine.DialoguePrompt`，无需重启服务。
- **可配置与优雅降级**：可通过环境变量 `DIALOGUE_PATH` 指定路径，文件不存在或解析为空时平滑跳过，不影响正常对话。

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
  - 支持 OneBot v11 Reverse WebSocket 协议，负责原生 QQ 消息段解析、群聊/私聊事件处理与会话轮次锁定。
- **AstrBot 适配器 (`internal/adapter/astrbot` 与 `adapters/astrbot_plugin_frostagent`)**：
  - 基于双向 WebSocket 长连接的轻量 JSON 专有协议；
  - 具备跨平台会话与记忆前缀隔离（如 `astrbot:group:<id>` / `astrbot:user:<id>`）；
  - 支持群聊无感 running compact 实时摄入与记忆反思；
  - 支持 Agent 工具调用阶段的 `sendHook` 中间消息流式即时下发；
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
- **现代化设计令牌与主题系统 (shadcn/ui 风格)**：
  - 基于 Neutral Zinc 阶梯色彩与现代语义 CSS 变量系统（`--background`, `--foreground`, `--card`, `--primary`, `--muted`, `--border`, `--destructive`, `--radius`）；
  - 支持跟随系统（`prefers-color-scheme`）、明亮浅色、深邃暗色三种模式实时无缝切换与持久化。
- **单二进制静态嵌入与开发体验**：
  - Vite 构建产物直接输出至 `internal/frontend/dist`，由 Go 1.16+ `embed.FS` 单二进制内嵌打包分发；
  - 秒级极速热重载开发服务器与轻量 Makefile 自动化集成。

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
  - 支持对表情包关键词和内容描述进行模糊语境匹配（Fuzzy Matching）；
  - 仅在 `ready` 状态的匹配候选集中，依据表情包的累计 `weight` 权重进行轮盘赌比例随机采样（Weighted Roulette Sampling），使高频出现的热门表情包具有更高的召回概率；
  - 出站消息自动注入 `is_sticker: true` 与 `sub_type: 1` 贴纸标识，由平台适配器发送为原生聊天贴纸而非普通图片。AstrBot 协议仅接收现有 `/api/sticker/{id}/image` HTTP 端点，不暴露 FrostAgent 私有存储路径；插件通过独立配置的 `http_base_url` 跨容器下载图片并转换为 OneBot 可消费的 `base64://...`。随后使用独立的 `StickerImage` 组件（不继承 AstrBot SDK 的 `Image` 类），绕过 aiocqhttp 对 `Image` 实例的强制 base64 转换与字段剥离（`_from_segment_to_dict` 的 `isinstance(segment, Image)` 分支），使 `toDict()` 返回的 OneBot 段（含 `sub_type: 1`）完整保留于通用 `segment.toDict()` 回退路径中；
  - Agent 工具执行循环通过检测工具返回结果中的 `messages` 载荷自动触发 `SendHook` 实际发送，无需按工具名硬编码分发。
- **Web 控制台表情包管理 (Web Dashboard Management)**：
  - Web 控制台在「Prompt 检查」下方提供「表情包摘取」独立管理页面（`/#stickers`）；
  - 提供表情包总数、就绪数、未摘要数与累计抓取总权重等统计卡片；
  - 支持按情绪关键词/描述实时模糊过滤与状态快速筛选；
  - 支持本地表情包手动上传入库、表情包关键词与描述在线编辑、单张删除以及一键重试所有未摘要表情包；
  - 提供 ConnectRPC `StickerService` 契约及 `/api/sticker/{id}/image` 原生 HTTP 缩略图流式直链服务；
  - `ListStickers` 接口支持基于 `page_token` 的偏移量分页，保证超出首页的数据可通过翻页访问。

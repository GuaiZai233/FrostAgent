## 全局安全控制体系

FrostAgent 将安全控制收束在共享的 `security.Controller`，而不是把权限判断分散到各个工具、平台适配器或实例运行时。本体系为 FrostAgent 所有平台适配器（OneBot、AstrBot 以及未来的 Telegram 等）提供统一的 Global Access Control 与 Watchdog 安全基础设施。

- **主体身份规范化与 Transport 解耦**：可信身份始终由平台适配器统一归一化生成，主键为 `(canonical_platform, user_id)`。Adapter/Transport 协议名称（如 `onebot`、`aiocqhttp`、`cqhttp`）明确与用户平台身份解耦，统一映射为标准平台标识（如 `qq`），确保同一真实用户在不同协议传输接入下共享同一身份标识与安全审计边界。模型输入、工具参数与模型文本均不可声明或伪造身份。
- **全局访问状态与跨实例一致性**：`AccessStore` 将 `ACTIVE` / `LOCKED` 状态持久化到全局根数据目录（`BRAIN_PATH`），并使用跨进程文件锁和操作系统原子替换写入协调并发（Windows 平台采用带 `MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH` 的 `MoveFileExW`，POSIX 平台采用临时文件替换与父目录 `fsync`），杜绝因删除后重命名失败引发的 fail-open 风险。多实例共享全局唯一 Controller，任何实例或适配器发生的锁定操作立即对全量实例和全部适配器生效。
- **可插拔分类器架构与 LLM 安全网关（Classifier Gateway & Hybrid Fallback）**：
  - 彻底移除了原先硬编码在 `watchdog.go` 内的脆弱静态正则表达式列表（`dangerousPatterns`），避免攻击者通过小语种多语言翻译、语法倒装或同义替换轻易绕过。
  - 抽象出标准 `Classifier` 接口，支持多层级安全审查组件：
    - **`LLMClassifier`（语义安全网关）**：基于 `core.LLMProvider` 驱动的高鲁棒性安全网关，采用 `<content>...</content>` 隔离定界符防御定界符注入逃逸，通过严格结构化 JSON 输出对输入文本进行语义级越狱、注入意图、破坏性命令与数据窃取分析，天然具备极高跨语言防御能力。
    - **`CalibratedClassifier`（多语言校准分类器）**：本地确定性多语言分类器，涵盖英、中、意、俄、日、西、法、德等主流语言提示词注入与越狱规则、破坏性执行指令（反弹 Shell、破坏性删除、Fork Bomb）、凭据收割以及腾讯平台合规策略规则，内置**教育与分析上下文检测**（识别 "what is rm -rf"、"如何防范提示词注入" 等良性科普讨论），彻底杜绝敏感词盲目误报。
    - **`HybridClassifier`（生产级混合分类器）**：首选调用 LLM 安全网关进行深层语义判断，当 LLM 未配置或遭遇网络/超时异常时自动无缝降级至确定性校准分类器，确保系统 Fail-Closed 安全不变量与极高可用性。
    - **生产环境运行链路装配（Production Path Wiring）**：在实例运行时初始化（`internal/instance/runtime.go` `buildRuntime`）中，生产控制器统一通过 `securityController.SetLLMProvider(routerManager.Provider(modelrouter.WorkloadDialogue, false, 5*time.Second), "model-router-security-gateway")` 注入真实模型路由器提供者；在适配器 Ingress 与 Engine 处理阶段传递运行时上下文（`GateIngressWithContext` / `EvaluateWithContext`），确保实例销毁与超时能够及时级联取消安全网关的在途请求。
    - **严格结构化输出强校验（Structured Output Validation）**：LLM 返回的 JSON 载荷解析后通过 `ClassificationResult.Validate()` 执行全面强校验，严格检查枚举值规范化（`NormalizeRiskCategory`、`NormalizeRiskLevel`、`NormalizeActorIntent`）、置信度区间 `[0.0, 1.0]`（严防 `math.NaN` 与 `math.Inf`）以及类别与风险等级一致性不变量（`none` 类别必须对应 `none` 风险等级且不可标记恶意意图；非 `none` 类别严禁标记 `none` 风险等级）。任何畸形、未知枚举或不变量违规均被作为分类器失败处理，由 `HybridClassifier` 安全回退至本地校准分类器，杜绝结构化污染。
    - **分类器异常 Fail-Closed 与无辜用户免罚保证**：分类器报错或超时被严格设计为无条件阻断（`WatchdogBlock`），同时明确隔离用户惩戒，绝不给触发用户记录 Strike 或添加锁定，兼顾安全底线与用户体验。
- **结构化风险信号体系（Structured Risk Signals）**：
  审查结果输出为标准 `ClassificationResult`，包含多维细粒度信号：
  - **风险类别（`RiskCategory`）**：提示词注入/越狱（`PROMPT_INJECTION`）、恶意破坏性执行（`MALICIOUS_EXECUTION`）、凭据与数据窃取（`EXFILTRATION`）、平台合规策略违规（`POLITICAL`、`VIOLENCE`、`FRAUD`、`VULGARITY`）、无风险（`NONE`）。
  - **风险级别（`RiskLevel`）**：`NONE`、`LOW`、`MEDIUM`、`HIGH`、`CRITICAL`。
  - **行为主体意图（`ActorIntent`）**：良性（`BENIGN`）、存疑/模糊（`AMBIGUOUS`）、恶意（`MALICIOUS`）。
  - **置信度（`Confidence`）**：`0.0 ~ 1.0` 浮点数置信分。
  - **来源溯源（`Origin`）**：完整的输入来源标记。
- **Watchdog 检查点与 Provenance 溯源**：Watchdog 覆盖直接用户入口（`USER_DIRECT`）、用户引用回复上下文（`USER_QUOTE_REPLY_CONTEXT`）、群聊摘要与近期群消息（`GROUP_CONTEXT`）、多模态视觉处理结果（`VISION_RESULT`）、平台元数据（群名、用户昵称、群名片等 `PLATFORM_METADATA`）、工具参数（`TOOL_ARGUMENT`）、工具返回结果（`TOOL_RESULT`）以及最终模型输出（`MODEL_OUTPUT`）全生命周期。所有非直接上下文在注入 LLM Prompt 前均必须经过独立 Checkpoint 审查。
- **内容阻断与用户处罚分级（低阻断门槛、极高锁定门槛）**：
  - 非直接来源（引用消息、群摘要、群历史、视觉识别结果、平台元数据、工具结果、模型输出）命中危险规则时，仅执行内容阻断或隔离（`BLOCK`），绝不增加 Strike 计数，绝不处罚当前触发请求的用户。
  - 存疑模糊判定（`ActorIntent == IntentAmbiguous` 或 `Confidence < 0.70`）仅执行内容阻断（`BLOCK`），严禁对主体增加 Strike 或执行锁定。
  - 直接用户输入的首次违规（包括模糊匹配与主动编码）仅阻断内容（`BLOCK`），记录阻断哈希与时间，strike 计数保持为 0，不施加用户级处罚。
  - 唯有在已有阻断历史的窗口期内发生明显重复提交或真正编码规避尝试时，才累计 `STRIKE`，并在达到阈值（3 次）后升级为全局锁定（`LOCK`）。
  - **精准规避判定与良性编码解耦**：规避证据（`isEvasion` / `encoded=true`）仅在归一化揭示了原始输入中不存在的危险规则（`!rawMatches && normMatches`），或通过归一化还原了此前被阻断的标准载荷哈希（`normHash == lastBlockedHash && rawHash != normHash`）时才被采信并计入惩罚；包含良性 URL 编码（如 `https://example.com/foo%20bar`）或良性字符但危险指令本就以明文出现的直接违规输入，仅做常规阻断，绝不滥记编码规避 Strike。
- **显式安全拦截报错与双重安全边界解耦（Inspector vs. Gateway）**：
  当用户请求被安全控制拦截时，不再静默丢弃（"一声不吭"），而是向用户返回具有明确安全责任边界的 FrostAgent 层面标准报错：
  - **安全审查员拦截（Security Inspector）**：当活跃用户的消息内容命中 Watchdog 阻断或违规规则时，返回：
    `FrostAgent 错误：Request rejected by security inspector: 不合适的内容！`
  - **安全网关封禁拦截（Security Gateway）**：当主体在安全访问控制网关中处于封禁/锁定状态（已在 `AccessStore` 中处于 `LOCKED` 状态，或由 Strike 升级为 `WatchdogLock`），返回：
    `FrostAgent 错误：Request rejected by security gateway: 您已被封禁，请联系管理员。`
  - **前置安全不变量与群聊礼貌拦截**：拦截报错在适配器 Ingress 入口最前沿发送（早于消息会话映射、群聊上下文缓冲、贴纸观察、视觉处理与 LLM 调用）；在私聊场景下始终返回报错；而在群聊场景中，仅当机器人被显式触发（@机器人、别名/唤醒词唤醒，或配置了 `GROUP_REPLY_ON_MENTION=false`）时才发送拦截报错，避免在未艾特机器人的群聊背景对话中因出现敏感词或被封禁用户发言而产生非预期的机器人报错打扰。群聊唤醒判定严格限定于本地 Ingress 元数据，严禁在安全判定阻断后为解析引用回复发起 `get_msg` 等上游平台 RPC，杜绝针对被拦截主体的额外网络消耗与潜在滥用面。
- **有界归一化与组合式规避解码（Compositional Normalization Pipeline）**：
  - Watchdog 设置明确的安全审查上限（256 KiB）。超出上限的超大输入直接 Fail-Closed 阻断，坚决杜绝静默截断放行导致的尾部走私注入。
  - 编码归一化采用容错百分号扫描器（`tolerantPercentUnescape`），按字节逐个解码有效的 `%[0-9a-fA-F]{2}` 序列，同时完整保留混杂的畸形转义（如 `%ZZ`、末尾悬空 `%`、未截断十六进制）和字面加号（`+`），彻底避免传统全有或全无解码器在遇到畸形字符时直接中断解码导致危险载荷漏检，并杜绝将 `C++` 或 `A+B` 误判为编码规避。
  - **多层组合归一化流水线**：采用有界循环（最多 3 轮），按层序依次瓦解复合规避手段：
    1. 容错百分号解码（`tolerantPercentUnescape`）；
    2. 全文 Base64 候选解码（`tryDecodeBase64`）；
    3. 嵌入式 Base64 候选替换（`replaceEmbeddedBase64`，识别 `echo <b64> | bash` 等混入明文命令中的 Base64 代码片段）；
    4. 不可见字符、零宽空格、双向文本控制符与标签符清除（`stripZeroWidthAndControl`）；
    5. 全角 ASCII 符号归一化（`0xFF01..0xFF5E -> 0x0021..0x007E`）；
    6. 数学花体与数学字母数字符号映射（`U+1D400..U+1D7FF -> ASCII A-Z, a-z`）；
    7. **混合语系西里尔同形字精准还原（`normalizeMixedScriptCyrillic`）**：针对利用西里尔字母 `о`、`е`、`а`、`р`、`с` 等与拉丁字母形似的混淆载荷（如 `ignоrе аll prеviоus`），仅在混合语系混淆词素中映射为 ASCII 拉丁字符，通过非同形原生西里尔字母特征精准识别并保留正规西里尔文本（如俄语、乌克兰语），杜绝破坏正常多语言处理。
- **对抗评估基准体系（Adversarial Evaluation Framework）**：
  - 新增 `internal/security/eval` 模块，提供标准测试语料（`TestCase`）与自动化评估测试套件（`EvalRunner`）。
  - 评测矩阵覆盖：
    - **误阻断率（False Block Rate）**：衡量良性科普、编程讨论、安全分析等查询被误拦的比例，基准测试目标为 0.00%。
    - **误封禁率（False Lock Rate）**：严禁无恶意或存疑用户被非预期锁定，实施 **Zero-Tolerance** 零容忍指标（基准测试达到 0.00%）。
    - **严重漏报率（Severe Miss Rate）**：多语言越狱、破坏性命令与敏感信息窃取载荷漏检率，基准测试达到 0.00%。
    - **综合准确率（Accuracy）**：全语料决策匹配准确率达到 100.00%（根据语料中显式定义的 `ExpectedAction` 与 `ExpectedCategory` 逐条严密断言，杜绝非预期放行或误报在未配置特殊标记时被静默统计为成功的缺陷）。
  - 配套自动化重复规避升级测试（`RunRepeatedEvasionSuite`）与跨实例一致性测试（`RunCrossInstanceConsistencySuite`），在测试多实例共享 `AccessStore` 时不仅检验良性并发读取，更通过跨实例并发变动 Strike 与锁定状态，验证多实例在强并发竞争下状态更新的原子同步与一致性。
- **安全审计凭据脱敏与最小化存储**：安全审计日志记录（`security_audit.jsonl`）的摘要预览（`safePreview`）在截断前必须经过严格的凭据脱敏处理（Redaction），过滤 Bearer Token、URL 查询凭据、API Key（如 `sk-...`、`glpat-...`、`xoxb-...`）、GitHub 经典 PAT 及各类 Token（`ghp_`、`gho_`、`ghu_`、`ghs_`、`ghr_`）、GitHub 细粒度 PAT（`github_pat_...`）以及密码字段，防止敏感鉴权凭据持久化到本地日志造成横向移动风险。
- **统一工具边界**：Engine 公共工具循环在执行内置工具和 MCP 工具前审查参数，执行后隔离高风险工具结果，模型输出后进行出站审查，中间 SendHook 也经过严格管控。
- **管理面板与控制面接口**：控制面根路径提供受 Bearer 认证保护的 `/api/security/locked` 与 `/api/security/unlock` REST 端点，与 MCP 控制面深度复用统一的 Scoped `instanceconfig.Store` 鉴权源，在无需将 Token 镜像到操作系统进程环境变量的配置存储模式下依然能够严格受控，配合 Web 控制台「安全控制」页提供全局锁定状态的可视化查看与人工解锁支持。
- **内部测试与 Synthetic Principal**：通用 `eval` / synthetic principal 概念保留用于内部 Watchdog 单元测试及未来的自动化测试 Harness；系统不暴露或维护任何面向第三方的 HTTP Evaluation API（如 `/v1/messages`），后续真实平台测试将通过新的 Telegram Adapter 完成。

该体系的核心不变量是：全局锁定状态在所有适配器 Ingress 阶段（早于群聊压缩、贴纸观察、会话锁、视觉处理及 LLM 调用）和 Engine 执行入口立即生效；被锁定的主体绝对无法进入任何业务流转。

## Multi-instance Architecture and Platform Foundation

The Control Plane owns one persistent security Controller at the global data root and injects that same Controller into every instance Runtime and Engine. Consequently, a lock created through any instance or adapter is visible immediately to all other instances and transports, while audit records retain the actual instance, session, stage, and provenance source identifiers.

Key design guarantees:
1. **Scope**: Global Access Control + Watchdog foundation for all FrostAgent platform adapters. (Telegram Adapter and Telegram-based adversarial E2E testing are tracked separately and not part of this foundation PR).
2. **Platform Canonicalization**: Decouples transport protocol names (`onebot`, `aiocqhttp`) from user principals (`qq:<user_id>`), guaranteeing that security policies and lock states apply to the human actor across transports.
3. **Pluggable Classifier Gateway**: Replaced fragile static regex lists with a pluggable `Classifier` architecture featuring `LLMClassifier` (semantic security gateway via `core.LLMProvider`), `CalibratedClassifier` (deterministic multilingual classifier with educational intent detection), and `HybridClassifier` (LLM with seamless fail-closed fallback).
4. **Indirect Context Checkpoints**: Injected prompt context (quoted reply messages, group running summaries, and recent group history) is vetted via distinct Watchdog provenance sources (`USER_QUOTE_REPLY_CONTEXT`, `GROUP_CONTEXT`), isolating dangerous injection without penalizing the current caller.
5. **Graduated Enforcement**: Lower threshold for blocking content, higher threshold for penalizing users. Single fuzzy or encoded violations result in content blocking; only deliberate, repeated evasion attempts within the sliding window accrue strikes toward a permanent lock. Non-direct contexts and ambiguous decisions never strike or lock principals.
6. **Bounded Compositional Normalization**: Multi-pass pipeline uncovering embedded Base64, Cyrillic lookalikes in mixed scripts, mathematical stylized runes, fullwidth ASCII, and zero-width/control characters without ReDoS vulnerabilities.
7. **Adversarial Evaluation Framework**: Native evaluation runner (`internal/security/eval`) measuring False Block Rate, False Lock Rate (Zero Tolerance), and Severe Miss Rate across multilingual benchmark corpuses.
8. **Fail-Closed Atomic Persistence**: Windows `MoveFileExW` and POSIX atomic renames prevent corrupted or missing security state files from failing open under high concurrency.
9. **Explicit Security Rejections & Boundary Decoupling**: Security interventions return explicit FrostAgent-level error notifications rather than dropping requests silently:
   - Content violation for active principals: `FrostAgent 错误：Request rejected by security inspector: 不合适的内容！`
   - Banned/locked principals at the gateway: `FrostAgent 错误：Request rejected by security gateway: 您已被封禁，请联系管理员。`
   In group chats, rejection notifications are sent only when the bot was explicitly addressed locally (via `@bot`, name wake word, or `GROUP_REPLY_ON_MENTION=false`), preventing unsolicited interruptions during unaddressed background conversations. The decision relies exclusively on ingress-local metadata and strictly avoids upstream platform RPCs (such as OneBot `get_msg` for quoted reply resolution) on behalf of rejected principals.

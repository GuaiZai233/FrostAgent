## 全局安全控制体系

FrostAgent 将安全控制收束在共享的 `security.Controller`，而不是把权限判断分散到各个工具、平台适配器或实例运行时。本体系为 FrostAgent 所有平台适配器（OneBot、AstrBot 以及未来的 Telegram 等）提供统一的 Global Access Control 与 Watchdog 安全基础设施。

- **主体身份规范化与 Transport 解耦**：可信身份始终由平台适配器统一归一化生成，主键为 `(canonical_platform, user_id)`。Adapter/Transport 协议名称（如 `onebot`、`aiocqhttp`、`cqhttp`）明确与用户平台身份解耦，统一映射为标准平台标识（如 `qq`），确保同一真实用户在不同协议传输接入下共享同一身份标识与安全审计边界。模型输入、工具参数与模型文本均不可声明或伪造身份。
- **全局访问状态与跨实例一致性**：`AccessStore` 将 `ACTIVE` / `LOCKED` 状态持久化到全局根数据目录（`BRAIN_PATH`），并使用跨进程文件锁和操作系统原子替换写入协调并发（Windows 平台采用带 `MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH` 的 `MoveFileExW`，POSIX 平台采用临时文件替换与父目录 `fsync`），杜绝因删除后重命名失败引发的 fail-open 风险。多实例共享全局唯一 Controller，任何实例或适配器发生的锁定操作立即对全量实例和全部适配器生效。
- **Watchdog 检查点与 Provenance 溯源**：Watchdog 覆盖直接用户入口（`USER_DIRECT`）、用户引用回复上下文（`USER_QUOTE_REPLY_CONTEXT`）、群聊摘要与近期群消息（`GROUP_CONTEXT`）、多模态视觉处理结果（`VISION_RESULT`）、平台元数据（群名、用户昵称、群名片等 `PLATFORM_METADATA`）、工具参数（`TOOL_ARGUMENT`）、工具返回结果（`TOOL_RESULT`）以及最终模型输出（`MODEL_OUTPUT`）全生命周期。所有非直接上下文在注入 LLM Prompt 前均必须经过独立 Checkpoint 审查。
- **内容阻断与用户处罚分级（低阻断门槛、高锁定门槛）**：
  - 非直接来源（引用消息、群摘要、群历史、视觉识别结果、平台元数据、工具结果、模型输出）命中危险规则时，仅执行内容剔除或隔离，绝不处罚当前触发请求的用户。
  - 直接用户输入的首次违规（包括模糊匹配与主动编码）仅阻断内容（`BLOCK`），记录阻断哈希与时间，strike 计数保持为 0，不施加用户级处罚。
  - 唯有在已有阻断历史的窗口期内发生明显重复提交或真正编码规避尝试时，才累计 `STRIKE`，并在达到阈值（3 次）后升级为全局锁定（`LOCK`）。
  - **精准规避判定与良性编码解耦**：规避证据（`isEvasion` / `encoded=true`）仅在归一化揭示了原始输入中不存在的危险规则（`!rawMatches && normMatches`），或通过归一化还原了此前被阻断的标准载荷哈希（`normHash == lastBlockedHash && rawHash != normHash`）时才被采信并计入惩罚；包含良性 URL 编码（如 `https://example.com/foo%20bar`）或良性字符但危险指令本就以明文出现的直接违规输入，仅做常规阻断，绝不滥记编码规避 Strike。
- **有界归一化与组合式规避解码（Compositional Normalization Pipeline）**：
  - Watchdog 设置明确的安全审查上限（256 KiB）。超出上限的超大输入直接 Fail-Closed 阻断，坚决杜绝静默截断放行导致的尾部走私注入。
  - 编码归一化采用容错百分号扫描器（`tolerantPercentUnescape`），按字节逐个解码有效的 `%[0-9a-fA-F]{2}` 序列，同时完整保留混杂的畸形转义（如 `%ZZ`、末尾悬空 `%`、未截断十六进制）和字面加号（`+`），彻底避免传统全有或全无解码器在遇到畸形字符时直接中断解码导致危险载荷漏检，并杜绝将 `C++` 或 `A+B` 误判为编码规避。
  - **组合式零宽与控制符多层规范化管道**：针对攻击者通过百分号编码或 Base64 嵌套零宽分隔符（如 `%E2%80%8B`、Unicode 双向嵌入与隔离控制符）实施的多层组合编码绕过，归一化流水线采用有界循环（最多 3 轮），在每一层百分号解码及 Base64 解码成功后立即重新应用不可见/零宽字符规范化消除（`stripZeroWidthAndControl`），彻底瓦解嵌套隐藏的零宽混淆指令，确保在进入下一层解码或正则规则匹配前完全还原规范化文本。
  - Base64 与零宽字符规避检测结合 UTF-8 与可打印字符验证，确保准确识别人工构造的规避载荷。
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
3. **Indirect Context Checkpoints**: Injected prompt context (quoted reply messages, group running summaries, and recent group history) is vetted via distinct Watchdog provenance sources (`USER_QUOTE_REPLY_CONTEXT`, `GROUP_CONTEXT`), isolating dangerous injection without penalizing the current caller.
4. **Graduated Enforcement**: Lower threshold for blocking content, higher threshold for penalizing users. Single fuzzy or encoded violations result in silent content blocking; only deliberate, repeated evasion attempts within the sliding window accrue strikes toward a permanent lock.
5. **Fail-Closed Atomic Persistence**: Windows `MoveFileExW` and POSIX atomic renames prevent corrupted or missing security state files from failing open under high concurrency.

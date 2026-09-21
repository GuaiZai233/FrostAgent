# ActionsCat 自动化平台集成设计文档 (ActionsCat Integration)

> 本文档系统阐述 FrostAgent 与 ActionsCat（自动化任务执行与调度平台）深度集成的系统架构、通信契约、Agent 工具集、安全控制边界及 Web 控制台设计。

---

## 一、设计背景与核心原则

### 1.1 背景与定位
ActionsCat 是独立的容器化/沙箱化自动化任务平台，支持多语言 Action 声明、版本管理、制品构建、定时与事件调度以及带特权能力的沙箱运行。
FrostAgent 作为对话智能体与自然语言中枢，通过与 ActionsCat 建立端到端集成链路，实现：
- **Agent 自主动作调用**：智能体在对话决策循环中能够动态发现、检查、触发并跟踪 ActionsCat 中部署的自动化 Action；
- **管理平面安全代理**：通过控制台与 HTTP 代理，为管理员提供 Action 元数据注册、手动触发、日志排查与事件测试能力；
- **事件反向驱动与双向闭环**：ActionsCat 任务沙箱在受控凭据下通过 `messages.Service`（`frostagent.sendmsg` 能力）将处理结果直接通知群聊或用户，形成“对话触发 -> 沙箱执行 -> 结果触达”的完整闭环。

### 1.2 核心设计原则

1. **最小权限与控制面鉴权对齐 (Control-Plane Auth Alignment)**：
   - ActionsCat HTTP 代理路由严格继承 FrostAgent 控制面统一鉴权标准（`MCP_CONTROL_TOKEN` / `ADMIN_TOKEN`），杜绝因代理暴露造成无认证凭据代持（Credentialed Proxy）。
   - 本地回环访问与远程访问严格隔离，支持 `MCP_ENFORCE_LOCAL_TOKEN=true` 强制本地鉴权。
2. **管理写权限严格门禁 (Management Mutation Gate)**：
   - 区分“动作执行（Run）”与“管理面修改（Create/Version/Build/Activate/Deploy）”。
   - 注册 Action、创建版本、触发构建、激活构建以及端到端部署均属于持久化管理面操作，Agent 工具端必须校验 `llm.RunContext` 并通过 `admincmd.IsAdmin` 限制仅配置在 `ADMIN_QQ_IDS` 中的管理员可用；非管理员与无上下文请求严格 Fail-Closed，产生 0 次后端网络调用。
3. **Mock 会话零持久化副作用 (Zero Durable Mutation Invariant)**：
   - 交互式模拟对话模式（`?mock=true`）下，任何具有外部持久化副作用的操作（`actionscat_run_action`、`actionscat_create_action`、`actionscat_create_version`、`actionscat_build_version`、`actionscat_activate_build`、`actionscat_deploy_action`）必须被前置阻断并返回说明，绝不向 ActionsCat 发起真实执行或写入，确保断电全丢不变量。
4. **构建超时与未决结果处理 (Build Timeout & Indeterminate Result Defense)**：
   - 区分普通 API 查询（`15s` 超时）与同步构建编译流程（`180s` 超时，`defaultBuildTimeout`）。底层 HTTP Client 显式禁用 transport 层自动重试与全局 client 级超时（`http.Client.Timeout = 0`），使用 context 传递 deadline。
   - 当同步构建请求发生 context 超时或传输中断时，严格禁止将其判定为“构建失败”，而是映射为 `ErrBuildTimeoutUnknownResult`（“构建结果未知”），提示 Agent 使用 `actionscat_get_build` 查询实际状态，防止状态机分歧与重复并发构建。
5. **构建状态门禁与版本不可变性 (Build Status Gating & Version Immutability)**：
   - 激活构建（`actionscat_activate_build`）严格前置校验构建状态，仅允许激活 `succeeded` 状态的制品构建，阻断未完成或失败构建的激活。
   - 严格遵循 ActionsCat 架构的“版本不可变（Version Immutability）”约束。若编译失败，工具提取截断的编译日志（`stderr`/`stdout`）供 Agent 排查，并明确指导 Agent 通过 `actionscat_create_version` 创建新版本，禁止对失败版本发起重复覆盖修改。
6. **敏感注入状态脱敏隔离 (Agent-Facing DTO Redaction)**：
   - ActionsCat 的执行上下文包含持久状态注入（`PlannedEnv`，可能携带 API Key、私有状态或运行时 Token）。
   - 向 LLM 暴露的 Agent DTO 明确采用白名单字段映射，彻底剔除 `planned_env` 等内部凭据，防止上下文污染与凭据泄漏。
7. **受保护运行时环境变量防御 (Protected Env Defense)**：
   - `actionscat_run_action` 严格禁止传入 `ACTIONSCAT_*` 前缀的环境变量，杜绝模型输出伪造 Action 运行身份或覆写受信任运行时标识。
8. **显式区分已启用 (Enabled) 与可运行 (Runnable)**：
   - 对齐 ActionsCat PR #3 制品模型（`Action -> ActionVersion -> ArtifactBuild -> SetActiveBuild -> Run`）。
   - 仅当 `Enabled == true && ActiveVersionID != "" && ActiveBuildID != ""` 时，Action 才判定为 `Runnable`。未关联激活构建的元数据壳在 Agent 工具中默认不可作为运行候选，执行失败时给出明确指引。

---

## 二、系统架构与数据流

```
                ┌──────────────────────────────────────────────────┐
                │          OneBot / AstrBot / Direct Chat          │
                └─────────────────────────┬────────────────────────┘
                                          │ 触发对话 / 协议事件
                                          ▼
┌───────────────────────────────────────────────────────────────────────────────────┐
│ FrostAgent Core Engine & Runtime                                                  │
│                                                                                   │
│  ┌──────────────────────┐    Tool Call    ┌────────────────────────────────────┐  │
│  │   LLM Agent Loop     │ ──────────────▶ │ internal/tools/actionscat_tools    │  │
│  │ (MaxIterations = 10) │                 │                                    │  │
│  └──────────────────────┘                 │ - actionscat_list_actions (DTO)    │  │
│                                           │ - actionscat_create_action (Admin) │  │
│                                           │ - actionscat_create_version(Admin) │  │
│                                           │ - actionscat_build_version (Admin) │  │
│                                           │ - actionscat_get_build             │  │
│                                           │ - actionscat_activate_build(Admin) │  │
│                                           │ - actionscat_deploy_action (Admin) │  │
│                                           │ - actionscat_run_action (Mock/Env) │  │
│                                           │ - actionscat_get_run (Redacted)    │  │
│                                           └─────────────────┬──────────────────┘  │
│                                                             │ Client Call         │
│                                                             ▼                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐  │
│  │ internal/actionscat/client.go (HTTP Client)                                 │  │
│  │ - 校验 ACTIONSCAT_ENDPOINT、管理/调度 Token                                  │  │
│  │ - 双超时体系: 默认查询 (15s) vs 同步构建 (180s, defaultBuildTimeout)         │  │
│  │ - http.Client.Timeout = 0 (context 控制)，禁用 transport 自动重试            │  │
│  │ - 构建超时精确映射为 ErrBuildTimeoutUnknownResult (避免误报失败)            │  │
│  │ - 请求体上限 (10MiB)、双重就绪探针 (Health & Auth)                           │  │
│  └──────────────────────────────────────┬──────────────────────────────────────┘  │
└─────────────────────────────────────────┼─────────────────────────────────────────┘
                                          │ HTTP (Bearer Token)
                                          ▼
┌───────────────────────────────────────────────────────────────────────────────────┐
│ ActionsCat Core Service (PR #3)                                                   │
│                                                                                   │
│  - /api/v1/actions                   : Action 元数据管理                           │
│  - /api/v1/actions/:id/versions      : 不可变版本声明 (Files/Build/RuntimeSpec)   │
│  - /api/v1/actions/:id/versions/:vid/builds : 触发制品编译构建 (go-builder)        │
│  - /api/v1/actions/:id/builds/:bid   : 构建状态与制品哈希查询                      │
│  - /api/v1/actions/:id/active-build  : 激活生效构建 (Status: succeeded 校验)       │
│  - /api/v1/actions/:id/runs          : 任务触发与执行生命周期                       │
│  - /api/v1/dispatch                  : 任意 JSON 业务事件分发                      │
│                                                                                   │
│               ┌──────────────────────────────────────────────────┐                │
│               │ Worker Sandbox (code-interpreter / Docker)       │                │
│               │  - network: none / 受限能力                       │                │
│               │  - SDK WriteState / Reply                        │                │
│               └─────────────────────────┬────────────────────────┘                │
└─────────────────────────────────────────┼─────────────────────────────────────────┘
                                          │ POST /api/v1/messages/send
                                          ▼
                               ┌──────────────────────┐
                               │ FrostAgent Dispatcher│ (出站消息触达群/用户)
                               └──────────────────────┘
```

---

## 三、核心模块与接口契约

### 3.1 客户端层（`internal/actionscat/client.go`）

封装与 ActionsCat 核心服务的底层交互：

```go
type Client struct {
    getenv     func(string) string
    httpClient HTTPClient
}

// 核心能力契约
func (c *Client) Health(ctx context.Context) (*HealthStatus, error)
func (c *Client) Status(ctx context.Context) StatusResponse
func (c *Client) ListActions(ctx context.Context) ([]Action, error)
func (c *Client) GetAction(ctx context.Context, actionID string) (*Action, error)
func (c *Client) CreateAction(ctx context.Context, req CreateActionReq) (*Action, error)
func (c *Client) CreateVersion(ctx context.Context, actionID string, req CreateVersionReq) (*ActionVersion, error)
func (c *Client) ListVersions(ctx context.Context, actionID string) ([]ActionVersion, error)
func (c *Client) GetVersion(ctx context.Context, actionID, versionID string) (*ActionVersion, error)
func (c *Client) BuildVersion(ctx context.Context, actionID, versionID string) (*ArtifactBuild, error)
func (c *Client) GetBuild(ctx context.Context, actionID, buildID string) (*ArtifactBuild, error)
func (c *Client) GetBuildLogs(ctx context.Context, actionID, buildID string) (*BuildLogs, error)
func (c *Client) ActivateBuild(ctx context.Context, actionID string, req SetActiveBuildReq) error
func (c *Client) TriggerRun(ctx context.Context, actionID string, req ManualRunReq) (*Run, error)
func (c *Client) TriggerRunAndWait(ctx context.Context, actionID string, req ManualRunReq, waitTimeout time.Duration) (*Run, error)
func (c *Client) ListRuns(ctx context.Context, actionID string, limit, offset int) ([]Run, error)
func (c *Client) GetRun(ctx context.Context, actionID, runID string) (*Run, error)
func (c *Client) GetRunLogs(ctx context.Context, actionID, runID string) (*RunLogs, error)
func (c *Client) Dispatch(ctx context.Context, event any) error
```

#### 双状态区分机制（Status vs Health）
- `Health(ctx)`：无鉴权探测 `/healthz`，仅验证服务端网络联通与容器进程存活；
- `Status(ctx)`：分层探测。第一步探测 `/healthz`（`healthy`），第二步携带 `ACTIONSCAT_MANAGEMENT_TOKEN` 发起最小验证查询 `/api/v1/actions?limit=1`（`authenticated`），避免将“未配 Token / 401 Unauthorized”误报为“服务就绪”。

#### 双超时与未决结果判定机制
- 标准查询默认超时 `15s`（`defaultTimeout`）；
- 同步编译构建超时设定为独立的长超时 `180s`（`defaultBuildTimeout`）；底层 `http.Client.Timeout = 0`，由各请求的 context deadline 精确控制，并禁用 HTTP transport 层对构建请求的自动重试；
- 构建超时精确包装为 `ErrBuildTimeoutUnknownResult`，阻断“假死当成编译失败”的状态误判。

### 3.2 控制面 HTTP 代理服务（`internal/service/actionscat/service.go`）

为前端 Web 控制台与管理工具提供统一的反向代理网关：
- **挂载路径**：
  - 实例作用域路由：`/instances/{id}/api/actionscat/*`
  - 全局默认别名路由：`/api/actionscat/*` 与 `/api/v1/actionscat/*`（由顶层 `managementMux` 转发）
- **鉴权集成**：复用 `mcpsvc.CheckControlPlaneAuthScoped`，基于请求来源 IP、Authorization Header 以及实例配置执行严格门禁。
- **Fail-Closed 请求防护**：
  - 采用 `http.MaxBytesReader` 限制请求体最大 10 MiB，抵御恶意大包；
  - 严格 JSON 解码（`DisallowUnknownFields` 与 EOF 探测），拒绝截断或格式错误的恶意请求，防止无效请求触发后端 Action。

### 3.3 智能体工具集（`internal/tools/actionscat_tools.go`）

向大模型 Agent Loop 暴露 9 个标准化工具，涵盖完整部署生命周期（元数据声明 -> 版本快照 -> 编译构建 -> 构建激活 -> 触发运行）：

#### 1. `actionscat_list_actions`
- **功能**：列出已注册的 Action 列表；
- **参数**：
  - `enabled_only: bool`（仅返回已启用动作）
  - `runnable_only: bool`（仅返回已关联有效构建、可立即执行的动作）
- **DTO 脱敏**：返回 `AgentActionDTO`，包含 `id`, `name`, `description`, `enabled`, `runnable`, `active_version_id`, `active_build_id`, `max_concurrency`。

#### 2. `actionscat_create_action` (Admin)
- **功能**：在 ActionsCat 中注册新的 Action 动作元数据壳；
- **权限门禁**：依赖 `llm.RunContext`，非管理员与 `Mock==true` 会话严格 Fail-Closed（0 次后端网络调用）；
- **参数**：`name: string` (必填), `description: string`, `max_concurrency: int`。

#### 3. `actionscat_create_version` (Admin)
- **功能**：为指定 Action 创建不可变源码版本快照；
- **权限门禁**：管理员权限门禁，`Mock==true` 拦截；
- **规约映射与防御**：
  - 文件总大小上限严格限制为 10 MiB；
  - 源码文件映射 (`files: map[string]string`, 必填)；
  - 健全默认值：默认构建命令 `go build -o /sandbox/out/entrypoint .`、入口 `/sandbox/entrypoint`、沙箱网络 `none`、超时 30 秒；
  - 完整规约支持：`build_spec` (language, toolchain, command, network), `runtime_spec` (entrypoint, network policy, timeout, memory, cpu), `state_injections`, `runtime_capabilities`。

#### 4. `actionscat_build_version` (Admin)
- **功能**：触发版本沙箱编译打包，生成制品构建（ArtifactBuild）；
- **超时处理**：采用 `180s` 独立构建超时；若超时返回清晰的“构建超时，构建结果未知”诊断说明及 `actionscat_get_build` 查询指引；
- **状态门禁与版本不可变性**：
  - 校验 `build.Status == "succeeded"`；
  - 若构建失败（`failed` / `timed_out`），自动提取并截断输出（4000 字符以内）编译日志（`stderr`/`stdout`）辅助 Agent 诊断；
  - 明确遵循**版本不可变（Version Immutability）**原则：提示 Agent 不可在原版本重试或修改，必须调用 `actionscat_create_version` 修复代码并创建新版本。

#### 5. `actionscat_get_build`
- **功能**：查询特定构建的任务状态、制品哈希、退出码及详细编译日志；
- **使用场景**：用于构建超时后的异步轮询确认，或开发排查阶段提取完整编译日志。

#### 6. `actionscat_activate_build` (Admin)
- **功能**：将成功构建（`succeeded`）激活为 Action 的生效版本，使其跃迁为 `runnable` 状态；
- **权限与状态门禁**：
  - 管理员权限门禁，`Mock==true` 拦截；
  - 激活前主动验证构建状态，拒绝激活非 `succeeded` 状态的构建。

#### 7. `actionscat_deploy_action` (Admin, All-in-One)
- **功能**：一站式复合部署工具，自动串联完整生命周期：
  `create_version -> build_version -> inspect status == succeeded -> activate_build`；
  执行成功后，Action 立即进入 `runnable` 状态，可直接通过 `actionscat_run_action` 执行；
- **权限门禁**：管理员权限门禁，`Mock==true` 拦截。

#### 8. `actionscat_run_action`
- **功能**：触发指定 Action 执行并支持同步等待结果（最长 25 秒）；
- **参数**：`action_id: string`（必填）, `extra_env: map[string]string`, `trigger_metadata: map[string]string`, `wait: bool`；
- **安全检查**：
  - `RunContext.Mock == true` 拦截：直接拒绝执行；
  - 前缀黑名单：任何以 `ACTIONSCAT_` 开头的 `extra_env` 键直接拒绝；
  - 友好报错诊断：检测到后端返回 `no active build` 时，向模型反馈清晰解释并指引部署流程。

#### 9. `actionscat_get_run`
- **功能**：查询指定运行任务的状态、耗时、退出码及 stdout/stderr 日志；
- **脱敏策略**：返回 `AgentRunDTO`，完全剔除 `PlannedEnv`。

---

## 四、安全与隔离边界

| 维度 | 安全威胁 | FrostAgent 防御机制与不变量 |
| :--- | :--- | :--- |
| **控制平面凭据** | 外部未授权调用 FrostAgent 代理，利用代理持有的 token 滥用 ActionsCat | 代理入口强制调用 `CheckControlPlaneAuthScoped`，非本地回环强制 Bearer 校验，支持本地强鉴权配置 |
| **管理面权限滥用** | 普通用户诱导 Agent 频繁调用管理工具（创建 Action/版本、构建、激活或部署）污染控制面 | 5 个状态变更工具全量接入 `ADMIN_QQ_IDS` 门禁；非管理员执行时直接返回权限不足，产生 0 次后端网络请求 |
| **模拟会话副作用** | `?mock=true` 调试会话调用执行或变更工具触发真实沙箱编译、写入与消息发送 | 读取 `RunContext.Mock`，为 true 时一律拦截所有变更与运行操作，严格维持“断电全丢”不变量 |
| **构建超时未决** | 耗时编译导致 HTTP 链接超时，被客户端误判为“失败”引发重复构建并发冲突 | `defaultBuildTimeout = 180s`，底层禁用 transport 自动重试；context 超时严格映射为 `ErrBuildTimeoutUnknownResult`，指导 Agent 使用 `get_build` 确认真实状态 |
| **构建状态与版本污染** | 激活未完成/失败的构建导致 Action 运行时崩溃；在失败版本上重试破坏不可变快照 | `activate_build` 与 `deploy_action` 严格断言 `build.Status == "succeeded"`；对失败构建输出截断日志并强制指引 Agent 通过 `create_version` 创建新版本 |
| **源码载荷溢出** | Agent 提交超大代码文件或递归目录耗尽内存与传输带宽 | 工具层计算源码映射总字节并硬限制为 10 MiB（`maxTotalFilesBytes`）；HTTP 代理层强制配置 `http.MaxBytesReader` 限制 10 MiB |
| **敏感状态泄露** | 沙箱运行中注入的持久状态（`planned_env`）进入大模型上下文导致数据泄露 | 单独定义 `AgentRunDTO`，严格白名单过滤输出字段，永远剔除 `PlannedEnv` |
| **身份与权限伪造** | 大模型或用户通过 `extra_env` 传递 `ACTIONSCAT_ACTION_ID` 伪造沙箱身份 | 工具层严格校验参数，禁止任何以 `ACTIONSCAT_` 开头的环境变量键 |
| **Malformed 请求** | 畸形 JSON 导致类型错位或以非预期空值意外触发任务 | HTTP 代理层使用 `http.MaxBytesReader`，JSON 解码错误直接 400 拦截 |
| **死循环与超时耗尽** | Action 长时间运行耗尽 Agent 单轮时间 | 设置最大同步轮询等待窗口（25s），超时平滑返回运行中状态与 run_id |

---

## 五、运行时配置与实例参数

所有 ActionsCat 相关配置项统一纳入 FrostAgent Settings 体系管理：

| 环境变量配置名 | 说明 | 默认值 | 作用域 | 敏感度 |
| :--- | :--- | :--- | :--- | :--- |
| `ACTIONSCAT_ENDPOINT` | ActionsCat 核心服务地址（如 `http://127.0.0.1:8080`） | 空（未配置） | 实例级 | 否 |
| `ACTIONSCAT_MANAGEMENT_TOKEN` | ActionsCat 管理 API Bearer Token | 空 | 实例级 | 是（脱敏显示） |
| `ACTIONSCAT_DISPATCH_TOKEN` | ActionsCat 事件分发 Token（可选，默认复用管理 Token） | 空 | 实例级 | 是（脱敏显示） |
| `AGENT_MAX_ITERATIONS` | 智能体单次处理允许的最大工具迭代轮数 | `10` | 实例级 | 否 |

---

## 六、Web 控制台管理界面（`apps/web/src/pages/actionscat.ts`）

前端控制台在左侧导航提供「ActionsCat」专属管理页面（`/#actionscat`），具备以下能力：

1. **服务连接与认证状态卡片**：
   - 实时检测 `configured`、`healthy` 与 `authenticated`；
   - 区分“健康检查未通过”、“管理凭据未认证”及“已连接并就绪”三种状态；
   - 提供端点一键复制、重新检查及跳转设置面板快捷按钮。
2. **Action 列表与可运行性标识**：
   - 区分展示 `可运行`（绿色徽章）与 `未构建/未激活`（黄色徽章）；
   - 展示版本 ID、构建 ID、并发数、定时表达式及能力声明列表；
   - 支持一键调起“手动执行”与“查看历史运行”操作。
3. **安全的手动触发执行对话框**：
   - 支持多行附加环境变量输入（自动忽略注释与空行）；
   - 支持自定义 JSON 格式触发上下文元数据校验；
   - 当针对未构建动作触发时，前端弹出醒目警示横幅提示。
4. **运行历史与实时日志查看器**：
   - 分页展示 Run 历史列表（触发类型、状态徽章、耗时、退出码、时间戳）；
   - 独立弹窗拉取并实时展示终端标准输出（STDOUT）与标准错误（STDERR），支持一键全量复制。
5. **事件分发测试工具**：
   - 快捷向 `/api/v1/dispatch` 发送 JSON Payload，便于调试自动化事件触发流。
6. **Action 元数据注册对话框**：
   - 允许管理员在控制台快捷创建 Action 名称、描述及最大并发数，文案明确提示其为元数据声明。

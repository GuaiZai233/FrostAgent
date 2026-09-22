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
4. **构建未决结果与幂等恢复机制 (Build Indeterminate Result & Idempotent Recovery)**：
   - 区分普通 API 查询（`15s` 超时）与同步构建编译流程（`180s` 超时，`defaultBuildTimeout`）。底层 HTTP Client 显式禁用 transport 层自动重试与全局 client 级超时（`http.Client.Timeout = 0`），使用 context 传递 deadline。
   - 当同步构建请求发生 context 超时、传输中断、HTTP 5xx 服务端错误（因 ActionsCat Core 先落盘持久化 Build 记录后执行沙箱编译）或 2xx 响应不可解析时，严格禁止将其判定为“构建失败”，而是映射为 `ErrBuildUnknownResult`（“构建结果未知”，对应 HTTP 504 Gateway Timeout），提示 Agent 使用 `actionscat_list_builds` 查询实际状态（包含初次为空时的短暂重试建议），防止状态机分歧与重复并发构建。
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
│                                           │ - actionscat_list_builds           │  │
│                                           │ - actionscat_activate_build(Admin) │  │
│                                           │ - actionscat_deploy_action (Admin) │  │
│                                           │ - actionscat_run_action (Mock/Env) │  │
│                                           │ - actionscat_get_run (Redacted)    │  │
│                                           │ - actionscat_create_schedule(Admin)│  │
│                                           │ - actionscat_list_schedules        │  │
│                                           │ - actionscat_delete_schedule(Admin)│  │
│                                           │ - actionscat_create_matcher (Admin)│  │
│                                           │ - actionscat_list_matchers         │  │
│                                           │ - actionscat_delete_matcher (Admin)│  │
│                                           └─────────────────┬──────────────────┘  │
│                                                             │ Client Call         │
│                                                             ▼                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐  │
│  │ internal/actionscat/client.go (HTTP Client)                                 │  │
│  │ - 校验 ACTIONSCAT_ENDPOINT、管理/调度 Token                                  │  │
│  │ - 双超时体系: 默认查询 (15s) vs 同步构建 (180s, defaultBuildTimeout)         │  │
│  │ - http.Client.Timeout = 0 (context 控制)，禁用 transport 自动重试            │  │
│  │ - 构建超时与传输中断精确映射为 ErrBuildUnknownResult (避免误报失败与重复构建)            │  │
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
│  - /api/v1/actions/:id/schedules     : Cron 定时调度规则注册与查询                 │
│  - /api/v1/schedules/:id             : 定时调度注销 (DELETE)                       │
│  - /api/v1/actions/:id/matchers      : 事件模式匹配规则注册与查询                  │
│  - /api/v1/matchers/:id              : 事件模式规则注销 (DELETE)                   │
│  - /api/v1/dispatch                  : 任意 JSON 业务事件分发                      │
│                                                                                   │
│  ┌───────────────────────────────┐     ┌───────────────────────────────────────┐  │
│  │ Background Cron Scheduler     │     │ Event Pattern Matcher Engine          │  │
│  │ (server.Scheduler.Start(ctx)) │     │ (exact / contains / regex)            │  │
│  └───────────────┬───────────────┘     └───────────────────┬───────────────────┘  │
│                  └───────────────────────┬─────────────────┘                      │
│                                          ▼ 自动拉起执行 (无 LLM 热路径介入)        │
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
func (c *Client) ListBuilds(ctx context.Context, actionID string) ([]ArtifactBuild, error)
func (c *Client) GetBuild(ctx context.Context, actionID, buildID string) (*ArtifactBuild, error)
func (c *Client) GetBuildLogs(ctx context.Context, actionID, buildID string) (*BuildLogs, error)
func (c *Client) ActivateBuild(ctx context.Context, actionID string, req SetActiveBuildReq) error
func (c *Client) TriggerRun(ctx context.Context, actionID string, req ManualRunReq) (*Run, error)
func (c *Client) TriggerRunAndWait(ctx context.Context, actionID string, req ManualRunReq, waitTimeout time.Duration) (*Run, error)
func (c *Client) ListRuns(ctx context.Context, actionID string, limit, offset int) ([]Run, error)
func (c *Client) GetRun(ctx context.Context, actionID, runID string) (*Run, error)
func (c *Client) GetRunLogs(ctx context.Context, actionID, runID string) (*RunLogs, error)
func (c *Client) ListSchedules(ctx context.Context, actionID string) ([]Schedule, error)
func (c *Client) CreateSchedule(ctx context.Context, actionID string, req CreateScheduleReq) (*Schedule, error)
func (c *Client) DeleteSchedule(ctx context.Context, scheduleID string) error
func (c *Client) ListMatchers(ctx context.Context, actionID string) ([]Matcher, error)
func (c *Client) CreateMatcher(ctx context.Context, actionID string, req CreateMatcherReq) (*Matcher, error)
func (c *Client) DeleteMatcher(ctx context.Context, matcherID string) error
func (c *Client) Dispatch(ctx context.Context, event any) error
```

#### 双状态区分机制（Status vs Health）
- `Health(ctx)`：无鉴权探测 `/healthz`，仅验证服务端网络联通与容器进程存活；
- `Status(ctx)`：分层探测。第一步探测 `/healthz`（`healthy`），第二步携带 `ACTIONSCAT_MANAGEMENT_TOKEN` 发起最小验证查询 `/api/v1/actions?limit=1`（`authenticated`），避免将“未配 Token / 401 Unauthorized”误报为“服务就绪”。

#### 双超时与未决结果判定机制
- 标准查询默认超时 `15s`（`defaultTimeout`）；
- 同步编译构建超时设定为独立的长超时 `180s`（`defaultBuildTimeout`）；底层 `http.Client.Timeout = 0`，由各请求的 context deadline 精确控制，并禁用 HTTP transport 层对构建请求的自动重试；
- 同步构建期间遭遇的任何传输中断（连接被重置、io.ErrUnexpectedEOF、响应体截断、超时）、HTTP 5xx 服务端错误以及 2xx 响应不可解析均统一包装为 `ErrBuildUnknownResult`，阻断误判并防止客户端重复并发提交构建。

### 3.2 控制面 HTTP 代理服务（`internal/service/actionscat/service.go`）

为前端 Web 控制台与管理工具提供统一的反向代理网关：
- **挂载路径**：
  - 实例作用域路由：`/instances/{id}/api/actionscat/*`
  - 全局默认别名路由：`/api/actionscat/*` 与 `/api/v1/actionscat/*`（由顶层 `managementMux` 转发）
- **触发平面代理端点**：
  - `GET /actions/:id/schedules`：查询指定 Action 的定时调度规则列表；
  - `POST /actions/:id/schedules`：注册 Action 定时调度规则（严格校验 `cron_expr` 非空）；
  - `DELETE /schedules/:id`：永久注销指定调度；
  - `GET /actions/:id/matchers`：查询指定 Action 的事件模式匹配规则列表；
  - `POST /actions/:id/matchers`：注册事件模式规则（严格校验 `name` 与 `pattern` 非空）；
  - `DELETE /matchers/:id`：永久注销指定事件规则。
- **鉴权集成**：复用 `mcpsvc.CheckControlPlaneAuthScoped`，基于请求来源 IP、Authorization Header 以及实例配置执行严格门禁。
- **Fail-Closed 请求防护**：
  - 采用 `http.MaxBytesReader` 限制请求体最大 10 MiB（创建版本源码包）/ 1 MiB（调度与规则元数据），抵御恶意大包；
  - 严格 JSON 解码（`dec.Decode(&trailing) == io.EOF` 严格防尾随多余 Token 校验），拒绝截断或格式错误的恶意请求，防止无效请求触发后端 Action。

### 3.3 智能体工具集（`internal/tools/actionscat_tools.go`）

向大模型 Agent Loop 暴露 16 个标准化工具，涵盖完整生命周期（元数据声明 -> 版本快照 -> 编译构建 -> 构建激活 -> 触发运行 -> 定时调度 -> 事件匹配）：

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
  - 健全默认值与契约防御：默认构建命令 `go build -o /sandbox/out/entrypoint .`、标准入口 `entrypoint`（锁定 canonical 入口，兼容 `/sandbox/entrypoint` 等路径别名，阻断自定义相对路径）、开发语言限定为 `go`、沙箱网络 `none`、超时 30 秒；
  - 完整规约支持：`build_spec` (language: go, toolchain, command, network), `runtime_spec` (entrypoint: entrypoint, network policy: none/public/allowlist/isolated, timeout, memory, cpu), `state_injections`, `runtime_capabilities`。

#### 4. `actionscat_build_version` (Admin)
- **功能**：触发版本沙箱编译打包，生成制品构建（ArtifactBuild）；
- **未知结果与超时处理**：采用 `180s` 独立构建超时；若超时或传输中断返回保守的“构建结果未知”诊断说明，并指示 Agent 使用 `actionscat_list_builds` 查询后台是否已生成构建，严禁盲目重试；
- **状态门禁与版本不可变性**：
  - 校验 `build.Status == "succeeded"`；
  - 若构建失败（`failed` / `timed_out`），自动提取并截断输出（4000 字符以内）编译日志（`stderr`/`stdout`）辅助 Agent 诊断；
  - 明确遵循**版本不可变（Version Immutability）**原则：提示 Agent 不可在原版本重试或修改，必须调用 `actionscat_create_version` 修复代码并创建新版本。

#### 5. `actionscat_get_build`
- **功能**：查询特定构建的任务状态、制品哈希、退出码及详细编译日志；
- **使用场景**：在已知 `build_id` 时提取完整编译日志或确认构建终态。

#### 6. `actionscat_list_builds`
- **功能**：列出 Action 的历史构建记录（按创建时间倒序），支持通过 `version_id` 过滤；
- **使用场景**：当 `actionscat_build_version` 或 `actionscat_deploy_action` 遭遇超时或传输中断时，客户端无 `build_id`。调用本工具可发现该版本是否在后端成功生成构建，避免重复触发构建。

#### 7. `actionscat_activate_build` (Admin)
- **功能**：将成功构建（`succeeded`）激活为 Action 的生效版本，使其跃迁为 `runnable` 状态；
- **权限与状态门禁**：
  - 管理员权限门禁，`Mock==true` 拦截；
  - 激活前主动验证构建状态，拒绝激活非 `succeeded` 状态的构建。

#### 8. `actionscat_deploy_action` (Admin, All-in-One)
- **功能**：一站式复合部署工具，自动串联完整生命周期：
  `create_version -> build_version -> inspect status == succeeded -> activate_build`；
  执行成功后，Action 立即进入 `runnable` 状态，可直接通过 `actionscat_run_action` 执行；
- **权限门禁**：管理员权限门禁，`Mock==true` 拦截。

#### 9. `actionscat_run_action`
- **功能**：触发指定 Action 执行并支持同步等待结果（最长 25 秒）；
- **参数**：`action_id: string`（必填）, `extra_env: map[string]string`, `trigger_metadata: map[string]string`, `wait: bool`；
- **安全检查**：
  - `RunContext.Mock == true` 拦截：直接拒绝执行；
  - 前缀黑名单：任何以 `ACTIONSCAT_` 开头的 `extra_env` 键直接拒绝；
  - 友好报错诊断：检测到后端返回 `no active build` 时，向模型反馈清晰解释并指引部署流程。

#### 10. `actionscat_get_run`
- **功能**：查询指定运行任务的状态、耗时、退出码及 stdout/stderr 日志；
- **脱敏策略**：返回 `AgentRunDTO`，完全剔除 `PlannedEnv`。

#### 11. `actionscat_create_schedule` (Admin)
- **功能**：为指定 Action 注册基于 Cron 表达式的后台定时触发器；
- **权限门禁**：依赖 `llm.RunContext`，非管理员与 `Mock==true` 会话严格 Fail-Closed（0 次后端调用）；
- **参数**：
  - `action_id: string`（必填，目标 Action ID）
  - `cron_expr: string`（必填，标准 5 段式 Cron 表达式，如 `0 9 * * *` 或 `*/10 * * * *`）
  - `timezone: string`（可选，IANA 时区标识，默认 `Asia/Shanghai`）
  - `enabled: bool`（可选，默认 true）
- **契约保证**：注册成功后由 ActionsCat 后台常驻调度器自主调度，脱离 LLM 在后台周期运行。

#### 12. `actionscat_list_schedules`
- **功能**：查询指定 Action 绑定的所有定时调度规则；
- **参数**：`action_id: string`（必填）；
- **输出**：返回调度列表，包含调度 ID、Cron 表达式、时区、下次计算执行时间（`next_run_at`）、上次执行时间及启用状态。

#### 13. `actionscat_delete_schedule` (Admin)
- **功能**：永久注销指定的定时调度规则；
- **权限门禁**：依赖 `llm.RunContext`，非管理员与 `Mock==true` 会话严格 Fail-Closed（0 次后端调用）；
- **参数**：`schedule_id: string`（必填，待注销的调度 ID）。

#### 14. `actionscat_create_matcher` (Admin)
- **功能**：为指定 Action 注册基于消息/事件模式匹配的触发规则；
- **权限门禁**：管理员权限门禁，`Mock==true` 拦截；
- **参数**：
  - `action_id: string`（必填，目标 Action ID）
  - `name: string`（必填，规则名称）
  - `match_type: string`（必填，匹配方式：`contains` / `exact` / `regex`）
  - `pattern: string`（必填，匹配表达式，regex 模式必须符合 Go/RE2 正则语法）
  - `target_field: string`（可选，事件 JSON 字段路径，默认 `text`）
  - `capture_env_map: map[string]string`（可选，正则命名捕获组映射为容器环境变量名）
  - `priority: int`（可选，评估优先级，数值越高越先匹配，默认 0）
  - `continue_matching: bool`（可选，匹配命中后是否允许后续规则继续匹配，默认 false）
  - `enabled: bool`（可选，默认 true）
- **安全检查**：前置校验 `capture_env_map` 中的环境变量名称，严禁使用 `ACTIONSCAT_` 前缀，防止覆盖运行时安全凭据。

#### 15. `actionscat_list_matchers`
- **功能**：查询指定 Action 绑定的所有事件模式规则；
- **参数**：`action_id: string`（必填）；
- **输出**：返回规则列表，包含规则 ID、名称、匹配类型、表达式、目标字段、捕获映射、优先级及启用状态。

#### 16. `actionscat_delete_matcher` (Admin)
- **功能**：永久注销指定的事件模式匹配规则；
- **权限门禁**：依赖 `llm.RunContext`，非管理员与 `Mock==true` 会话严格 Fail-Closed（0 次后端调用）；
- **参数**：`matcher_id: string`（必填，待注销的规则 ID）。

---

### 3.4 触发平面架构与自主运行机制 (Trigger Plane & Autonomous Execution)

触发平面（Trigger Plane）是 ActionsCat 区别于纯交互式沙箱的核心系统能力。它包含 **定时调度器 (Schedule Trigger)** 与 **事件模式引擎 (Matcher Trigger)** 两个子系统：

1. **后台常驻定时调度器 (Background Cron Scheduler)**：
   - 调度器作为常驻后台 Goroutine（`server.Scheduler.Start(ctx)`）独立运行，使用一分钟粒度的 Tick 进行轮询；
   - 支持标准 5 段式（分 时 日 月 周）Cron 表达式以及完整 IANA 时区（如 `Asia/Shanghai`, `UTC`, `America/New_York`），由服务端准确计算 `next_run_at`；
   - **零 LLM 热路径开销**：定时触发直接在 ActionsCat 内部调度执行，任务完成时由沙箱内的 SDK `client.Reply(...)` 经由 `messages.Service` 触达指定群聊/用户。整个触发与执行链条无需大模型参与，彻底消除了定期巡检的 Token 消耗、并发占用与大模型不确定性。

2. **事件模式匹配引擎 (Event Matcher Engine)**：
   - ActionsCat 监听来自 IM 平台或外部系统的通用 JSON 事件（`/api/v1/dispatch`）；
   - 规则匹配支持三种模式：
     - `contains`：目标字段包含子串；
     - `exact`：目标字段与规则全量字符一致；
     - `regex`：标准正则匹配，支持命名捕获组 `(?P<group_name>...)`；
   - **动态参数捕获与环境注入**：通过 `capture_env_map`，匹配引擎可从用户消息中提取关键参数（例如 `(?P<city>\S+)` -> `CITY=city`）并直接注入为任务运行的容器环境变量，使同一 Action 能够按不同输入弹性执行；
   - **优先级与匹配短路**：支持按 `priority` 倒序评估，默认命中后阻断后续规则（`continue_matching == false`），支持多规则流水线。

---

## 四、安全与隔离边界

| 维度 | 安全威胁 | FrostAgent 防御机制与不变量 |
| :--- | :--- | :--- |
| **控制平面凭据** | 外部未授权调用 FrostAgent 代理，利用代理持有的 token 滥用 ActionsCat | 代理入口强制调用 `CheckControlPlaneAuthScoped`，非本地回环强制 Bearer 校验，支持本地强鉴权配置 |
| **管理面权限滥用** | 普通用户诱导 Agent 频繁调用管理工具（创建 Action/版本/调度/规则、构建、激活或部署）污染控制面 | 9 个状态变更工具（包含 Schedule/Matcher 创建与删除）全量接入 `ADMIN_QQ_IDS` 门禁；非管理员执行时直接返回权限不足，产生 0 次后端网络请求 |
| **模拟会话副作用** | `?mock=true` 调试会话调用执行、变更或触发器工具触发真实沙箱编译、写入、定时触发或消息发送 | 读取 `RunContext.Mock`，为 true 时一律拦截所有变更、运行与触发器注册操作，严格维持“断电全丢”不变量 |
| **构建未决结果风险** | 耗时编译超时、服务端 5xx、响应解析异常或传输中断，被客户端误判为“失败”引发重复并发构建 | `defaultBuildTimeout = 180s`，底层禁用 transport 自动重试；任何传输中断、服务端 5xx、响应损坏与超时统一包装为 `ErrBuildUnknownResult`，指导 Agent 先通过 `list_builds` 确认状态后再决定是否激活，防止重复并发编译 |
| **构建状态与版本污染** | 激活未完成/失败的构建导致 Action 运行时崩溃；在失败版本上重试破坏不可变快照 | `activate_build` 与 `deploy_action` 严格断言 `build.Status == "succeeded"`；对失败构建输出截断日志并强制指引 Agent 通过 `create_version` 创建新版本 |
| **触发器变量伪造与越权** | 通过事件模式匹配的 `capture_env_map` 注入 `ACTIONSCAT_*` 系统级凭据，覆盖容器运行时回调端点或授权 Token | 工具层在注册 Matcher 时执行前置黑名单校验，任何以 `ACTIONSCAT_` 为前缀的目标环境变量均被直接拒绝 |
| **正则拒绝服务 (ReDoS)** | 注册灾难性回溯的畸形正则表达式导致匹配引擎 CPU 耗尽 | Go 语言标准库 `regexp` 基于 RE2 线性自动机实现，天生免疫回溯死循环；代理与工具层严格校验正则语法编译合法性 |
| **源码载荷溢出** | Agent 提交超大代码文件或递归目录耗尽内存与传输带宽 | 工具层计算源码映射总字节并硬限制为 10 MiB（`maxTotalFilesBytes`）；HTTP 代理层强制配置 `http.MaxBytesReader` 限制 10 MiB |
| **敏感状态泄露** | 沙箱运行中注入的持久状态（`planned_env`）进入大模型上下文导致数据泄露 | 单独定义 `AgentRunDTO`，严格白名单过滤输出字段，永远剔除 `PlannedEnv` |
| **身份与权限伪造** | 大模型或用户通过 `extra_env` 传递 `ACTIONSCAT_ACTION_ID` 伪造沙箱身份 | 工具层严格校验参数，禁止任何以 `ACTIONSCAT_` 开头的环境变量键 |
| **Malformed 请求** | 畸形 JSON 导致类型错位或以非预期空值意外触发任务 | HTTP 代理层使用 `http.MaxBytesReader`，JSON 解码错误直接 400 拦截；严格校验防尾随多余 JSON Tokens |
| **死循环与超时耗尽** | Action 长时间运行耗尽 Agent 单轮时间 | 设置最大同步轮询等待窗口（25s），超时平滑返回运行中状态与 run_id |

---

## 五、运行时配置与实例参数

所有 ActionsCat 相关配置项统一纳入 FrostAgent Settings 体系管理：

| 环境变量配置名 | 说明 | 默认值 | 作用域 | 敏感度 |
| :--- | :--- | :--- | :--- | :--- |
| `ACTIONSCAT_ENDPOINT` | ActionsCat 核心服务地址（如 `http://127.0.0.1:8080`） | 空（未配置） | 实例级 | 否 |
| `ACTIONSCAT_MANAGEMENT_TOKEN` | ActionsCat 管理 API Bearer Token | 空 | 实例级 | 是（脱敏显示） |
| `ACTIONSCAT_DISPATCH_TOKEN` | ActionsCat 事件分发 Token（可选，默认复用管理 Token） | 空 | 实例级 | 是（脱敏显示） |
| `SANDBOX_BASE_URL` / `FA_SANDBOX_ENDPOINT` | 兼容 Sandbox Gateway 服务地址（如 `http://127.0.0.1:3874`） | `http://127.0.0.1:3874` | 全局/实例级 | 否 |
| `SANDBOX_AUTH_TOKEN` / `FA_SANDBOX_AUTH_TOKEN` | Sandbox Gateway X-Auth-Token 凭据 | 空 | 全局/实例级 | 是（脱敏显示） |
| `SANDBOX_ENABLED` | 是否启用沙箱命令执行与隔离构建 | `false` | 全局/实例级 | 否 |
| `SANDBOX_SESSION_NAMESPACE` | 沙箱会话命名空间隔离前缀 | `frostagent` | 全局/实例级 | 否 |
| `AGENT_MAX_ITERATIONS` | 智能体单次处理允许的最大工具迭代轮数 | `10` | 实例级 | 否 |

---

## 六、Web 控制台管理界面（`apps/web/src/pages/actionscat.ts`）

前端控制台在左侧导航提供「ActionsCat」专属管理页面（`/#actionscat`），具备以下能力：

1. **服务连接与认证状态卡片**：
   - 实时检测 `configured`、`healthy` 与 `authenticated`；
   - 区分“健康检查未通过”、“管理凭据未认证”及“已连接并就绪”三种状态；
   - 提供端点一键复制、重新检查及跳转设置面板快捷按钮；
   - **内置 Sandbox Gateway 就绪诊断横幅**：自动探测关联的沙箱网关，实时呈现网关健康度、凭据有效性、协议兼容性以及 `go-builder` / `action-runtime` profile 支持矩阵。
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
7. **触发器与调度规则管理对话框 (Triggers & Schedules Dialog)**：
   - 每个 Action 卡片提供「触发器」配置入口；
   - **定时调度管理**：以表格方式清晰呈现当前 Cron 表达式、时区、下次计算执行时间（`next_run_at`）及启用状态；支持一键注销调度，并提供便捷的「新增定时调度」表单（校验 Cron 表达式、时区与启用开关）；
   - **事件模式匹配管理**：实时拉取事件模式规则列表，展示匹配类型（contains / exact / regex）、Pattern 表达式、目标字段、命名捕获组映射与优先级；支持一键注销规则，并提供「新增事件匹配规则」表单（支持 regex / contains / exact 选择、正则语法校验及 `capture_env_map` 解析与受保护前缀防御）。

---

## 七、沙箱网关契约与隔离规范 (Sandbox Gateway Contract & Isolation Specification)

### 7.1 问题背景与核心挑战
ActionsCat 的设计初衷是将 Agent 编写的自动化任务在强隔离沙箱环境中安全编译并执行。然而，现有的公开版 `Foxerine/code-interpreter` 网关仅适用于单一 Python 交互式解释器场景：
1. **API 契约缺失**：公开版网关未提供会话分配与生命周期管理接口（`POST /api/v1/sessions` 直接返回 HTTP 404）；
2. **Profile 体系缺失**：缺乏专为 Go 编译优化的 `go-builder` 环境和运行隔离的 `action-runtime` 环境；
3. **网络与回调隔离断裂**：不支持在 `network: none` 策略下保留宿主机/内部回调通信（`ACTIONSCAT_RUNTIME_ENDPOINT`），导致真实执行链条中断。

### 7.2 架构隔离与安全边界保证 (Architectural Invariants & Safety Bounds)
FrostAgent 与 ActionsCat 严格恪守核心架构安全不变量：
```text
SandboxBackend -> external isolated worker
no LocalBackend / HostBackend
fail closed / no local fallback
```
- **绝对杜绝宿主机直跑不可信代码**：不可信 Action 代码与大模型生成命令绝不直接以宿主机 `os/exec` 进程方式执行，坚决不向不可信代码暴露宿主机操作系统根目录、进程命名空间、同机器网络服务以及宿主环境变量（避免泄露 API Token、数据库凭据或系统机密）；
- **真实容器/微虚机隔离底座**：沙箱后端必须由具备真实强隔离能力的底层运行时（如 Docker、containerd、Firecracker、nsjail、bwrap 等）承载，必须满足：
  1. **独立文件系统根目录**：文件操作严格限制在容器/chroot/命名空间内，杜绝路径逃逸；
  2. **独立进程命名空间与低权限用户**：以独立非 root 用户运行，杜绝访问宿主进程树；
  3. **独立网络命名空间**：`network: none` 必须由内核网络命名空间或防火墙强行阻断所有出站原始套接字/TCP/UDP，而非仅靠代理环境变量；
  4. **严格的环境变量白名单**：从空环境变量构建运行上下文，只注入显式 Session 环境变量与必要 Runtime 基线，绝不继承网关宿主 `os.Environ()`；
  5. **资源配额硬限制**：通过 cgroups/Job Objects 强制实施内存上限（`MemoryLimitMB`）与 CPU 配额（`CPULimit`）；
  6. **确定性会话生命周期与清理**：会话超时或执行 `Release` 时，必须彻底终止该会话的整个进程树/容器并回收物理工作区。

### 7.3 兼容沙箱网关契约规范 (Gateway Contract Specification v1.0)

兼容沙箱网关必须实现下列核心 HTTP 接口：

| HTTP 方法与路径 | 授权要求 | 功能描述 | 请求与响应规范 |
| :--- | :--- | :--- | :--- |
| `GET /api/v1/status` | Header `X-Auth-Token` (若配置) | 探活网关并列举支持的 profiles | 响应：`{"status":"ok","supported_profiles":["go-builder","action-runtime","minimal"]}` |
| `POST /api/v1/sessions` | Header `X-Auth-Token` | 分配隔离工作区会话并绑定策略 | 请求：`{"user_uuid":"...","profile":"go-builder","network":"none","runtime_callback_url":"...","env":{...}}`<br>响应：`{"user_uuid":"...","profile":"...","status":"ready"}` |
| `POST /api/v1/shell/exec?user_uuid=...` | Header `X-Auth-Token` | 在指定 session 隔离容器中执行命令 | Query: `user_uuid`<br>请求：`{"command":"...","cwd":"/sandbox","timeout":60.0}`<br>响应：`{"stdout":"...","stderr":"...","exit_code":0,"timed_out":false}` |
| `POST /api/v1/release?user_uuid=...` | Header `X-Auth-Token` | 释放容器并物理清理 session 工作区 | Query: `user_uuid`<br>响应：成功返回 HTTP 204 No Content，若会话不存在则返回 HTTP 404 |

### 7.4 四态就绪诊断体系 (4-Way Readiness Diagnostics)

为彻底解决“黑盒 404”与“配置错误难定位”问题，FrostAgent 引入了系统化的四态就绪诊断探测（`internal/sandbox/readiness.go`），将沙箱网关连接结果精确归类为：

```
                              ┌───────────────────────┐
                              │ CheckReadiness Probe  │
                              └──────────┬────────────┘
                                         │
                    ┌────────────────────┴────────────────────┐
                    ▼                                         ▼
            [TCP / HTTP Dial]                         [HTTP Status Code]
                    │                                         │
         Dial / Timeout Error?                        ┌───────┴───────┐
         ├── YES ──▶ StatusEndpointUnreachable        │               │
         └── NO                                    401 / 403?        404 on /sessions?
                                                      ├── YES ──▶ StatusAuthFailure
                                                      └── NO          ├── YES ──▶ StatusAPIContractMissing
                                                                      └── NO
                                                                          Profiles Supported?
                                                                          ├── NO  ──▶ StatusProfileUnsupported
                                                                          └── YES ──▶ StatusReady
```

1. **`endpoint_unreachable` (端点不可达)**：网络拒绝连接、端口未监听、DNS 解析失败或连接超时；
2. **`auth_failure` (认证失败)**：网关返回 HTTP 401 Unauthorized 或 403 Forbidden，说明 `SANDBOX_AUTH_TOKEN` / `FA_SANDBOX_AUTH_TOKEN` 不匹配；
3. **`api_contract_missing` (协议契约缺失)**：网关虽健康响应 `/api/v1/status`，但对 `/api/v1/sessions` 返回 404 Not Found（明确指出当前网关为不兼容的旧版或未实现会话契约的 code-interpreter 镜像）；
4. **`profile_unsupported` (Profile 不支持)**：网关拒绝了请求的构建或运行配置文件（例如缺少 `go-builder` 或 `action-runtime`）；
5. **`ready` (完全就绪)**：网关服务连通、认证校验通过、会话契约完备且所有必须的 Profile 均已就绪。

诊断探针通过 `codeinterpreter.Client.Diagnose()` 与 `actionscat.Client.CheckSandboxReadiness()` 暴露，并在 Web 前端（`apps/web/src/pages/actionscat.ts`）提供直观的诊断看板与错误排查指引。

### 7.5 配置与接入指引

在 `.env` 中配置外部兼容沙箱网关：
```env
SANDBOX_ENABLED=true
FA_SANDBOX_ENDPOINT=http://127.0.0.1:3874
FA_SANDBOX_AUTH_TOKEN=my-secret-sandbox-token
SANDBOX_SESSION_NAMESPACE=frostagent
```

在生产环境中，请确保所连接的 Sandbox Gateway 启用了容器或微虚机后端（例如配置了具备 Go 工具链支持与会话路由的 code-interpreter worker 集群），并设置非空鉴权 Token。FrostAgent 在启动和诊断时会自动执行四态探测，确保环境安全可靠后方允许执行构建与任务调用。


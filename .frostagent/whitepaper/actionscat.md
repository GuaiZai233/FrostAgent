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
   - 区分“动作执行（Run）”与“管理面修改（Create/Config）”。
   - 注册/创建 Action 属于持久化管理面操作，Agent 工具端必须校验 `llm.RunContext` 并通过 `admincmd.IsAdmin` 限制仅配置在 `ADMIN_QQ_IDS` 中的管理员可用；非管理员与无上下文请求严格 Fail-Closed，产生 0 次后端网络调用。
3. **Mock 会话零持久化副作用 (Zero Durable Mutation Invariant)**：
   - 交互式模拟对话模式（`?mock=true`）下，任何具有外部持久化副作用的操作（`actionscat_run_action`、`actionscat_create_action`）必须被前置阻断并返回说明，绝不向 ActionsCat 发起真实执行或写入，确保断电全丢不变量。
4. **敏感注入状态脱敏隔离 (Agent-Facing DTO Redaction)**：
   - ActionsCat 的执行上下文包含持久状态注入（`PlannedEnv`，可能携带 API Key、私有状态或运行时 Token）。
   - 向 LLM 暴露的 Agent DTO 明确采用白名单字段映射，彻底剔除 `planned_env` 等内部凭据，防止上下文污染与凭据泄漏。
5. **受保护运行时环境变量防御 (Protected Env Defense)**：
   - `actionscat_run_action` 严格禁止传入 `ACTIONSCAT_*` 前缀的环境变量，杜绝模型输出伪造 Action 运行身份或覆写受信任运行时标识。
6. **显式区分已启用 (Enabled) 与可运行 (Runnable)**：
   - 对齐 ActionsCat PR #3 制品模型（`Action -> ActionVersion -> ArtifactBuild -> Run`）。
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
│                                           │ - actionscat_run_action (Mock/Env) │  │
│                                           │ - actionscat_get_run (Redacted)    │  │
│                                           │ - actionscat_create_action (Admin) │  │
│                                           └─────────────────┬──────────────────┘  │
│                                                             │ Client Call         │
│                                                             ▼                     │
│  ┌─────────────────────────────────────────────────────────────────────────────┐  │
│  │ internal/actionscat/client.go (HTTP Client)                                 │  │
│  │ - 校验 ACTIONSCAT_ENDPOINT、管理/调度 Token                                  │  │
│  │ - 统一超时 (15s)、请求体上限 (10MiB)、双重就绪探针 (Health & Auth)            │  │
│  └──────────────────────────────────────┬──────────────────────────────────────┘  │
└─────────────────────────────────────────┼─────────────────────────────────────────┘
                                          │ HTTP (Bearer Token)
                                          ▼
┌───────────────────────────────────────────────────────────────────────────────────┐
│ ActionsCat Core Service (PR #3)                                                   │
│                                                                                   │
│  - /api/v1/actions            : Action 元数据管理                                   │
│  - /api/v1/actions/:id/runs   : 任务触发与执行生命周期                               │
│  - /api/v1/dispatch           : 任意 JSON 业务事件分发                              │
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
func (c *Client) TriggerRun(ctx context.Context, actionID string, req ManualRunReq) (*Run, error)
func (c *Client) TriggerRunAndWait(ctx context.Context, actionID string, req ManualRunReq, waitTimeout time.Duration) (*Run, error)
func (c *Client) GetRun(ctx context.Context, actionID, runID string) (*Run, error)
func (c *Client) GetRunLogs(ctx context.Context, actionID, runID string) (*RunLogs, error)
func (c *Client) Dispatch(ctx context.Context, event any) error
```

#### 双状态区分机制（Status vs Health）
- `Health(ctx)`：无鉴权探测 `/healthz`，仅验证服务端网络联通与容器进程存活；
- `Status(ctx)`：分层探测。第一步探测 `/healthz`（`healthy`），第二步携带 `ACTIONSCAT_MANAGEMENT_TOKEN` 发起最小验证查询 `/api/v1/actions?limit=1`（`authenticated`），避免将“未配 Token / 401 Unauthorized”误报为“服务就绪”。

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

向大模型 Agent Loop 暴露四个标准化工具：

#### 1. `actionscat_list_actions`
- **功能**：列出已注册的 Action 列表；
- **参数**：
  - `enabled_only: bool`（仅返回已启用动作）
  - `runnable_only: bool`（仅返回已关联有效构建、可立即执行的动作）
- **DTO 脱敏**：返回 `AgentActionDTO`，包含 `id`, `name`, `description`, `enabled`, `runnable`, `active_version_id`, `active_build_id`, `max_concurrency`。

#### 2. `actionscat_run_action`
- **功能**：触发指定 Action 执行并支持同步等待结果（最长 25 秒）；
- **参数**：
  - `action_id: string`（必填）
  - `extra_env: map[string]string`（自定义额外环境变量）
  - `trigger_metadata: map[string]string`（触发上下文元数据）
  - `wait: bool`（是否同步等待执行结果，默认 true）
- **安全检查**：
  - `RunContext.Mock == true` 拦截：直接拒绝执行；
  - 前缀黑名单：任何以 `ACTIONSCAT_` 开头的 `extra_env` 键直接拒绝；
  - 友好报错诊断：检测到后端返回 `no active build` 时，向模型反馈清晰解释与指引。

#### 3. `actionscat_get_run`
- **功能**：查询指定运行任务的状态、耗时、退出码及 stdout/stderr 日志；
- **脱敏策略**：返回 `AgentRunDTO`，完全剔除 `PlannedEnv`。

#### 4. `actionscat_create_action`
- **功能**：在 ActionsCat 中注册新的 Action 动作元数据声明；
- **权限门禁**：
  - 依赖调用上下文：必须存在 `llm.RunContext`；
  - Mock 拦截：模拟会话禁止创建；
  - 管理员鉴权：必须通过 `admincmd.IsAdmin(runContext.ActorUserID, scopes...)` 验证，仅限 `ADMIN_QQ_IDS` 用户使用；
  - 状态声明：返回说明告知新动作为元数据壳，后续需完成构建激活方可执行。

---

## 四、安全与隔离边界

| 维度 | 安全威胁 | FrostAgent 防御机制与不变量 |
| :--- | :--- | :--- |
| **控制平面凭据** | 外部未授权调用 FrostAgent 代理，利用代理持有的 token 滥用 ActionsCat | 代理入口强制调用 `CheckControlPlaneAuthScoped`，非本地回环强制 Bearer 校验，支持本地强鉴权配置 |
| **管理面权限滥用** | 普通聊天用户利用 Prompt 诱导 Agent 频繁调用 `create_action` 污染控制面 | 接入 `ADMIN_QQ_IDS` 门禁；非管理员执行时直接返回权限不足，0 次后端网络请求 |
| **模拟会话副作用** | `?mock=true` 调试会话调用 `run_action` 触发真实沙箱与消息发送 | 读取 `RunContext.Mock`，为 true 时一律拒绝执行，维持“断电全丢”不变量 |
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

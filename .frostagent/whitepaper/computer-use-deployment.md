# Computer Use：专用 Windows VM 部署与操作

本功能将一台操作者提供的专用 Windows VM 委托给一个 Bot。FrostAgent 运行模型与任务编排，
通过带认证的私网 HTTPS 桥操作 VM 的终端和交互式桌面；桥不运行第二套 AI。

这是整机信任边界，不是沙箱。Bot 能使用 VM 上的 Git 凭据、安装工具并管理版本。
不要把个人日常电脑、共享生产机或未授权的桌面当作测试 VM。源码、输出及截图可能发给配置的
模型供应商，含云端供应商；私网桥不等于内容仅留在 VM。上线前核对管理工作台显示的模型数据流。

## 部署前提

- Windows 10/11 或带桌面体验的 Windows Server，64 位；独立交互账号用于 Bot，建议仅此用途。
- 一台 VM 对应一个 Bot；桥配置的实例 ID 与 FrostAgent 实例一致，不能复用/回滚另一实例的账本。
- VM 安装 Git 和 PowerShell，工具链使用固定绝对路径；按需持久安装多个版本。
- 使用 LAN / VPN / 私网，FrostAgent 主动连接。不要将桥端口直接映射到公网。
- VM 用户自行配置 Windows 登录与 Git Credential Manager / SSH 凭据；不上传 Windows 密码或 Git 私钥。
- 带匹配私网地址 SAN 的 TLS 证书、私钥，以及在 FrostAgent 端信任的 CA。不能跳过证书校验。
- 操作者另行备份 VM 磁盘、主仓库、worktree、工具链及桥状态账本。

开发者未提供专用测试 VM 时，只运行静态检查和替身测试，禁止在宿主机安装自动登录、启动计划任务、
执行桌面操作或重启。下文注册步骤只由操作者在目标 VM 内执行。

## 1. 编译与 VM 本地配置

在源码工作区编译对应 Windows 架构的桥，将产物和两个部署脚本复制到 VM 上的专用目录，
例如 `C:\FrostBridge`。源码树中不放真实配置或凭据：

```powershell
go build -o computer-bridge.exe ./cmd/computer-bridge
```

以上命令在 Windows 构建；有 Bash/Make 的源码环境也可执行 `make build-computer-bridge`，
以 `CGO_ENABLED=0 GOOS=windows` 输出 `bin/computer-bridge.exe`。需不同架构时显式设置
`GOARCH` 并核对 VM 架构。这是独立源码构建目标，不包含二进制发布或安装动作。

复制 `scripts/computer-use/bridge.example.json` 为 VM 本地 `bridge.json`，填写真实值。
示例中的 token 故意无效，不能原样部署。使用密码管理器生成至少 32 字节随机熵的 base64url 令牌，
只在 VM 配置和受认证的 FrostAgent 工作台之间传递，不写命令历史、日志、截图或仓库。

| 配置 | 说明 |
| --- | --- |
| `instance_id` | 对应 Bot 的实例 ID |
| `listen` | 数值私网 IP 和端口，例如 `192.168.50.20:9443`；不是 `0.0.0.0` |
| `token` | 此桥的独立高熵令牌；持有者拥有 VM 操作权限 |
| `certificate` / `key` | VM 本地 PEM 证书与私钥绝对路径 |
| `state_dir` | 独立持久状态目录，包含去重账本及项目/工作区注册信息 |
| `audit_dir` | 独立审计目录，与 state 不同且不得互相嵌套 |
| `project_root` | 主项目仓库根目录；任务 worktree 不等同主项目 |
| `git` | Git 可执行文件的固定绝对路径 |
| `max_concurrent` | 桥操作并发预算；不同于模型任务总预算 |
| `audit_retention_days` | 默认 7：当天及此前六个 UTC 日历日 |

使用 Windows 文件安全属性限制配置、证书私钥、脚本和桥二进制：仅专用运行账号、SYSTEM 和
必要管理员可读取；非受信任账号不能修改二进制/启动脚本。state/audit 对专用账号可写，
不能放在项目内让日常仓库清理删除。审计不是凭据存储，故障时拒绝新的副作用。
不要对整个磁盘、用户目录或其他项目运行递归 ACL/删除命令。

桥账本记录的 `unknown` 表示操作效果不能确定，不是“未执行”。保留账本，不通过删除数据库、
复制旧快照或换新操作 ID 来恢复同一副作用。恢复旧 VM 快照后先人工核对系统与 Git 状态。

## 2. 自动登录与交互式桌面恢复

在 VM 控制台登录专用账号，使用微软官方 [Sysinternals Autologon](https://learn.microsoft.com/en-us/sysinternals/downloads/autologon)
的交互界面配置自动登录；不要把密码作为命令行参数传入工具。Autologon 将凭据保存在 LSA secret，
但 VM 管理员仍可能提取，不能将其视为对管理员保密。

自动登录是明确的风险接受：能访问 VM 控制台/磁盘的人可能取得整个会话。限制 hypervisor、
控制台和远程桌面权限。域策略、锁屏、UAC 安全桌面或 RDP 断开可能影响可用桌面，
必须按实际 VM 的登录策略验收；不绕过安全桌面，也不宣称可在同一会话隐藏并行操作。

桥必须运行在已登录用户的交互会话，不能用 Session 0 服务代替。部署脚本使用
[Interactive 计划任务 principal](https://learn.microsoft.com/en-us/powershell/module/scheduledtasks/new-scheduledtaskprincipal)
和登录触发器，运行权限为该专用账号可用的最高权限。计划任务不保存 Windows 密码。
启动器隐藏辅助控制台并等待桥退出，计划任务才可跟踪进程退出并有限次重启。
重启恢复桌面不意味着恢复 Agent 任务。

## 3. 注册开机登录后的桥启动

将 `Register-FrostComputerBridge.ps1` 和 `Start-FrostComputerBridge.ps1` 放在一起。
在专用 VM 的管理员 PowerShell 中，先预览，再确认执行；用真实用户名和 FrostAgent 私网地址替换示例：

```powershell
.\Register-FrostComputerBridge.ps1 -BridgePath 'C:\FrostBridge\computer-bridge.exe' -ConfigPath 'C:\FrostBridge\bridge.json' -User 'VMNAME\FrostBot' -AllowedAddress '192.168.50.10' -ConfirmDedicatedVM -WhatIf
.\Register-FrostComputerBridge.ps1 -BridgePath 'C:\FrostBridge\computer-bridge.exe' -ConfigPath 'C:\FrostBridge\bridge.json' -User 'VMNAME\FrostBot' -AllowedAddress '192.168.50.10' -ConfirmDedicatedVM
```

该脚本仅注册，不启动，不重启，不覆盖已有任务/防火墙规则，不设置自动登录。防火墙规则限指定
FrostAgent 私网 IP、桥程序、监听地址/端口和 Domain/Private profile；不允许 Any/网段通配。
若 VPN 被 Windows 标为 Public，应由操作者核对网络信任策略，不能扩大到任意来源。
脚本若在规则建立后注册任务失败，会保留规则并报告名称，供操作者显式检查。

检查计划任务属性后，在该账号已登录的 VM 中手动启动任务。查看 Task Scheduler 的 Last Run Result、
VM 私网监听和 FrostAgent 同步结果；不要用“任务已注册”代替“桌面可用”。若 PowerShell 执行策略
阻止脚本，按组织策略签名/许可已审查脚本；脚本不使用 ExecutionPolicy Bypass。

桥默认不开放公网。TLS 证书需与工作台桥 URL 匹配，将私有 CA PEM 填入 FrostAgent 配置；
只传 CA 公钥证书，不传 VM 证书私钥。现有更宽的防火墙规则也可能放行流量，须一并审查。

## 4. FrostAgent 工作台与可信私聊

在真实管理控制台选择 Bot → Computer Use：

1. 填写 HTTPS 私网桥 URL、桥令牌和 CA。令牌不回显；留空保存保留旧值，明确勾选清除才移除。
   停用状态可以保留合法桥地址并清除令牌，重新启用必须补齐有效凭据；执行中的请求须先暂停和核对。
2. 设置启用、并发任务/时间/模型轮数/工具调用预算及审计保留天数，确认云模型数据流。
3. “连接 VM 同步项目”显式读取桥；普通“刷新状态”只读取 FrostAgent 已保存的元数据。
4. 注册项目 ID、VM 中主仓库路径、基准分支/提交、PowerShell 路径和工具链环境。
   主仓库须在配置根目录内且有 `.git` 目录。可选 remote 不包含密码或令牌，Git 使用 VM 本地凭据。
5. 输入独立任务指令启动任务。每个任务从基准创建独立 worktree，`.git` 文件合法；
   不隐式携带主仓库脏修改，也不自动注入群聊历史、引用消息或记忆。

私聊 Computer Use 另需“可信适配器令牌”：每 Bot 独立高熵值，必须与 VM 桥令牌不同。
在 OneBot 反向 WebSocket / AstrBot 插件连接配置中，使用 WSS（仅数字本机回环地址可用 WS），将其用于握手
`Authorization: Bearer <adapter_token>`；不要放 URL 查询参数。
禁止在远端明文 WS 连接上传递令牌；未配置、非安全传输或握手未认证时，私聊不能获得
Computer Use 权限，普通聊天继续兼容。
认证后的 QQ 私聊仍须匹配本实例 `ADMIN_QQ_IDS`；群聊、主动回复和 Web 模拟聊天永远不能提升为
本工作台。令牌只证明适配器连接可信，不替代主人身份检查。

AstrBot 插件从 0.1.5 起使用 `computer_use_adapter_token` 配置项，或环境变量
`FROSTAGENT_COMPUTER_USE_ADAPTER_TOKEN`；非空插件配置优先。令牌为 32～256 个 base64url
字符，需独立生成高熵值。认证连接只接受正常证书校验的 `wss://` 或数字回环
`ws://127.0.0.1` / `ws://[::1]`；`localhost` 名称、局域网明文 WS、URL 凭据及查询参数均拒绝。
插件不跟随重定向、不使用自动环境代理、不记录握手凭据/帧或原始异常。反向代理需保留
Authorization 且不得记录它；轮换后同步更新插件配置并重新加载连接。

可信私聊可用 `/computer start <项目ID> <独立任务指令>` 发起任务，`/computer status` 查看元数据。
暂停/核对/继续分别用 `/computer pause <任务ID>`、`/computer reconcile <任务ID>`、
`/computer resume <任务ID> <新的指令>`；
整机接管/解锁用 `/computer takeover`、`/computer desktop-resume`。
结果未知的任务只能在真实工作台人工核对并确认继续，不能用普通私聊确认替代。

### 固定工具链环境

项目路径/环境由受信任管理者提供，不能当作安全沙箱。工具链应安装在固定版本的 bin 目录，
并把该绝对目录置于项目 PATH 前方，例如专用账号下的
`C:\Users\FrostBot\.rustup\toolchains\<固定版本>\bin`，而不是 `.cargo\bin` 的 rustup shim。
保留旧版本目录，未来任务才使用新的项目默认配置。

shell 注册时解析真实路径，任务创建时仅复制允许的环境变量。PATH 中相对路径、解析失败/
不可读目录、`shims`、`WindowsApps`、`.cargo/bin` 及包含 `rustup`/`rustup.exe` 的目录会排除；
目录别名会解析为实际路径，Windows 上含可执行文件 symlink 的目录也会排除。
因此只配置版本管理器的默认入口可能造成命令不可用，需显式配置版本目录。

已有任务保留创建时的真实路径和环境，但这不是二进制内容快照，也无法识别任意脚本内部读取的
可变默认版本；不要原地替换正在使用的版本文件。不要在环境字段保存密码/令牌。
全局工具链维护需要协调同机其他任务。默认保留本地结果；任务指令或仓库 `AGENTS.md` 要求时
可以 commit/push。

## 5. 暂停、人工接管与未知结果

- **暂停任务**只暂停该任务后续调度并确认该任务 GUI 输入，其他任务仍可能使用桌面。
- **暂停整机桌面 / 人工接管**等待本次桥控制确认 `gui_stopped` 与 `desktop_paused`，
  且工作台随后快照的 `desktop_confirmed` 为真、`desktop_generation` 与控制响应一致后才显示确认。
  新控制会使旧确认失效；过期响应、断线、超时、未确认或代次不一致都不能当作可接管。
  终端/后台进程是否结束要另行核对。
- **恢复 Bot 桌面权限**解除整机 GUI 锁，不自动继续任何任务。继续任务须另行填写明确的新指令。
- **刷新状态**只读 FrostAgent 缓存元数据，不查询桥；记录的 `desktop_paused` 不等于实时停止。
  “连接 VM 同步项目”才显式读取桥状态。初始/失效的 `desktop_confirmed=false` 不能提供接管确认；
  即使已确认，也需与其他受信任操作者协调，防止随后有人重新解锁。
- **核对状态**只查询原操作事实，不重放。accepted/running 必须继续等待；
  unknown 需要人工核对实际文件、Git、进程和外部系统，再确认继续，绝不能盲目重放旧副作用。
- FrostAgent 重启、桥重启、模型失败或预算耗尽后，不自动继续未完成任务。
- 核对发现桥端没有旧请求记录时，保留原操作身份并标为结果未知，人工检查后才能明确继续；
  not-found 不代表操作从未执行，也不会自动换 ID 重跑。
- 命令非零退出仍暂停任务，但会取回尚未过期的失败诊断供临时预览；诊断过期不显示上一条
  成功操作的内容，也不要求重跑失败命令。
- 临时文本/截图只作短期预览，桥缓存最长 15 分钟且可能因容量或重启提前丢失。
  过期会清除，不以重新执行操作恢复内容；不写操作账本、审计或浏览器持久存储。

归档项目只是隐藏记录。删除 Bot、元数据恢复或重新配置不应删除 VM 代码、worktree 或工具链。
没有显式确认清理范围前，保留 worktree；当前工作台不提供删除磁盘按钮。

## 备份与上线验收

FrostAgent 实例 ZIP 的可选 `computer_use.json` 只覆盖项目/任务元数据，不包含
`computer_use_config` 连接配置及桥/适配器秘密、项目环境、模型消息、源码或截图。
历史任务元数据可能包含桥 URL 和本地路径，分享备份前须核对；导入时清空历史桥绑定。
此备份不替代 VM 磁盘与 Git 备份。
恢复/复制后 VM 配置保持禁用和脱离绑定，原任务仅供历史核对，不能直接继续原任务。
重新配置桥及项目环境、人工核对旧操作后，以新的独立指令新建任务；不能换 ID 重放未知副作用。
旧 worktree 仍留在 VM，新任务不自动继承其未提交内容。此限制不影响同实例正常重启后的显式继续。
恢复不会删除远端代码。桥去重账本与实际副作用必须一起考虑备份时点。

源码内有 SQLite 元数据 ZIP/复制恢复测试，以及 PostgreSQL 元数据和 migration 回滚/重试测试；
目前没有专用 PostgreSQL computer-use 备份测试，不将前者当作后者已通过的证据。

源码静态检查可在开发机运行，不需要管理员权限，也不会执行部署脚本：

```powershell
.\scripts\computer-use\Test-DeploymentScripts.ps1
```

上线前在专用 VM 中实际验证并保存验收记录：

- 主人私聊/管理控制台准入、非主人/群/主动回复/mock 拒绝，运行中撤销主人或适配器令牌。
- TLS/桥认证/防火墙范围，公网和未授权来源不可达；真实 API 不泄露原始内容到普通日志。
- 两个项目/任务并行 worktree，主仓库脏文件不复制，已有任务工具链不被新默认值切换。
- GUI 独占、整机人工接管确认、恢复权限后任务不自动继续，锁屏/UAC 下失败不冒充成功。
- 命令超时/后台进程，断线后 same-ID 核对、崩溃 unknown、旧响应不驱动新 generation。
- VM 手动重启 → 自动登录 → 桥启动 → 桌面可用；FrostAgent 重启 → 任务保持暂停。
- 七天审计轮转、写入权限/磁盘故障 fail-closed、内容过期、元数据备份/恢复不删除代码。

这些真实 VM 场景不能由 TypeScript 类型检查、构建、PowerShell 语法检查或模拟器测试代替。

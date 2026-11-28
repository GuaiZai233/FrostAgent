# frostctl 管理指南

每个后端发行包都包含同版本的 frostctl、后端和本指南。Docker 镜像包含
/usr/local/bin/frostctl；原生包由 make package 或 CI 的 Backend Packages
任务生成。使用 frostctl version 和 frostctl version --server 核对版本。

## 本机连接与升级

省略 --remote 时始终连接本机 IPC，不保存默认远程目标，也不回退到 HTTP。
Linux 只接受同一用户命名空间内 UID 0 的客户端。默认 Socket 为
/run/frostagent/management.sock。原生服务由服务管理器为运行账号创建
/run/frostagent（0700）；父目录必须由 root 或该服务账号控制且不能由其他账号写入。
例如 systemd 服务使用 RuntimeDirectory=frostagent 和 RuntimeDirectoryMode=0700。
Socket 路径可用 FROSTAGENT_IPC_PATH 覆盖，后端与 CLI 必须一致。
普通用户无法创建该目录时，后端记录 `local management unavailable` 警告并继续运行；
本机 frostctl 此时不可用，HTTP 管理仍要求 Token。可由服务管理器准备上述目录，
或使用下述 bootstrap 初始化后通过 Web/HTTPS 管理。

```sh
sudo frostctl instance list --output json
docker compose exec --user 0 frostagent frostctl instance list --output json
docker compose exec --user 0 frostagent frostctl doctor --instance INSTANCE_ID
```

容器后端仍以 UID 10001 运行。CLI 必须在同一容器中以 UID 0 执行；
容器外的 root 或转发 Header 不构成本机身份。不要把 Socket 暴露给其他容器。

Windows 使用本机命名管道 \\.\pipe\FrostAgent.Management。
本机管道要求从“以管理员身份运行”的 PowerShell 启动 CLI；未提权的管理员进程会被拒绝。
未提权后端仍可启动，但本机管道不可用。CLI 验证管道的服务端进程；管道服务端必须为提升权限的管理员/System，
或 SCM 中名称为 FrostAgent 的服务所对应的进程。以 LocalService/NetworkService
运行时必须通过该服务注册及 PID 校验。此 CLI 不安装 Windows 服务；
已有服务包装器须把实际管道进程作为 SCM 服务进程。普通用户不能抢占管理管道。

所有 HTTP 管理请求现在都需要当前管理 Token，包括 IPv4/IPv6 回环、
REST、ConnectRPC、下载、日志流和管理 WebSocket。
静态面板和真实 OneBot 入站保留各自的入口与鉴权。
MCP_ENFORCE_LOCAL_TOKEN 和 ALLOW_REMOTE_MCP_MANAGEMENT 保留已保存的值，
但不再控制 HTTP 认证；任何取值都不能恢复回环免 Token 访问。

升级前先在旧部署设置管理 Token，并让面板和自动化发送 Authorization: Bearer。
新部署无 Token 时可以启动，管理 HTTP 返回 401；静态面板保留访问凭据入口和初始化引导。
IPC 可用时可通过它初始化；IPC 不可用时，原生部署和容器均可使用部署环境
FROSTAGENT_BOOTSTRAP_CONTROL_TOKEN 可一次性写入 SQL（32–4096 个不含空格的
可打印 ASCII 字符），已有 MCP_CONTROL_TOKEN 或 ADMIN_TOKEN 时不覆盖。
环境变量不用于后续轮换；通过本机 IPC 从标准输入设置：

```sh
frostctl config secret set MCP_CONTROL_TOKEN --scope global --stdin
```

该命令从 stdin 读到 EOF。避免把真实 Token 写入命令参数或终端历史。
HTTP 只认可当前 SQL 配置；Token 轮换和切换数据库后旧 Token 失效。
IPC 身份在轮换后仍有效。

升级已有普通用户部署时，优先在升级前保存管理 Token；若已升级且无 Token，
停止服务，在部署环境中注入上述 bootstrap 变量后从原数据目录重启，
在「设置 → 访问凭据」保存同一 Token。初始化成功后移除部署环境中的副本。
更换数据库前也要准备目标库自己的 Token；缺失时 HTTP 会被锁定，
可用 IPC 初始化，或保留 bootstrap 输入来初始化目标库。

## 远程与结果

```sh
frostctl remote add homelab --url https://agent.example
frostctl remote token set homelab --stdin
frostctl instance list --remote homelab --output json
frostctl instance list --output json
```

最后一条仍然访问本机。远程只支持系统信任链验证的 HTTPS，拒绝重定向；
URL 源变化会清除旧 Token。远程 HTTPS 遵循 HTTPS_PROXY / NO_PROXY，
代理隧道仍执行目标服务端的系统证书与主机名校验；本机 IPC 不使用代理。
优先系统凭据存储，不可用时使用当前用户私有文件。
remote token set 的结果和 remote show/list 均显示 store（system 或 file）；
回退文件时 stderr 另行提示。file 是未加密的凭据文件，Linux 权限为 0600，
Windows 使用当前用户 DACL；不要将其目录纳入可共享的配置备份。
remote token 只保存客户端凭据，不会初始化或修改服务端 Token。

help 和 schema 从与参数解析相同的定义生成。命令默认 table，可选 json；
logs tail 支持 jsonl。退出码：0 成功、1 已知失败、2 参数错误、3 写入结果未知。
unknown 可能表示后端已经提交，CLI 不自动重试。先执行响应中
verification_command 的参数数组核对当前资源，并保留 correlation 中的关联 ID。
--timeout 限制等待时间，不保证撤销已提交的操作。

SQL 配置提交若丢失确认，后端会立即尝试独立读回；读回成功后同步运行时，
本次响应仍为 unknown。若数据库无法读回，配置查询和后续写入会明确返回不可用，
不会用旧缓存作为核对结果；受影响的管理 Token 也暂停授权。恢复数据库连接并重启
后端后，再通过本机 IPC 执行 verification_command 核对。

## 日常管理

```sh
frostctl schema --output json
frostctl status --instance INSTANCE_ID
frostctl doctor --instance INSTANCE_ID
frostctl doctor --check management --check storage
frostctl storage status
frostctl logs list --instance INSTANCE_ID --level error --limit 50
frostctl logs show ERROR_UUID --instance INSTANCE_ID --include-payload
frostctl logs tail --scope control-plane --follow --output jsonl
frostctl logs export --instance INSTANCE_ID --file logs.jsonl
```

完整 doctor 要求实例；management/storage 可独立运行。诊断只读取配置和已观测状态，
不会调用模型或同步 MCP。显式 model test 和 mcp server sync 才访问上游。
控制面日志保存于数据目录的 control-plane-logs，容量 128 MiB，按容量淘汰，
无按天过期。实例日志使用自己的 LOG_STORAGE_MIB。查询返回 coverage，
包含保留时间范围、容量与持久化错误。实时流为尽力而为，断线可能丢失事件；
导出失败留下 .partial 文件，并返回 complete=false。
控制面日志、模型目录缓存和本地数据库统一使用启动时解析的绝对数据根目录。
原生入口沿用当前工作目录下的 data；服务管理器应固定 WorkingDirectory，
从其他目录启动会选择另一套数据。此版本没有独立的日志目录配置。

security audit list 支持 --limit 1–1000（默认 100）和 --offset 0–10000，
在服务端按实例过滤后分页，按新到旧返回。审计存储仍保留最多 1000 条事件；
并发追加或保留策略淘汰可能使 offset 分页移动。

## 配置

```sh
frostctl config schema --instance INSTANCE_ID --output json
frostctl config get BOT_NAME --instance INSTANCE_ID
frostctl config export --instance INSTANCE_ID --file config.json
frostctl config validate --instance INSTANCE_ID --file config.json
frostctl config diff --instance INSTANCE_ID --file config.json
frostctl config apply --instance INSTANCE_ID --file config.json --dry-run
frostctl config apply --instance INSTANCE_ID --file config.json
```

配置文件使用按键更新，省略的键保留，unset 数组移除覆盖：

```json
{"values":{"BOT_NAME":"Example"},"unset":["BOT_ALIASES"]}
```

导出仅包含该范围显式保存的普通配置；secret_states 为只读提示，应用时忽略。
继承值和代码默认值不会导出成实例覆盖。配置值仍按字符串输入；schema 中的
type 描述语义类型，default_hint 描述依赖其他设置的默认规则。
批量写入在后端重新校验并原子提交；响应区分已生效和待重启。
普通查询只显示敏感项配置状态。敏感项必须使用 config secret set/unset。

## 会话、模型与依赖

```sh
frostctl trace find --error ERROR_UUID --instance INSTANCE_ID
frostctl trace show TRACE_ID --instance INSTANCE_ID
frostctl session list --instance INSTANCE_ID --platform onebot
frostctl session inspect SESSION_ID --instance INSTANCE_ID --include-prompt
frostctl session history SESSION_ID --instance INSTANCE_ID
frostctl session tools SESSION_ID --instance INSTANCE_ID
frostctl session tool show SESSION_ID CALL_ID --instance INSTANCE_ID
frostctl model endpoint list --scope global
frostctl model endpoint add --scope global --file endpoint.json
frostctl model endpoint secret set ENDPOINT_ID --scope global --stdin
frostctl model list --scope global --endpoint ENDPOINT_ID
frostctl model test MODEL_ID --endpoint ENDPOINT_ID
frostctl model route set dialogue --instance INSTANCE_ID --file route.json
frostctl mcp server list --instance INSTANCE_ID
frostctl mcp server sync SERVER_ID --instance INSTANCE_ID
frostctl security audit list --instance INSTANCE_ID
frostctl billing status
```

endpoint.json 使用 ModelEndpoint 的 JSON 字段，例如：

```json
{"id":"example","displayName":"Example","baseUrl":"https://models.example/v1","enabled":true,"apiFormat":"openai_chat_completions"}
```

route.json 引用目录中已注册的模型 ID：

```json
{"mode":"MODEL_BINDING_MODE_MODEL","modelId":"registered-model"}
```

端点和凭据共享，路由属于实例。CLI 提交一次操作立即生效；保留 Web 草稿 RPC，
旧草稿发布时检测版本冲突，维护者须刷新后再编辑。
进行中的模型轮次继续使用自己的快照。model list 只发现上游模型名，
不登记模型条目；当前模型登记仍使用 Web 目录编辑器。
端点 update 是完整端点定义替换，ID 不可更改。实例 route unset 恢复任务默认规则。

Trace 基于已有 TraceID 和审计证据，可能对应整个会话。未记录的阶段、结果或耗时
明确标为 unavailable，不推测单次处理关系。会话上下文、聊天原文与工具档案分别查询；
空档案返回覆盖范围。session tool show 的大结果按字符分页，可用 --offset 继续。
MCP 普通查询隐藏命令参数、环境和 Header 值、原始错误及 URL 中的认证内容；
不会把脱敏结果自动回灌到 Web 配置。billing status 展示每个实例的开关和共享连接配置，
没有历史连接健康证据时标为 unobserved。

## 生命周期与备份

```sh
frostctl instance create --name Example
frostctl instance restart INSTANCE_ID
frostctl instance config copy --from SOURCE_ID --instance TARGET_ID
frostctl backup create --instance INSTANCE_ID --file instance.zip
frostctl backup verify --file instance.zip
frostctl backup restore --file instance.zip --name Restored --dry-run
frostctl backup restore --file instance.zip --name Restored --confirm
frostctl instance delete prepare INSTANCE_ID
frostctl instance delete confirm INSTANCE_ID --backup instance.zip --confirm
frostctl support collect --since 2026-01-01T00:00:00Z --file support.zip
```

创建和恢复的实例默认停用。restart 在一个生命周期锁内停止并重建运行时。
copy 替换目标的可导出配置，不复制运行数据与独立凭据，并剔除端点 URL 的认证部分。
prepare 停止实例；cancel 取消待删除但保持停用。

verify 检查完整 ZIP、成员业务约束、格式与目标数据库能力；
dry-run 使用与导入相同的目录冲突规划器，只读，不预留名称/ID。
实际恢复重新校验，不覆盖已有实例，保留已有共享凭据。
备份格式 v1 新增可选数据库版本及应用版本元数据；旧备份缺失字段会明确报告 unknown，
并继续依据归档内容及目标能力检查。应用版本作参考，不替代数据库兼容性判定。
归档只有实例名称，没有绑定实例 ID 的删除证明。删除前重新验证本地备份的可恢复性，
归属检查明确为 not-provable。

实例 ZIP 保留完整端点 URL，可能包含内嵌凭据，须按敏感文件保管。
支持包为 ZIP，只收集日志元数据、配置状态、诊断状态和环境信息，
排除任意配置值、URL 凭据、日志原文、Prompt、聊天与工具结果；
manifest 记录 since、覆盖范围及 complete，任一组件失败会标记不完整。
logs clear --scope instance --instance INSTANCE_ID --confirm 或
logs clear --scope control-plane --confirm 会同时清除该范围内存和磁盘历史。

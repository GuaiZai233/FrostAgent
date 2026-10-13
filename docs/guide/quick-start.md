# 快速开始

本页的目标是先让一个实例完成对话，再接入真实聊天平台。命令均在仓库根目录运行。

## 1. 获取与构建

准备 Go ≥ 1.25.3、Node.js 22 或更高版本、pnpm 10.33.0、Buf、Make 与 Bash。Windows 可以使用 Git Bash 执行 Make，或按[安装与构建](./installation)中的 PowerShell 步骤构建。

```bash
git clone https://github.com/GuaiZai233/FrostAgent.git
cd FrostAgent
pnpm install --frozen-lockfile
go install github.com/bufbuild/buf/cmd/buf@latest
make build
```

构建产物为 `bin/app` 和 `bin/agent`，Windows 平台也可显式构建为 `.exe`。`app` 是运行服务，`agent` 是旧版 JSON 记忆文件工具，参见[升级说明](../operations/upgrading)。

## 2. 启动服务

::: code-group

```bash [Linux / macOS]
./bin/app
```

```powershell [Windows（显式构建 .exe 后）]
.\bin\app.exe
```

```bash [通过 Go 启动]
go run ./cmd/app
```

:::

从仓库根目录启动，让 `data/` 和默认示例对话模板路径保持一致。`go run` 需要已经构建控制台并生成 Go 协议代码。

首次运行默认使用 `data/frostagent.db`，管理页面为 `http://127.0.0.1:8080`，外部适配器监听为 `127.0.0.1:1234`。

::: tip 当前配置方式
当前版本通过控制台把配置保存到 SQL 数据库。无需复制 `.env.example`，修改根目录 `.env` 或设置进程中的 `SYSTEM_PROMPT` 不会配置当前实例。
:::

## 3. 创建实例

1. 打开管理页面，在侧边栏底部打开「实例管理」。
2. 创建并选择实例，记下它的实例 ID。
3. 打开「模型路由器」，添加模型供应商和模型。
4. 在「实例默认路由」为「主对话」选择模型并发布。
5. 打开「人格设定」，编辑系统提示词和示例对话。
6. 在「概览」打开「是否启用」。

新实例默认停用。页面刷新后需要重新选择实例。供应商地址填写 API 基地址，例如服务的 `https://api.example.com/v1`，不要填写完整的 `chat/completions` 地址；具体以供应商的 API 路径为准。

## 4. 验证一次对话

进入「直接对话」，选择协议和私聊场景，连接后发送一条问候。确认能收到回复，并在「日志查询」检查模型请求与投递记录。

「直接对话」使用临时模拟会话，断开连接后会清理。它可用于检查模型、人格和工具链，真实平台权限、消息投递与记忆写入仍需在正式接入后验证。模拟模式不参与 Alcyone 计费，但模型供应商仍可能对 API 调用收费。

## 5. 接入真实平台

从概览复制对应地址，把其中主机改为上游能访问到的主机，保留实例 ID 和路径：

```text
ws://127.0.0.1:1234/instances/<实例ID>/ws/onebot
ws://127.0.0.1:1234/instances/<实例ID>/ws/astrbot
```

- OneBot 上游：参见 [OneBot v11 接入](../adapters/onebot)。
- AstrBot：安装专用插件并关闭 AstrBot 自带的 LLM 回复，参见 [AstrBot 接入](../adapters/astrbot)。

首次群聊验证使用真实 @机器人；名称唤醒、主动回复与群记忆分别按[群聊策略](../usage/group-chat)配置。

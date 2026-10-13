# 维护文档站

文档位于仓库 `docs/`，使用 VitePress 1.6.4 和默认主题，加入中文导航、本地全文搜索与深浅色样式。它是 pnpm 工作区的 `@frostagent/docs` 包，与控制台独立构建。

## 运行

从仓库根目录执行：

```bash
pnpm install --frozen-lockfile
pnpm docs:dev
pnpm docs:check
pnpm docs:build
pnpm docs:preview
```

开发默认 `http://localhost:5173`，生产预览默认 `http://localhost:4173`，端口被占用时按终端输出访问。也可在 `docs/` 执行 `pnpm dev` / `pnpm build` / `pnpm preview`。

生产目录为 `docs/.vitepress/dist/`，缓存与输出均被 Git 忽略。构建默认检查内部死链；不要关闭死链检查来掩盖失效链接。

## 文件组织

| 路径                   | 内容                             |
| ---------------------- | -------------------------------- |
| `index.md`             | 首页                             |
| `guide/`               | 项目介绍、快速开始、安装部署     |
| `adapters/`            | 平台接入                         |
| `usage/`               | 功能使用                         |
| `integrations/`        | 外部集成                         |
| `reference/`           | 配置和 API 入口                  |
| `operations/`          | 数据、安全、日志与升级           |
| `development/`         | 开发和文档维护                   |
| `.vitepress/config.ts` | 站点配置、侧边栏、搜索和中文文案 |
| `.vitepress/theme/`    | 默认主题扩展                     |
| `public/`              | 按原样复制的静态资源             |

增加页面后同步侧边栏，并从相关章节链接到它。示例使用占位符或合成数据；可复制命令注明运行目录和前置条件。配置范围、默认值与生效条件从源码核对。

## 部署静态站点

默认基路径 `/`，把构建目录部署到任意静态文件服务即可。文档站不由 `cmd/app` 自动托管。

部署在子路径时通过 `DOCS_BASE` 指定前后都有 `/` 的路径，例如 GitHub 项目 Pages 使用 `/FrostAgent/`：

::: code-group

```bash [Bash]
DOCS_BASE=/FrostAgent/ pnpm docs:build
DOCS_BASE=/FrostAgent/ pnpm docs:preview
```

```powershell [PowerShell]
$env:DOCS_BASE = '/FrostAgent/'
pnpm docs:build
pnpm docs:preview
Remove-Item Env:DOCS_BASE
```

:::

基路径属于构建配置，修改后重新构建。默认保留 `.html` URL，普通静态文件服务即可解析，不需要为无扩展名路由额外配置重写。

仓库 CI 执行文档类型检查和构建；不自动发布 Pages。部署前检查目标域名和路径，按 [VitePress v1 部署指南](https://vuejs.github.io/vitepress/v1/guide/deploy)配置所用静态平台。

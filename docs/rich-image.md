# 富文本图片发送（send_rich_image）

QQ 消息只能承载文字。`send_rich_image` 让机器人把 Markdown（含 LaTeX 数学公式）或
SVG 图示渲染成 PNG，并作为一条图片消息发送到当前 OneBot 会话。适合数学解题、表格、
代码清单和抽象概念示意图。渲染完全在 Go 进程内完成，不依赖浏览器、AstrBot 或外部服务。

## 参数

| 参数 | 说明 |
| --- | --- |
| `format` | `markdown` 或 `svg`。不支持 HTML。 |
| `content` | 源文本。Markdown 最大 20000 字节，SVG 最大 100000 字节。 |
| `theme` | `light`（默认）或 `dark`，仅对 Markdown 生效；SVG 固定白底。 |
| `width` | 版面宽度（CSS 像素，320–1200，默认 640）；PNG 为两倍宽度。 |

渲染后高度超过限制时会返回错误，由模型拆成多张图重新发送。渲染失败同样以工具错误返回，
模型可以修正内容后重试，或改用 `send_message` 发送文字。

## Markdown

支持标题、段落、粗体、删除线、行内代码、代码块、有序/无序列表、引用、表格、分隔线。

数学公式使用 `$...$`（行内）和 `$$...$$`（独立成块）。`$5 and $10` 这类金额写法不会被误判。
已支持：

- 上下标、分数（`\frac` `\binom`）、根号（`\sqrt` `\sqrt[n]{}`）
- `\sum` `\prod` `\int` `\lim` 等大型运算符及上下限
- 希腊字母、常见运算符与关系符、`\left...\right`、`\text{}`、`\mathbf{}`、`\mathbb{}`
- 重音（`\hat` `\bar` `\vec` `\overline` `\tilde` `\dot`）
- `matrix` `pmatrix` `bmatrix` `vmatrix` `cases` `aligned` 环境

不支持的命令不会让整张图失败，而是把公式源码以代码样式显示。

## SVG

需要完整的 `<svg>` 和 `viewBox`。形状、路径、渐变使用 SVG 属性（`fill`、`stroke`）。
`<text>` 支持 `x` `y` `font-size` `fill` `text-anchor` `font-weight`、`tspan` 与
`translate()` 变换。不支持 CSS `<style>`、class、脚本、外部图片、`<foreignObject>`，以及 `<use>` 和任何 href 引用（会被直接拒绝，请内联图形）；元素最多 3000 个。

## 字体

默认使用思源黑体（Noto Sans CJK SC）。程序按顺序查找常见系统路径，Docker 镜像已安装
`fonts-noto-cjk`。如需指定字体文件（`.otf` `.ttf` `.ttc`），设置环境变量：

```text
FROSTAGENT_RICH_IMAGE_FONT=/path/to/SourceHanSansSC-Regular.otf
```

找不到 CJK 字体时，含中文的内容会返回明确错误，而不是渲染成方块。数学符号使用内嵌的
STIX Two Math 兜底，因此公式不依赖系统字体。

## 发送与限制

- 仅在当前活动的 OneBot 会话可用；主动回复、模拟会话和无 OneBot 连接时不可用。
- 图片先写入系统临时目录，通过 `qq_extend_messages` 同一条内联传输链路（单次 30 MiB 上限）
  发送，30 分钟后的残留文件会在下次调用时清理。

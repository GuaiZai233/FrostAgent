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
- `\boxed{}`、`\cancel{}`、`\underbrace{}_{}`、`\overbrace{}^{}`、`\stackrel`、`\overset`、`\underset`、`\xrightarrow`、`\xleftarrow`、`\not`
- `\bigl \Bigl \biggl \Biggl` 等大小定界符，`\lvert \rVert` 等绝对值/范数记号
- `\checkmark`、`\oint`、`\oiint`、`\bigotimes`、`\leqslant`、`\impliedby`、`\longrightarrow` 等常用符号
- `matrix` `pmatrix` `bmatrix` `vmatrix` `Vmatrix` `Bmatrix` `cases` `aligned` `align*` `gather` 环境

代码块可以用围栏语言标记着色。语法高亮使用 chroma，凡是 chroma 认识的语言名或别名
（如 `python` `go` `js` `ts` `bash` `json` `rust` `yaml` `sql` 等几百种）都会着色：

- 没有语言标记或语言 chroma 不认识的代码块保持单色。
- Raku（`raku` / `perl6` / `pl6`）不着色：chroma 的 Raku 词法器会改写自己的共享规则，
  这些改动会留到下一次分词，所以排除掉。
- 高亮过程在一个互斥锁内完成，并发渲染的结果与串行一致。
- 亮色与暗色主题各有一套配色。

已知局限（如遇到请反馈）：

- 大括号画成横线加端点，不是曲线花括号；`\cancel` 只是一条斜线。
- 排版是简化实现，上下标位置、间距和 TeX 有细微差别，没有 `\color`、`\label`、`\tag`、`\nonumber`、`\hspace`。
- `array` 忽略列格式，统一居中。
- 符号若两个字体都没有，会显示为源码而不是整张图失败。

`go test` 中的 `TestCommonCommandsParse` 和 `TestCommonCommandsLayout` 会检查一份常用命令清单，任何不支持的写法都会一次性列出。

不支持的命令不会让整张图失败，而是把公式源码以代码样式显示。

## SVG

需要完整的 `<svg>` 和 `viewBox`。形状、路径、渐变使用 SVG 属性（`fill`、`stroke`）。
`<text>` 支持 `x` `y` `font-size` `fill` `text-anchor` `font-weight`、`tspan` 与
`translate()` 变换。不支持 CSS `<style>`、class、脚本、外部图片、`<foreignObject>`，以及 `<use>` 和任何 href 引用（会被直接拒绝，请内联图形）；元素最多 3000 个；仅支持 UTF-8 编码的合法 XML。

## 字体

字体不内嵌进二进制，而是从上游 GitHub 仓库下载，固定版本并校验 SHA-256：

| 用途 | 字体 | 固定来源 |
| --- | --- | --- |
| 中英文正文 | 霞鹜臻楷 LXGW ZhenKai GB v0.825（约 17 MB，SIL OFL 1.1） | `github.com/lxgw/LxgwZhenKai` release v0.825 |
| 数学公式 | STIX Two Math v2.13（约 0.8 MB，SIL OFL 1.1） | `github.com/stipub/stixfonts` 固定到 v2.13 的提交 |
| emoji 与符号 | Noto Emoji（单色轮廓，约 2 MB，SIL OFL 1.1） | `github.com/google/fonts` 固定到 8b0a1d0 提交 |

URL、版本和哈希写死在 `internal/richimage/fontstore.go`，不能由模型或配置改成其它地址。

- **Docker 镜像**：构建时下载并校验，放在 `/usr/share/fonts/frostagent`，并通过
  `FROSTAGENT_RICH_IMAGE_FONT_DIR` 指向该目录，运行时无需联网。
- **本地运行**：第一次渲染时下载到用户缓存目录（`<UserCacheDir>/frostagent/fonts`），之后直接使用。
  可用 `FROSTAGENT_RICH_IMAGE_FONT_DIR` 指定目录。离线环境把两个文件按上表文件名预先放进该目录即可，
  程序只接受哈希匹配的文件，损坏或被改动的缓存文件会被拒绝并重新下载。
- 下载失败会在 5 分钟内不再重试，避免每次渲染都卡在网络上；调用方取消不计入失败。
- 需要替换正文字体时，设置 `FROSTAGENT_RICH_IMAGE_FONT=/path/to/font.otf`（支持 `.otf` `.ttf` `.ttc`）。
- 字符按“正文字体 → STIX → Noto Emoji”逐个回退选择字形。三者都没有时直接报错并指出字符，
  不会再画成缺字方框。emoji 为单色轮廓，颜色跟随文字颜色，不是彩色 emoji。
- 两个字体都下载失败且系统里没有思源黑体等 CJK 字体时，含中文的内容返回明确错误；
  没有 STIX 时公式退回到内置的 Go 字体，缺失的符号以源码显示。

字体的许可证文本见仓库根目录的 `THIRD_PARTY_NOTICES.md`（Docker 镜像内位于
`/usr/share/doc/frostagent/`）。这两个环境变量是部署级设置，从进程环境变量读取。

## 发送与限制

- 仅在当前活动的 OneBot 会话可用；主动回复、模拟会话和无 OneBot 连接时不可用。
- 图片先写入系统临时目录，通过 `qq_extend_messages` 同一条内联传输链路（单次 30 MiB 上限）
  发送，30 分钟后的残留文件会在下次调用时清理。

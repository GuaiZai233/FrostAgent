# 模型端点 API 格式

在“模型路由 → Endpoint”中选择 API 格式，只填写服务或中转站的 Base URL，例如 `https://relay.example.com`。代理前缀会保留；末尾已有对应的 `/v1` 或 `/v1beta` 时不会重复追加。

| API 格式 | 请求路径 |
| --- | --- |
| Anthropic Messages（原生） | `/v1/messages` |
| OpenAI Chat Completions | `/v1/chat/completions` |
| OpenAI Responses API | `/v1/responses` |
| Gemini Native generateContent | `/v1beta/models/{model}:generateContent` |
| 自定义（完整 URL） | 原样使用填写的完整 URL，不追加路径 |

四种预设格式按各自协议转换文本、图片、工具定义、工具调用与结果，以及用量。模型测试和运行时使用相同的格式。

自定义模式使用 **OpenAI Chat Completions** 请求与响应格式，支持 URL 查询参数，需手动填写模型名称。如需自定义 Embedding 路径，请单独建立自定义端点，填写完整的 Embedding URL。

新建端点默认选择 OpenAI Chat Completions。已有端点保留“旧版 OpenAI”模式：继续向原 Base URL 追加 `/chat/completions`，不会自动插入 `/v1`。可以编辑旧端点切换格式。

Embedding 仍使用 OpenAI 兼容协议。OpenAI 两种预设格式生成 `/v1/embeddings`；Anthropic/Gemini 端点需另配 OpenAI 兼容 Embedding 端点。

数据库保存 `api_format`，升级至 schema 16，兼容性下限为 16。15 为历史 Computer Use 开发版保留；历史模块数据及其完整备份、删除保护继续保留。升级后的数据库需要支持 schema 16 的程序。

协议参考：[OpenAI Responses](https://developers.openai.com/api/reference/resources/responses/methods/create)、[Anthropic Messages](https://platform.claude.com/docs/en/api/http/messages)、[Gemini generateContent](https://ai.google.dev/api/generate-content)。

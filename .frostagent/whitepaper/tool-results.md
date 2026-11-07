# 工具结果与 Engine 的演进边界

Engine 负责模型调用循环、工具前置门禁与终止决策的编排。工具负责业务执行，并通过 `core.ToolExecutionResult` 报告模型可见的 Content、已完成的持久记忆写入 MemoryWritten，以及待适配器交付的 MessagePayload。消息 payload 表示发送请求，只有现有 SendHook / 平台确认成功后才反馈发送成功。

`internal/llm/tool_execution.go` 负责执行接口适配和消息结果交付：统一选择结构化或旧接口、检查取消与会话 epoch、调用 SendHook。核心 memory、send_message、send_sticker 使用结构化接口；普通工具的文本结果默认没有副作用，也不会因形似消息 JSON 而自动发送。Engine 对文本与不同的 message payload 一并进行安全审查，被拦截或过滤的结果不触发发送；已完成的记忆写入仍据实际执行结果报告。

兼容路径：保留 `Execute` / `ExecuteContext` 的字符串返回值、错误处理和现有文案。仅对未实现可选 `core.StructuredToolExecutor` 的旧执行器与 MCP 适配器，在上述单一边界保留原来的记忆文案与消息 JSON 识别。新增核心工具应实现结构化结果；旧执行器可逐个迁移，不要求一次性改动外部工具。后续计费与会话能力继续保留在各自模块，新增跨领域业务优先通过现有组件接口接入，避免继续扩展核心循环。

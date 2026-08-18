# 计费系统设计文档 (Count / Billing)

> 本文档系统阐述 FrostAgent 计费系统（雪花计费）的架构、接口、数据流、保护机制与实现方案。

---

## 一、设计原则

1. **Fail-Closed（故障闭合）与资金安全**：
   - 计费系统不可用或网络异常时，拒绝发起对话，防止未授权与无限穿透。
   - 对话前预先计算最大可能开销并执行预扣款（Reserve），模型调用结束后按实际消耗结算（Commit），多退少补；异常时全额释放（Release）。
2. **定点数最小单位（Minor Unit / 厘）**：
   - 系统内部一律采用整数 `int64` 表示最小单位（1 雪花 = 100 厘 / minor units，即 0.01 雪花），杜绝浮点数精度丢失问题。
   - 向上取整原则：计算开销不足 1 厘时向上舍入为 1 厘，确保开销覆盖。
3. **单轮与 Tool 循环全生命周期隔离与计费**：
   - Agent 工具循环中的每一轮迭代独立估算、预占与结算。
   - 若中途某轮出现余额不足，立即安全终止后续循环，已执行的轮次开销不予回退。
   - 若 Tool Call 轮次的 Commit 结算失败，立即阻断后续工具执行并返回错误，避免"免费调用产生外部副作用"。
4. **多模态与视觉前置边界防护**：
   - 包含图片的多模态请求在下载图像或调用视觉模型前，前置查询用户余额。余额为 0 或欠费时快速失败拦截。
5. **硬上限与防恶意攻击防护**：
   - 单条输入字符数上限（30,000 runes）。
   - 单轮与上下文总 Token 上限防护（MaxContextTokens = 128,000, MaxSingleInputTokens = 32,000）。
   - 单次工具输出硬截断保护（MaxToolOutputBytes = 64KB）。

---

## 二、系统架构与数据流

```
                ┌──────────────────────────────────────────────┐
                │          OneBot Adapter / WS Server          │
                └──────────────────────┬───────────────────────┘
                                       │ 1. 接收用户消息
                                       ▼
                     ┌───────────────────────────────────┐
                     │ 包含图片? ──▶ 前置余额校验 (Balance) │
                     └─────────────────┬─────────────────┘
                                       │ 2. 构造 RunContext (带 BillingRunState)
                                       ▼
                       ┌───────────────────────────────┐
                       │      LLM Agent Engine         │
                       └───────────────┬───────────────┘
                                       │
        ┌──────────────────────────────┴──────────────────────────────┐
        ▼ 每轮迭代 (Iteration)                                         │
┌──────────────────────────────┐                                      │
│ 1. 上下文与输入 Token 估算     │                                      │
│    (billing.EstimateTokens)  │                                      │
└──────────────┬───────────────┘                                      │
               ▼                                                      │
┌──────────────────────────────┐                                      │
│ 2. 预扣款 (ReserveLLM)       │ ──▶ POST /v1/billing/llm/reserve     │
│    余额不足 ──▶ 立即终止循环  │                                      │
└──────────────┬───────────────┘                                      │
               ▼                                                      │
┌──────────────────────────────┐                                      │
│ 3. 调用模型 (Provider.Chat)   │                                      │
│    失败 ──▶ 释放 (ReleaseLLM) │ ──▶ POST /v1/billing/llm/release     │
└──────────────┬───────────────┘                                      │
               ▼                                                      │
┌──────────────────────────────┐                                      │
│ 4. 实际用量结算 (CommitLLM)   │ ──▶ POST /v1/billing/llm/commit      │
│    失败且含 ToolCall ──▶ 阻断 │                                      │
└──────────────┬───────────────┘                                      │
               ▼                                                      │
┌──────────────────────────────┐                                      │
│ 5. 执行工具并截断 (64KB限制)   │                                      │
└──────────────┬───────────────┘                                      │
        ▲      │ 产生 tool 消息                                        │
        └──────┴──────────────────────────────────────────────────────┘
                                       │ 循环结束
                                       ▼
                ┌──────────────────────────────────────────────┐
                │  生成账单回执 (Receipt) 并拼接触发回复发送      │
                └──────────────────────────────────────────────┘
```

---

## 三、核心模块与接口设计

### 3.1 计费客户端（`internal/billing/client.go`）

封装与 Alcyone 计费微服务的交互，支持超时控制、重试与标准错误解析：

```go
type Client interface {
    Balance(ctx context.Context, platform, externalID string) (*BalanceResponse, error)
    ReserveLLM(ctx context.Context, req ReserveLLMRequest) (*ReserveLLMResponse, error)
    CommitLLM(ctx context.Context, reservationID string, actualMinor int64) (*CommitLLMResponse, error)
    ReleaseLLM(ctx context.Context, reservationID string, reason string) (*ReleaseLLMResponse, error)
}
```

- **错误类型**：
  - `ErrInsufficientFunds`（余额不足，HTTP 402）
  - `ErrNotFound`（用户或预扣款记录不存在，HTTP 404）
  - `ErrIdempotencyConflict`（幂等冲突，HTTP 409）
  - `ErrReservationExpired`（预扣款超时已自动释放，HTTP 410）
  - `ErrReservationTerminal`（预扣款已结算或已释放，HTTP 409）

### 3.2 定价表与成本计算（`internal/billing/pricing.go`）

- 维护模型定价映射表，支持环境变量/远程动态配置与默认回退机制。
- 计算公式：
  $$\text{CostMinor} = \left\lceil \frac{\text{PromptTokens} \times P_{\text{prompt}} + \text{CompletionTokens} \times P_{\text{completion}}}{1,000,000} \right\rceil$$
- 保证若模型有定价且发生调用，单次结算开销至少为 1 厘（0.01 雪花）。

### 3.3 本地快速分词与 Token 估算器（`internal/billing/tokenizer.go`）

- 专为 CJK（中文/日文/韩文）、英文单词、数字、标点符号与 Emoji 设计的高性能轻量分词估算器。
- 结合 ChatML 结构开销（消息头 `<|im_start|>`、工具调用元数据）进行预占 Token 量估算：
  $$\text{ReservePromptTokens} = \lceil \text{EstimatedPromptTokens} \times \text{SafetyMultiplier} \rceil$$
  $$\text{ReserveTokens} = \text{ReservePromptTokens} + \text{MaxOutputTokens}$$

---

## 四、安全与防护策略

### 4.1 会话上下文裁剪与完整性（`MaxHistory = 50`）
- 会话历史保留最近 50 条消息。
- 历史裁剪遇到 `role == "tool"` 消息时，自动向前追溯并保留关联的 `assistant (tool_calls)` 消息，防止破坏模型调用上下文结构。

### 4.2 工具循环计费（Per-Iteration Loop Billing）
- 每一轮 LLM 交互均独立执行 `Reserve -> Chat -> Commit / Release`。
- 多轮交互的总消耗与 Token 用量在 `BillingRunState` 中累计，并在会话最终回复末尾生成合并账单回执。

### 4.3 视觉前置边界防护（Vision Boundary Check）
- 在解析消息段包含图片时，先检查用户余额。若余额 $\le 0$，直接中断并提示充值，避免无效产生图片下载流量与视觉模型消耗。

### 4.4 消息尺寸与工具输出硬限制
- 单条用户消息长度超过 30,000 字符时，前置拦截拒绝处理。
- 单次上下文估算超过 128,000 Token 时，快速失败拒绝调用。
- 工具执行结果超过 64KB (`MaxToolOutputBytes`) 时，进行安全尾部截断并附带 `...[tool output truncated: exceeds 65536 bytes]` 标记。

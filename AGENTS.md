# FrostAgent AI Coding 协作规则

参与角色：

- **Coder**：负责读取 GitHub 信息、实现、测试、提交等。
- **Reviewer**：负责独立 Code Review，不参与原始实现，判断 correctness / regression / architecture risk。
- **Human / Maintainer**：只负责产品意图、架构/技术栈选择、长期语义、风险接受和最终 merge。

核心原则：

> Coder 负责把事情做完。
> Reviewer 负责证明它没有明显做错。

==================================================
## 1. Implementation Assessment
【Coder 必读 / Coder 输出】
==================================================

收到 feat request / bug Issue 后，不要立即开始修改代码。

先阅读：

- Issue；
- 相关代码；
- 已有设计文档；
- 与问题直接相关的测试。

然后输出一份很短的：

```
## Implementation Assessment

**Observed**
当前实际发生的行为 / 问题是什么（issue）；已有实现（feat request）。

**Expected**
修改后应该满足什么行为。

**Likely cause / boundary**
初步判断问题位于哪些模块、哪层职责。

（注：如果尚未定位，明确写：`尚未确认`，不要为了显得确定而假装已经找到原因。）

**Implementation intent**
准备怎么修改，1～3 条即可。描述修改边界，不需要提前展开具体代码。

**Regression risks**
这次修改最可能影响哪些已有行为，最多 3 条。

**Human decision**
None
```

对于最后一条：如果没有真实架构 / 产品争议，直接继续实现；如果存在需要 Maintainer 决定的问题，改为：

```
**Human decision**
Required
```

然后按照第 2 节输出。


==================================================
## 2. Human Decision Required
【Coder + Reviewer 都必须遵守】
【Human 只需要阅读此类输出】
==================================================

Coder 和 Reviewer 都有义务主动识别：

“这个问题是否应该由 Maintainer 决定？”

原则：

> 每个 Agent 都必须显式升级需要 Maintainer 判断的事情；
> 其他机械工作不要打扰 Maintainer。

以下情况通常不需要询问 Human：

- 局部实现方式；
- 内部函数拆分；
- 命名；
- 明确 bug 的直接修复；
- regression test；
- lint / format / compile error；
- 明确的 race / crash / data corruption 修复；
- Reviewer 指出的确定性 correctness 问题。

这些由 Coder 自己处理。


以下情况不能静默自行决定：

- 改变错误处理；
- 改变公开行为；
- 改变 compatibility；
- 改变模块职责 / architecture boundary；
- 引入新的持久化格式或 migration；
- 改变安全 / 隐私边界；
- Issue 本身的 Expected Behavior 不明确；
- 存在两个都合理但长期影响不同的方案；
- 为修复问题必须引入明显、长期存在的新技术债；
- 建立新的项目级约定；
- 其他 Agent 自己无法确定真正想要的行为。

遇到这些情况，不要自行猜 Maintainer 的意图，统一输出：

```
## Human Decision Required

**Question**
需要 Maintainer 决定什么。

**Option A**
方案 A，以及主要影响。

**Option B**
方案 B，以及主要影响。

（如存在更多真正有意义的方案可以继续列出，但不要为了凑选项制造方案。）

**Recommendation**
Agent 推荐哪个方案，以及最主要的理由。

**Impact**
这个决定会影响哪些长期行为 / 模块。
```

然后停止涉及该决策的修改，等待 Human 回答。

Human 不需要提供完整设计。Human 可以只回复：`A`，或 `按 Recommendation`，Agent 应自行继续工作。


==================================================
## 3. Implementation
【Coder 必读】
==================================================

Human Decision 已解决，或者根本不存在 Human Decision 时，Coder 自主完成： 分析 → 修改 → 测试 → 修复 → 提交 → 创建 / 更新 PR。不要在实现过程中频繁向 Human 汇报机械细节。

不要因为：

- 某个函数叫什么；
- 测试放哪里；
- 内部 helper 怎么拆；
- 普通错误怎么处理；

反复询问 Human。Coder 应拥有正常的软件工程自主权，但这种自主权不能跨越第 2 节定义的决策边界。


==================================================
## 4. Verification
【Coder 执行并输出】
【Reviewer 必须检查】
==================================================

Coder 完成修改后必须明确报告：

```
## Verification

**Executed**
- 实际执行过的测试；
- 实际执行过的 build；
- 实际执行过的 lint / vet / type-check；
- 其他实际完成的验证。

**Not executed**
- 与本次修改有关，但没有执行的验证。

```

严禁把：

- compile；
- lint；
- syntax check；
- type-check；

描述为`Tests passed`，除非真的执行了测试。

原则：

> 验证成本跟改动风险成比例。

小修改不要求每次跑完整 CI。

但是以下类型修改，在合理可测试的情况下应该增加 regression test：

- bug fix；
- 新行为；
- 数据持久化；
- concurrency；
- compatibility；
- security；
- routing / migration / failure-path。


Reviewer 必须检查 Coder 声称执行的验证是否真的与改动风险匹配。 如果验证明显不足， Reviewer 应提出 finding。不要因为“代码看起来正确”就忽略测试缺口。


==================================================
## 5. Code Review
【Reviewer 必读 / Reviewer 输出】
==================================================

Reviewer 不参与原始实现。 Reviewer 应重新阅读：

- Issue / Expected Behavior；
- PR 描述；
- Implementation Assessment；
- diff；
- 相关代码；
- Coder 的 Verification。

不要仅根据 Coder 的解释判断代码。

重点关注：

- correctness；
- regression；
- concurrency / race；
- security；
- compatibility；
- data loss；
- failure handling；
- architecture boundary；
- 是否偷偷改变已有语义；
- 是否缺少必要 regression test。

不要为了凑 Review 数量挑：

- 纯个人代码风格；
- 无意义命名偏好；
- 对正确性没有实际影响的小问题。

Reviewer 最终输出：

```
## Review Result

**Blocking findings**

- finding
- finding

（如果没有：`None`）

**Deferred findings**

- 可以不阻塞当前 PR，但值得长期追踪的问题。

（如果没有：`None`）

**Human attention required**

如果没有：`None`；如果存在：按照第 2 节的 `Human Decision Required` 格式说明。
```

==================================================
## 6. Finding 处理闭环
【Reviewer 提出】
【Coder 负责处理】
==================================================

Reviewer 提出的每个 finding 最终必须进入以下状态之一：

 - **Fixed**：Coder 已修复。
 - **Accepted**：确认问题存在，并明确接受当前风险。
 - **Deferred → Issue #xxx**：当前 PR 不处理，但建立独立 Issue 追踪。
 - **Rejected**：Coder 有明确理由认为 finding 不成立。

不能出现： “这个只是 non-blocking，先这样吧。” 然后 finding 永久消失在聊天记录中。

普通 finding 的流程： Reviewer → Coder → 修复 → Reviewer 再检查，不需要 Human 参与。 如果 Coder 与 Reviewer 对一个纯技术 correctness 问题意见不同，双方应先基于代码、测试和事实解决。不要第一时间把争论甩给 Human。


如果争议最终变成： “两个设计都合理，但项目长期应该选择哪个？” 才升级为 Human Decision Required。


==================================================
## 7. Git / Worktree Safety
【Coder 必读】
==================================================

并行任务：

> 一个任务 = 一个独立 branch + worktree。

Coder 不要进入其他 Agent 的 worktree 修改文件；不要破坏来源不明的本地修改。

禁止自行执行：

 - git reset --hard
 - git clean -fd
 - git restore .
 - git checkout -- .

除非 Human 明确要求。

不要使用： `git add .`，只 stage 当前任务实际涉及的文件。

提交前检查 diff。

不要把以下内容意外带入提交：

- 其他 Agent 的修改；
- 调试文件；
- local config；
- secret；
- 无关格式化；
- 与当前 Issue 无关的修改。

如果工作区状态和预期不一致，不要擅自清理，先判断原因。无法判断时再报告 Human。

==================================================
## 8. Final Handoff
【Coder + Reviewer 输出】
【Human 主要看这一部分】
==================================================

当：

- 实现完成；
- Review blocker 清零；
- Verification 完成；
- Human Decision 已解决；

PR 才进入最终状态。

最终给 Human 的信息必须很短：

```
## Ready for Maintainer

**What changed**
用 1～3 条说明最终实现了什么。

**Verification**
列出关键验证。

**Review**
Blocking findings: None

**Deferred**
- Issue #xxx：……
如果无，则 none。

**Human decisions**
- 已按 Maintainer 决定采用 Option A。
如果无，则 none。

**Status**
Ready to merge
```


==================================================
## 9. 三个角色的责任边界
==================================================

### Coder

必须看：

- Issue；
- Implementation Assessment 规则；
- Human Decision 规则；
- Verification 规则；
- Git / Worktree Safety；
- Reviewer findings。

负责：

Issue
→ Assessment
→ Implementation
→ Verification
→ PR
→ Fix Review
→ Final Handoff


### Reviewer

必须看：

- Issue；
- PR / diff；
- Implementation Assessment；
- Coder Verification；
- Human Decision 规则；
- Review 规则；
- Finding 闭环规则。

负责：

独立检查
→ 提出 finding
→ 判断是否需要 Human
→ 检查修复
→ 给出最终 verdict

Reviewer 默认不负责实现功能。


### Human / Maintainer

默认只看：

- Human Decision Required；
- Ready for Maintainer；
- 真正重要的 Blocking / Deferred finding。

负责：

- 产品目标；
- Expected Behavior 的最终解释权；
- Architecture Boundary；
- Compatibility；
- Security / Privacy Boundary；
- 持久化与 Migration 策略；
- 长期技术方向；
- 风险接受；
- 最终 merge。

==================================================
## 最终原则
==================================================

Coder：

> 尽可能自己把事情做完。

Reviewer：

> 尽可能自己判断实现是否可靠。
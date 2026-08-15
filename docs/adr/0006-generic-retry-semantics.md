# ADR-0006：通用错误分类与重试语义

- 状态：已接受，已实现
- 日期：2026-07-28

## 背景

TaskPulse 当前只在 Worker 租约过期时使用 `RetryCount` 和 `MaxRetries`。
Executor 返回普通 `error` 后，Worker 会直接将任务置为 `failed`，无法区分：

- 网络超时、服务端 5xx 和限流等临时错误；
- 非法输入、鉴权失败和能力不存在等永久错误；
- 服务端明确给出的 `Retry-After`；
- 用户取消和任务超时。

这不是 URL 抓取特有的问题。LLM Provider、外部 Skill、浏览器工具和其他远程服务
都可能出现临时故障。如果每个 Executor 自己循环重试，将无法统一持久化状态、
崩溃恢复、取消、事件、指标和重试预算。

现有实现还有一个语义隐患：`NewTaskClaimedEvent` 根据 `RetryCount > 0`
判断任务是否由租约过期恢复。实现普通错误重试后，正常重试任务的
`RetryCount` 也会大于零，因此不能继续依赖该条件判断领取原因。

## 决策

### 1. 重试是 TaskPulse 的通用调度能力

Executor 负责识别具体错误，TaskPulse 负责决定任务状态和下次执行时间：

```text
Executor
→ 返回成功结果或分类后的 ExecutionError
→ Worker 应用 RetryPolicy
→ TaskTransitionStore 原子保存状态与事件
→ 到达 available_at 后重新领取
```

URL、LLM 和 Skill Executor 使用同一套协议，不在 Worker 中编写特定业务判断。

### 2. 保留 RetryCount 和 MaxRetries

当前不重命名为 `AttemptCount/MaxAttempts`，避免同时修改 HTTP API、领域模型、
MySQL Schema、查询语句、测试和已有文档。

字段语义固定为：

```text
RetryCount：首次执行之外，已经消耗或预留的额外执行机会数量
MaxRetries：任务最多允许的额外执行次数
当前执行序号：RetryCount + 1
```

示例：

```text
MaxRetries = 3
首次执行                RetryCount = 0
第一次错误重试          RetryCount = 1
第二次错误重试          RetryCount = 2
第三次错误重试          RetryCount = 3
再次失败                不再调度，进入 failed/dead-letter
```

Worker 租约过期后的重新执行也会消耗一次相同的重试预算。TaskPulse 对外提供的是
至少一次执行语义，无法确定旧 Worker 是否已经产生副作用，因此恢复执行必须计入预算。

### 3. 定义通用执行错误

Worker 只识别以下稳定分类：

```go
type ErrorKind string

const (
    ErrorTransient ErrorKind = "transient"
    ErrorPermanent ErrorKind = "permanent"
)

type ExecutionError struct {
    Kind       ErrorKind
    Code       string
    RetryAfter time.Duration
    Err        error
}
```

- `Kind` 决定是否可以重试。
- `Code` 保存可观测但不绑定供应商的原因，例如 `rate_limited`、`upstream_5xx`、
  `network_timeout`、`invalid_input`、`unauthorized`。
- `RetryAfter` 是上游给出的最短等待建议；为零时使用本地退避策略。
- `Err` 保留内部错误链，写入任务和日志前必须避免泄露密钥、Prompt 或原始文档。

未分类的普通 `error` 默认视为永久错误。这样可以避免未知错误触发无限重试和故障放大。
每个 Executor 必须通过适配层把供应商错误转换为通用分类。

`context.Canceled` 不进入重试；它属于取消流程。执行超时是否可重试，由执行边界产生
明确的 `network_timeout` 或 `attempt_timeout` 分类，不能只根据错误字符串判断。

### 4. 使用持久化延迟调度

临时错误且仍有预算时，执行以下原子转换：

```text
BEGIN
→ running 转换为 retrying
→ RetryCount = RetryCount + 1
→ available_at = now + retry_delay
→ 清理当前租约
→ 写入 task_retrying 事件
→ COMMIT
```

领取查询扩展为同时选择：

```text
queued   且 available_at <= now
retrying 且 available_at <= now
```

任务进入 `retrying` 时就预留并增加 `RetryCount`；再次领取时不重复增加。
租约过期恢复仍在重新领取时增加 `RetryCount`。

### 5. 指数退避由策略控制

第一版采用有上限的指数退避，并使用 equal jitter：

```text
cap   = min(base_delay * 2^(RetryCount-1), max_delay)
delay = cap/2 + random(0, cap/2)
```

这样实际延迟不会超过本地 `max_delay`，也能避免大量任务在同一时刻重新请求。
如果上游返回 `RetryAfter`，实际等待时间不得短于该值。

重试策略属于 workflow 注册信息，而不是写死在 URL、LLM 或 Skill Executor 中：

```go
type RetryPolicy struct {
    MaxRetries int
    BaseDelay  time.Duration
    MaxDelay   time.Duration
}
```

创建任务时的 `max_retries` 必须受 workflow 策略上限约束，不能允许接入方任意制造
无限重试任务。

### 6. 显式记录领取原因

领取逻辑必须返回或内部保留 `ClaimKind`：

```text
initial  ：queued 首次领取
retry    ：retrying 到期后领取
recovery ：running 租约过期后接管
```

事件类型和消息根据 `ClaimKind` 生成，不能再根据 `RetryCount > 0` 推断：

```text
initial  → task_started
retry    → task_retry_started
recovery → task_recovered
```

### 7. 状态和事件保持原子

以下事实必须在一个事务中提交：

- `running → retrying` 与 `task_retrying`；
- `retrying → running` 与 `task_retry_started`；
- 重试预算耗尽后的 `failed` 与 `task_failed`。

事件至少记录：

```text
error_code
retry_count
max_retries
available_at
delay_ms
```

事件 payload 不保存完整敏感输入和未经处理的供应商响应。

## 备选方案

### Executor 内部自行重试

不采用。进程崩溃后重试进度丢失，TaskPulse 无法取消、观测或统一限制重试。

### 所有错误都重试

不采用。非法输入、鉴权失败和能力不存在不会因为等待而恢复，还会放大流量和费用。

### 立即改为 AttemptCount/MaxAttempts

暂不采用。概念更直观，但当前项目已经使用 `RetryCount/MaxRetries` 完成租约恢复和
API 接入。现阶段通过严格定义语义即可消除歧义；只有外部 API 经常误用时才重新评估。

### 使用 Redis 延迟队列

暂不采用。MySQL 已有 `available_at` 和领取索引，可以先建立正确性与性能基线。
只有压测证明轮询、锁竞争或延迟精度不满足要求后，再评估 Redis Streams 或专用队列。

## 代价与风险

- `TaskTransitionStore` 需要增加“调度重试并写事件”的事务方法。
- MySQL 查询需要同时处理 `queued`、`retrying` 和过期 `running`。
- 抖动会让测试不稳定，因此时间源和随机源必须可注入。
- 至少一次执行仍可能重复产生外部副作用，Executor 或上层业务必须提供幂等键。
- 重试会增加 LLM Token、外部 API 调用量和成本，必须按 workflow 设置上限。

## 验证标准

- 临时错误在预算内进入 `retrying`，到达 `available_at` 前不能被领取。
- 到达 `available_at` 后只能被一个 Worker 领取。
- 永久错误直接进入 `failed`。
- 未分类错误默认进入 `failed`。
- `RetryAfter` 大于本地退避时间时得到尊重。
- 重试预算耗尽后不再执行。
- Worker 在重试等待期间重启，任务仍能继续调度。
- 状态更新或事件写入任一步失败时，整个事务回滚。
- URL、Fake LLM 和 Fake Skill Executor 能使用同一错误类型和策略。

## 重新评估条件

- `RetryCount/MaxRetries` 在 API 使用中持续造成误解；
- 不同 workflow 需要不同预算模型，例如按费用而不是次数限制；
- MySQL 延迟领取在压测中成为主要瓶颈；
- Agent 任务需要步骤级重试，而不仅是整个任务级重试。

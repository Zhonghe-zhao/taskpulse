# 实验 21：执行时限与持续 Heartbeat 的边界

## 问题

Lease 表示 Worker 在一段时间内仍拥有执行权。只要 Heartbeat 正常，Lease 会持续延长。
因此 Lease 不能用来限制一次 LLM、网页抓取或第三方 API 调用的最长时间：一个卡住的调用
可能持续 Heartbeat，却永远不返回结果。

## 方案

TaskPulse Worker Runtime 增加可选的 `ExecutionTimeout`：

```text
Claim + Lease/Heartbeat
        与
ExecutionTimeout
        分别负责所有权和单次执行上界
```

当到达执行时限时：

```text
Runtime 取消 Executor Context
-> Runtime 立即 Fail(error_code=execution_timeout, retryable=true)
-> TaskPulse 使用原有 RetryPolicy 退避重试
```

这不是跨重试的总 Deadline，也不改变 MySQL 任务表；它是 Worker 对一次外部调用的本地防护。

## Compose 验证步骤

在 `E:\CS\TaskPulse` 中执行：

```powershell
$env:TASKPULSE_LLM_FAKE_DELAY = "30s"
$env:TASKPULSE_EXTERNAL_EXECUTION_TIMEOUT = "5s"
docker compose up -d --build --force-recreate llm-worker
docker compose logs -f llm-worker
```

创建一个普通的 `llm_analysis` 任务，然后查询事件：

```powershell
Invoke-RestMethod "http://127.0.0.1:8085/tasks/<task-id>" | ConvertTo-Json -Depth 10
Invoke-RestMethod "http://127.0.0.1:8085/tasks/<task-id>/events" | ConvertTo-Json -Depth 10
```

## 预期结果

- 每次尝试约 5 秒后停止，而不是等待 30 秒 fake LLM；
- Worker Fail 请求中的 `error_code` 为 `execution_timeout`；
- 事件出现 `task_retrying` 和 `task_retry_started`；
- `retry_count` 按现有策略增长，预算耗尽后任务进入 `failed`；
- Heartbeat 可以正常存在，但它不会掩盖执行时限。

实验结束后清空两个临时环境变量，恢复默认 Worker 行为：

```powershell
Remove-Item Env:TASKPULSE_LLM_FAKE_DELAY -ErrorAction SilentlyContinue
Remove-Item Env:TASKPULSE_EXTERNAL_EXECUTION_TIMEOUT -ErrorAction SilentlyContinue
docker compose up -d --force-recreate llm-worker
```

## 边界

Runtime 会在到达时限后停止 Heartbeat 并标记该尝试超时，即使 Executor 忽略 Go
`context.Context`。但 Go 进程无法安全强制终止那个阻塞的 goroutine；该 goroutine 仍可能继续
产生外部副作用，而重试 Worker 也可能开始新尝试。因此对这类依赖仍应设置自身的 HTTP/SDK 请求
超时，并在业务层保留幂等写回。

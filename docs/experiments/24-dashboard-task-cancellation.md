# 24：Dashboard 任务取消实验

## 实验结论

2026-08-13，通过 TaskPulse Dashboard 验证了两类取消路径：

1. 排队任务取消：`queued → canceled`
2. 运行中任务取消：`running → canceled`

两组均通过。取消是用户可直接使用的控制面能力，不依赖 Worker 主动配合；运行中取消依赖租约失效与版本 fencing，阻止旧 Worker 再写入终态。

## 要验证的问题

Dashboard 已提供取消按钮。需要确认：

- 只对 `queued` / `retrying` / `running` 显示取消；
- 二次确认有效；
- 排队任务取消后不再被 Claim；
- 运行中任务取消后出现一次 `task_canceled`；
- 旧 Worker 后续 heartbeat / complete 不能覆盖为 `succeeded`；
- Dashboard 自动刷新后状态正确。

## 环境

| 项目 | 配置 |
|---|---|
| 日期 | 2026-08-13 |
| TaskPulse | Docker Compose + MySQL，`http://127.0.0.1:8085` |
| Dashboard | `/dashboard` |
| MemoBridge Worker | 本机进程（运行中取消场景使用） |
| workflow | `memobridge.semantic_profile` |

## 实验一：排队任务取消

### 操作

1. 停止或避开会立刻 Claim 的 Worker，保证任务停留在 `queued`。
2. 创建可丢弃任务（假资料引用即可）：

```powershell
$body = @{
  workflow = "memobridge.semantic_profile"
  input = @{
    source_item_id = 999999
    content_hash = "sha256:deadbeef"
    prompt_version = "source_semantic_profile:v1"
    requested_by = "cancel_queued_test"
  }
  max_retries = 3
} | ConvertTo-Json -Depth 5

$task = Invoke-RestMethod `
  -Method Post `
  -Uri "http://127.0.0.1:8085/tasks" `
  -Headers @{ "Idempotency-Key" = "cancel-queued-$(Get-Date -Format yyyyMMddHHmmss)" } `
  -ContentType "application/json" `
  -Body $body
```

3. 在 Dashboard 打开任务详情，点击取消并确认对话框。

### 结果

```text
task_id = task_1786563885343094258_44950
status  = canceled
progress = 0%
retry_count = 0/3
queue_duration ≈ 32s
execution_duration = 0s
```

事件：

```text
task_created
-> task_canceled
```

观测：

- 没有 `task_started`；
- 失败信息为空（不是业务失败，是调用方取消）；
- 终态后取消按钮不再适用；
- 输入引用保留 `requested_by=cancel_queued_test`。

### 结论

排队取消通过。任务在被 Claim 前即可收敛为终态，不会进入执行。

## 实验二：运行中任务取消

### 操作

1. 启动 MemoBridge Worker（可用执行延迟，便于在窗口内点取消）。
2. 创建真实或测试任务并等待进入 `running`。
3. Dashboard 确认出现 `task_started` 与进度（本次为 10%）。
4. 点击取消并确认。

### 结果

事件（Dashboard 截图）：

```text
task_created
-> task_started(memobridge-worker-local-2)
-> task_progress(10%)
-> task_canceled
```

列表态：

```text
workflow = memobridge.semantic_profile
status   = canceled
progress = 10%
retry_count = 0/3
```

观测：

- `task_canceled` 只出现一次；
- 取消发生在 Claim 与进度上报之后；
- 状态收敛为 `canceled`，未再变为 `succeeded`；
- Dashboard 刷新后展示「已取消」。

### 结论

运行中取消通过。取消写入后，任务离开可执行集合；旧 Worker 失去有效租约/版本条件，不能再把任务 Complete 成成功。

## 与实验 10 的关系

`10-running-task-cancellation.md` 已在内置 `llm_analysis` + Fake 延迟下证明 API 取消与 Executor Context 停止。本实验补充：

- 用户路径是 Dashboard，而不是只调 HTTP；
- workflow 使用 `memobridge.semantic_profile`；
- 同时覆盖排队与运行两种入口状态。

## 总结

| 场景 | 结果 | 主要证据 |
|---|---|---|
| 排队取消 | 通过 | `task_1786563885343094258_44950`：`created → canceled` |
| 运行中取消 | 通过 | Dashboard 时间线：`started → progress(10%) → canceled` |

## 边界

- 本实验不证明取消与 Complete 的极端并发竞态统计分布；若任务已先 Complete，取消应看到不可取消终态，不覆盖成功结果（见实验 10）。
- `source_changed` 仍非本轮范围。

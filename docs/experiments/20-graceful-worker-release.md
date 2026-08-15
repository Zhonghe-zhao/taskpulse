# 实验 20：Worker 优雅停机与主动归还 Lease

## 问题

Worker 正在执行长耗时 LLM 任务时，Kubernetes 滚动更新、缩容或容器停止会发送
`SIGTERM`。旧方案中 Worker 停止心跳后，任务仍是 `running`，必须等待完整 Lease 到期
才能由其他 Worker 恢复。这个等待既延迟任务完成，也会把一次正常运维动作误当作故障恢复。

## 假设

若 Worker 能在收到停止信号后携带当前 `lease_token` 和 `version` 主动归还任务，
TaskPulse 可将任务原子地从 `running` 转为 `queued`，清除 Lease 并写入审计事件；其他
Worker 不必等待 Lease 到期即可重新领取。

## 实现

```text
SIGTERM
  -> Runtime 停止 Claim
  -> 取消 Executor Context
  -> 最多等待 TASKPULSE_EXTERNAL_SHUTDOWN_TIMEOUT
  -> POST /worker/tasks/{id}/release
  -> MySQL 原子写入 queued + task_released
  -> 健康 Worker Claim 并继续执行
```

`release` 的约束：

- 仅持有有效 Lease 的 Worker 可以调用；
- 使用 token/version fencing，旧 Worker 不能覆盖新 Owner；
- 重复调用返回已释放任务，不重复写事件；
- 不增加 `retry_count`；
- 强制 Kill 或 release 失败时，仍回退到既有的 Lease 过期恢复。

## Compose 验证步骤

先用两个 Worker 和较长的 fake LLM 执行时间启动环境：

```powershell
$env:TASKPULSE_LLM_FAKE_DELAY = "30s"
docker compose up -d --build --scale llm-worker=2
docker compose ps
```

创建一个 `llm_analysis` 任务，确认某个 Worker 已输出 `task claimed`。找到该 Worker
容器后对它发送正常停止信号：

```powershell
docker compose ps -q llm-worker
docker stop --time 10 <worker-container-id>
```

查询任务事件：

```powershell
Invoke-RestMethod "http://127.0.0.1:8085/tasks/<task-id>/events" | ConvertTo-Json -Depth 10
```

## 预期证据

事件顺序应包含：

```text
task_created
-> task_started
-> task_released
-> task_started
-> task_succeeded
```

关键判断：

- `task_released` 出现在原 Lease 到期前；
- 第二次 `task_started` 由另一个 Worker 领取；
- `retry_count` 没有因 release 增加；
- 不出现 `task_recovered`，因为这不是崩溃恢复路径。

Kubernetes 下同样适用：`llm-worker` 的 `terminationGracePeriodSeconds=10`，而
`TASKPULSE_EXTERNAL_SHUTDOWN_TIMEOUT=5s`，为 release HTTP 调用保留了关闭宽限。

## 边界

Worker 在外部副作用已完成、但 release/complete 前被强制杀死时，系统仍只能提供
at-least-once 执行。MemoBridge 等业务 Worker 必须使用自己的幂等写回约束处理重复执行。

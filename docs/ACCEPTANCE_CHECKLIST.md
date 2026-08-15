# TaskPulse 最终验收清单

这份清单的目的不是把项目包装成已经完成，而是把“代码已经保证的能力”和“必须由运行环境证明的能力”分开。

## 已由代码和测试证明

- 公共 Go Client：创建、查询、取消、Claim、Heartbeat、Progress、Complete、Fail；
- 公共 Worker Runtime：按 workflow 注册 Executor、轮询、租约续期、失败分类、单次执行时限、优雅退出和主动 Release；
- MySQL 任务状态、TaskEvent、workflow + 幂等键、并发 Claim、Lease Token、版本 fencing；
- Complete/Fail 请求丢失后的幂等重放；
- 可重试失败、退避、最终失败和过期 Lease 恢复；
- 任务进度、结构化日志和 Prometheus 指标；
- 外部 Worker 与内置 Worker 都会记录 Claim、主动 Release、完成、重试、Lease 续期等指标；
- MemoBridge 只传递 `source_item_id`、`content_hash` 和 `prompt_version`，不共享数据库或资料正文；
- `go test ./...`（TaskPulse）和 `go test ./cmd/memobridge-worker` 已通过。

## 你需要实际运行的三组证据

### 1. Compose 真实联调

```powershell
docker compose -f compose.integration.yaml build
docker compose -f compose.integration.yaml up -d
.\scripts\smoke-memobridge-integration.ps1
```

通过条件：重复提交返回同一个 Task ID；TaskPulse 为 `succeeded`；事件完整；MemoBridge 可以读取 SemanticProfile；两个外部 Worker Prometheus 计数增长。

从冒烟脚本输出中取得 `task_id` 后，保存 TaskPulse 侧的原始证据：

```powershell
.\scripts\capture-task-evidence.ps1 -TaskID <task_id> -Label memobridge-compose-success
```

### 2. Worker 崩溃恢复

```powershell
$env:MEMOBRIDGE_TASKPULSE_EXECUTION_DELAY = "45s"
$env:TASKPULSE_LEASE_DURATION = "30s"
docker compose -f compose.integration.yaml up -d --build memobridge-worker
```

在任务被 MemoBridge Worker Claim 后，使用下面的命令终止正在执行的容器：

```powershell
docker compose -f compose.integration.yaml kill memobridge-worker
docker compose -f compose.integration.yaml logs -f memobridge-worker
```

`restart: unless-stopped` 会拉起替代 Worker。等待 Lease 过期后，检查 TaskPulse Event 中是否出现：

```text
task_created -> task_started -> task_recovered -> task_succeeded
```

`task_recovered` 本身就是替代 Worker 的 recovery claim 审计事件，不会额外再写一次
`task_started`。这证明任务状态属于 TaskPulse/MySQL，而不是属于某个短暂的 Worker 进程。

恢复成功后保存任务状态、事件与指标：

```powershell
.\scripts\capture-task-evidence.ps1 -TaskID <task_id> -Label memobridge-worker-recovery
```

在前述长延迟环境中，也可直接用自动脚本完成相同验收：

```powershell
.\scripts\smoke-memobridge-crash-recovery.ps1
```

### 3. Worker 优雅交接

在两个带有测试延迟的 MemoBridge Worker 上运行：

```powershell
$env:MEMOBRIDGE_TASKPULSE_EXECUTION_DELAY = "45s"
docker compose -f compose.integration.yaml up -d --build --scale memobridge-worker=2
.\scripts\smoke-memobridge-graceful-handoff.ps1
```

通过条件：任务事件为 `task_created -> task_started -> task_released -> task_started -> task_succeeded`；
`retry_count` 保持 `0`，没有 `task_recovered`，并产生 `taskpulse_tasks_released_total` 指标。

### 4. MySQL 调度基线

```powershell
.\scripts\capture-mysql-baseline.ps1 -Label before-mysql-10000-w32-p200
go run ./cmd/dispatch-benchmark `
  --tasks 10000 `
  --create-workers 32 `
  --status-poll-interval 200ms `
  --output .\artifacts\benchmarks\mysql-10000-w32-p200.json `
  --timeout 30m
.\scripts\capture-mysql-baseline.ps1 -Label after-mysql-10000-w32-p200
```

保存 `artifacts/benchmarks` 和 `artifacts/mysql` 中的文件。它们是是否引入 Redis 的决策证据。

## Redis 决策

当前结论是“尚不引入”，不是“永远不用”。只有在同一工作负载下同时看见以下信号时，才进入 Redis Streams 方案：

- 任务发现 p95 或吞吐不满足目标；
- 空 Claim 比例高，并且 MySQL 资源、连接或锁等待表明确实成为压力来源；
- Redis 作为 Outbox 驱动的通知层，能在相同任务量下降低该瓶颈；
- Redis 不可用时仍能由 MySQL Outbox 和扫描恢复，不丢任务。

如果基准显示任务耗时或 LLM Provider 才是主瓶颈，就保留 MySQL 调度，并在报告中说明 Redis 不解决那个问题。

## 面试时的真实边界

TaskPulse 保证的是“持久任务状态 + 单个有效 Lease + 至少一次执行尝试”。它不声称外部 LLM 调用绝对只发生一次。业务方需要像 MemoBridge 一样用 `source_item_id + content_hash + prompt_version` 做幂等写回，处理“业务写回成功但 Complete 响应丢失”这类不可消除的分布式边界。

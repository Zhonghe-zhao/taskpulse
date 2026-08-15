# 实验文档规范

实验文档负责保存工程证据，不负责替代架构说明。

每份实验至少记录：

```text
问题：观察到了什么风险或失败？
假设：认为原因是什么？
环境：版本、配置、并发量和数据规模。
步骤：怎样稳定复现？
指标：记录哪些数值？
结果：实际发生了什么？
结论：方案是否有效？
边界：哪些情况仍未解决？
```

建议文件名：

```text
NN-short-experiment-name.md
```

压测和故障恢复实验应保存命令、关键输出和结论，避免只留下“测试通过”。

## 当前实验

`12-kubernetes-worker-crash-recovery.md`：Kubernetes Pod 自动恢复与 TaskPulse 租约恢复的协同实验。

`13-kubernetes-worker-scaling.md`：Kubernetes Worker 副本扩展与任务吞吐量对比实验。

`20-graceful-worker-release.md`：Worker 收到 SIGTERM 后主动归还 Lease，避免等待 Lease 到期。

`21-execution-timeout.md`：单次 Executor 执行时限与 Heartbeat 的职责边界。

`22-kubernetes-graceful-worker-handoff.md`：Kubernetes 正常终止 Worker Pod 时的主动 Lease 交接。

`23-memobridge-real-llm-reliability.md`：MemoBridge 真实 DeepSeek 负载下的成功、幂等、重试、优雅交接和 Worker 崩溃恢复实验。

`24-dashboard-task-cancellation.md`：Dashboard 排队取消与运行中取消，用户路径下的 `task_canceled` 证据。

`25-complete-idempotent-replay.md`：Complete 响应丢失后的同凭证重放，证明成功事件只落一次。

`../integrations/docker-compose-integration-runbook.md`：Compose 下 MemoBridge 真实任务闭环、崩溃恢复和自动化优雅交接验收。

- `01-memory-task-store.md`：内存任务存储的并发保护和复制边界。
- `02-task-service.md`：应用层创建任务和事件的用例编排。
- `03-llm-analysis-workflow.md`：第二类 workflow 的执行闭环。
- `04-observability-baseline.md`：结构化日志和 `/metrics` 基线。
- `05-retry-observability-experiment.md`：LLM 限流重试的事件和指标证据。
- `06-worker-crash-recovery.md`：Worker 崩溃后的租约恢复和版本隔离。
- `07-multi-worker-claim.md`：多个 Worker 并发领取任务和任务互斥。
- `08-multi-process-claim.md`：多个 TaskPulse 进程共享 MySQL 队列的验证边界。
- `09-worker-throughput-baseline.md`：不同 Worker 数量下的吞吐和队列等待基线。
- `10-running-task-cancellation.md`：运行中任务取消和 Executor Context 停止。
- `11-external-worker-protocol.md`：外部 Worker 领取、续租和结果提交协议。
- `17-mysql-dispatch-core-matrix.md`：分发参数含义与 2/4/8 Worker、delay 0/100ms 核心矩阵结论。
- `23-memobridge-real-llm-reliability.md`：MemoBridge 真实 LLM 可靠执行联调总报告。
- `24-dashboard-task-cancellation.md`：Dashboard 排队/运行取消。
- `25-complete-idempotent-replay.md`：Complete 幂等重放。

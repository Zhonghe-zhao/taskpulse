# TaskPulse MVP 路线图（历史演进记录）

> 注意：本文记录项目早期的实施路线，不是当前能力的权威来源。当前代码状态见 [PROJECT_STATUS.md](PROJECT_STATUS.md)，最终验收见 [ACCEPTANCE_CHECKLIST.md](ACCEPTANCE_CHECKLIST.md)，当前架构见 [ARCHITECTURE.md](ARCHITECTURE.md)。本文中的未勾选项不自动代表当前功能缺失。

- 文档状态：执行中
- 最近更新：2026-07-30
- 当前里程碑：MySQL 持久化

## MVP 目标

完成一个可持久化、可恢复、可观测的异步任务执行系统，并通过 URL 检测和 Agent/LLM 长任务验证通用执行能力。

项目按问题驱动演进。路线图中的“计划”不能在 README 或面试介绍中描述为已实现。

## 已完成：内存执行闭环

- [x] Task 与 TaskEvent 领域模型
- [x] 任务状态机
- [x] MemoryTaskStore 与 MemoryEventStore
- [x] 单进程原子 ClaimNext
- [x] 创建任务、查询任务、查询事件 HTTP API
- [x] Application 层用例编排
- [x] Worker 与 Executor 注册机制
- [x] URL Check Executor
- [x] URL 级有界并发
- [x] 成功、部分成功和失败结果
- [x] 单元测试与 HTTP 测试

## 里程碑一：MySQL 持久化

问题：内存 Store 在进程重启后丢失任务，无法协调多进程 Worker。

- [x] 编写 MySQL Schema 和初始化迁移
- [x] 实现 MySQL 配置校验和连接池模块
- [x] 增加环境配置加载和可选 MySQL 集成测试
- [ ] 在 Compose MySQL 上运行并通过连接集成测试
- [x] 实现 MySQLTaskStore.Create/Get/Update
- [x] 实现 MySQLTaskStore.ClaimNext
- [x] 实现 MySQLEventStore.Append/ListByTaskID
- [x] 在同一事务中创建 Task 和 Created Event
- [x] 将 MySQL Store 接入 API、Worker 与 Reaper 运行链路
- [x] 在同一事务中提交 Worker 任务终态与终态事件
- [x] 在同一事务中领取任务并写入 started/recovered Event
- [x] 在同一事务中清理过期任务并写入 failed Event
- [x] 使用唯一 `idempotency_key` 防止重复创建
- [x] 使用 `FOR UPDATE SKIP LOCKED` 领取任务
- [ ] 增加数据库集成测试
- [ ] 实验：重启服务后任务仍可查询
- [ ] 实验：多个 Worker 只能领取一次任务

决策依据：[ADR-0001](adr/0001-use-mysql-as-system-of-record.md)、[ADR-0007](adr/0007-idempotent-task-creation.md)

## 里程碑二：可靠执行

问题：Worker 崩溃、临时错误和取消请求尚未被正确处理。

- [x] Worker 租约、心跳续租与过期重新领取
- [x] 重试额度耗尽后的过期任务清理
- [x] 明确通用错误分类、重试预算与退避语义（ADR-0006）
- [x] 实现 ExecutionError、RetryPolicy 与可测试的 equal-jitter 退避计算
- [x] 将 available_at 纳入领域模型，定义 ScheduleRetry，并统一 Memory/MySQL 延迟领取语义
- [x] 原子保存 running → retrying 与 task_retrying Event
- [x] 使用 ClaimKind 区分首次领取、错误重试与租约恢复
- [x] 到期的 retrying 任务可被原子领取并写入 task_retry_started
- [x] Worker 根据临时错误/永久错误分类选择重试或失败
- [x] 指数退避和最大重试次数
- [ ] 死信终态
- [x] 取消 queued/retrying 任务 API，并原子写入 canceled Event（ADR-0008）
- [x] 运行中任务的取消请求与 Context 协作式取消
- [x] 条件更新和版本号防止并发状态覆盖
- [ ] 实验：杀死 Worker 后任务恢复
- [ ] 实验：重复执行不产生错误终态

## 里程碑三：Agent 场景

问题：需要证明 TaskPulse 不依赖 URL 业务，并能承载耗时、受限流影响的智能任务。

- [x] 定义 `llm_analysis` 输入输出
- [x] 定义可替换的 LLM Client 接口
- [x] 实现 Fake Client 测试
- [ ] 接入一个真实模型 Provider
- [ ] 记录模型、Token、耗时和错误类型
- [x] 定义 429、5xx 和超时的通用错误分类
- [ ] 限制 Agent/LLM 任务并发量

第一版不做动态工作流、多 Agent 协作、任意代码执行和插件市场。

## 里程碑四：工程证据

- [x] Worker/Reaper 结构化日志
- [x] Prometheus 文本格式 `/metrics`
- [x] 执行耗时、完成状态、重试、租约和 Reaper 清理指标
- [x] 队列积压和最老可领取任务等待时间指标
- [ ] 成功率聚合指标
- [ ] Dockerfile 与 Docker Compose
- [ ] k6 压力测试
- [ ] P50、P95、P99 报告
- [ ] pprof 分析
- [ ] 故障实验报告

## 可选里程碑：Redis Streams

Redis Streams 不是 MySQL 持久化的前置条件。满足以下条件后再新增 ADR：

- MySQL 队列基线已经完成；
- 压测发现轮询或锁竞争是主要瓶颈，或者 Agent 任务需要独立消费组和低延迟唤醒；
- 已理解 MySQL 与 Redis 双写问题；
- 准备使用 Outbox Relay，而不是在请求中直接双写。

候选能力：Consumer Group、`XACK`、Pending Entries、`XAUTOCLAIM` 和死信 Stream。

## 可选里程碑：Kubernetes

在 API/Worker 拆分、持久化和崩溃恢复完成后再进行：

- [ ] API Deployment 与 Service
- [ ] Worker Deployment
- [ ] ConfigMap 与 Secret
- [ ] readiness/liveness 探针
- [ ] 优雅终止
- [ ] Pod Kill 后任务恢复实验

Kubernetes 重启 Pod 不等于任务恢复；任务恢复必须由 TaskPulse 的持久化、租约和幂等保证。

## MVP 完成标准

MVP 不是“技术栈都启动成功”，而是以下证据全部成立：

```text
任务可以创建并异步执行
→ 重启不丢失
→ 多 Worker 不会同时领取
→ 临时失败可以重试
→ Worker 崩溃可以恢复
→ 重复请求和重复执行具有明确幂等语义
→ 指标能够解释积压、延迟和失败
→ 文档能说明每项技术的原因、边界和验证结果
```

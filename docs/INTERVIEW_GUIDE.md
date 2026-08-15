# TaskPulse 秋招讲解稿

## 一句话定位

TaskPulse 是面向长耗时、依赖外部服务且容易失败任务的可靠异步执行运行时。它管理任务生命周期，不理解上层业务，也不是 LLM 或消息队列的替代品。

## 为什么需要它

同步 HTTP 请求无法可靠承载以下工作：

- LLM 调用可能持续数十秒或数分钟；
- 外部服务可能超时、限流或暂时不可用；
- Worker 进程可能在执行中崩溃；
- 用户需要查询进度、失败原因和重试状态。

TaskPulse 把业务请求快速转换为持久化任务，由独立 Worker 执行，并通过状态机、租约、心跳、重试、幂等和事件记录管理整个生命周期。

## 架构边界

```text
MemoBridge API
    -> TaskPulse Client
    -> TaskPulse MySQL
    -> MemoBridge Worker Runtime
    -> MemoBridge PostgreSQL / LLM
```

MemoBridge 负责 SourceItem、Prompt、LLM、结果校验和 SemanticProfile 写回。

TaskPulse 负责 Task、TaskEvent、Claim、Lease、Heartbeat、Retry、Cancel、Progress、Recovery 和 Prometheus 指标。

TaskPulse 不访问 MemoBridge 数据库，也不保存资料全文、Prompt 或完整模型输出。

## 最关键的可靠性语义

### Lease 和 Fencing

Worker Claim 后获得带版本的租约。Heartbeat 续租；Worker 崩溃后租约过期，其他 Worker 才能重新 Claim。每次 Complete、Fail、Progress 都校验 Worker、LeaseToken 和 Version，旧 Worker 的请求会被拒绝。

### 为什么不是 Exactly-Once

外部 LLM 调用无法被数据库事务包住。Worker 可能已经写回业务数据库，但还没来得及向 TaskPulse Complete 就崩溃。因此系统提供至少一次执行尝试和单个有效租约，而不是承诺外部副作用绝对只执行一次。

MemoBridge 用 `source_item_id + content_hash + prompt_version` 做幂等写回，避免重试覆盖新资料或产生重复 SemanticProfile。

### 为什么 Complete 要幂等

Complete 请求可能已经提交到数据库，但 HTTP 响应在返回途中丢失。Worker 重发同一个 LeaseToken 时，TaskPulse识别已完成的版本并返回原状态，不重复追加事件或修改结果。

## 为什么先用 MySQL

任务状态、租约、重试计数和事件必须持久化，并且 Claim 需要事务和行锁。MySQL 的 `FOR UPDATE SKIP LOCKED` 能直接提供并发领取语义，系统复杂度较低。

Redis 只有在压测证明 MySQL 的轮询、Claim 锁竞争或任务发现延迟成为瓶颈后才引入。Redis 即使引入，也只适合作为通知/分发层，MySQL 仍保存任务事实和最终状态。

## 实验结论

已有 40 个任务的固定延迟实验：

```text
2 Worker: 601.595 秒，0.06649 task/s
4 Worker: 300.993 秒，0.13289 task/s
```

Worker 数翻倍后吞吐近似翻倍，说明该负载主要受任务执行时间限制，而不是已经证明 MySQL 调度成为瓶颈。后续用 dispatch benchmark 补充 Claim 次数、空 Claim 比例、队列 p95、MySQL CPU、连接数和锁等待。

## 面试中的技术链路

```text
真实长任务
  -> 同步请求不可靠
  -> 持久化任务状态
  -> 并发 Claim
  -> Lease + Heartbeat
  -> 崩溃恢复
  -> 外部副作用导致至少一次语义
  -> 业务侧幂等写回
  -> 指标和故障实验验证
```

## 不做什么

当前不做 DAG 编排、插件市场、多 Agent 协作、ZooKeeper、etcd、服务网格，也不为了简历堆 Kafka、Redis 或 RabbitMQ。每个技术选择都必须对应已测量的问题。

# MySQL 与 Redis 分发基线实验

## 实验目的

本实验不预设 Redis 一定有价值。

需要回答的问题是：当任务量和 Worker 数增加时，当前 MySQL 轮询方案是否出现了可观测的瓶颈；如果出现，瓶颈具体是什么，Redis 是否能够改善它。

## 真实负载假设

使用 MemoBridge 批量生成 SemanticProfile 作为代表性负载：

```text
用户一次导入大量资料
每个 SourceItem 生成一个独立任务
Worker 执行耗时不可控，并且可能受到 LLM 限流和超时影响
```

这只是代表性工作负载，不代表当前 MemoBridge 已经拥有几万条真实资料。

## 当前方案

```text
Worker 定时调用 TaskPulse Claim
TaskPulse 使用 MySQL 查询 queued 任务
MySQL 使用 FOR UPDATE SKIP LOCKED 完成并发领取
```

当前方案的理论轮询请求量为：

```text
轮询请求数/秒 = Worker 数 / 轮询间隔秒数
```

例如 50 个 Worker 每 200ms 轮询一次，理论上会产生约 250 次 Claim 请求/秒，即使队列为空也会产生查询。

这个公式只是待验证的假设，不是性能结论。

## 实验变量

任务数量：

```text
1,000
10,000
100,000
```

Worker 数量：

```text
2
8
32
64
```

轮询间隔：

```text
200ms
1s
2s
```

任务执行时间至少测试两种：

```text
100ms：强调分发吞吐
1s：模拟外部 LLM 或网络任务
```

## 必须记录的指标

每一轮压测前后运行 `scripts/capture-mysql-baseline.ps1`，把 MySQL 快照和
`dispatch-benchmark --output` 的 JSON 一起保存。否则只能看到 Worker 端结果，
无法判断瓶颈是否真在 MySQL。

TaskPulse 指标：

- 任务创建到首次 Claim 的延迟，至少记录 p50、p95、p99；
- Claim 请求数量；
- Claim 成功数量；
- 空 Claim 数量；
- 任务执行吞吐；
- 队列等待时间；
- 任务总完成时间。

MySQL 指标：

- CPU 使用率；
- 活跃连接数；
- 查询次数；
- `tasks` 表扫描行数；
- 行锁等待时间；
- 慢查询数量；
- 磁盘 I/O。
- `tasks` 表现有索引，以及 workflow Claim 和过期 Lease 恢复查询的 `EXPLAIN FORMAT=JSON`。

Redis 方案额外记录：

- Stream 发布数量；
- Stream 读取延迟；
- Pending Entries 数量；
- 重复投递数量；
- Redis 不可用期间的恢复时间。

## Redis 方案的准确边界

第一版 Redis 只作为任务通知和分发层：

```text
MySQL 保存任务事实
Redis Streams 保存待处理 task_id 通知
TaskPulse 仍然通过 MySQL 原子 Claim 建立 Lease
```

因此它理论上主要解决：

- 空队列时的无效轮询；
- 新任务发现延迟；
- Worker 唤醒和分发效率。

它不能直接解决：

- MySQL 每个任务一次 Claim 的查询成本；
- MySQL 事务写入吞吐；
- 任务结果存储压力；
- LLM Provider 的限流；
- Worker 执行本身的耗时。

如果实验显示瓶颈是 MySQL Claim 锁竞争，而不是空轮询，那么只增加 Redis 通知是不够的，还需要研究批量 Claim、分片或调整状态存储模型。

如果 `EXPLAIN` 显示 workflow 过滤后的 Claim 或过期 Lease 查询扫描了大量其他 workflow 的行，再将
`(workflow, status, available_at, created_at, id)` 或 `(workflow, status, lease_expires_at, created_at, id)`
作为候选索引写入 ADR；不能只根据 SQL 形状直接增加索引。

## 判定标准

只有满足以下至少一项，才实施 Redis：

1. 在目标 Worker 数下，MySQL 轮询产生明显的空 Claim 压力；
2. MySQL CPU、连接数或锁等待达到预先设定的上限；
3. 任务发现 p95 延迟不能满足目标；
4. Redis 方案在相同任务量和 Worker 数下，能够显著降低上述指标；
5. Redis 故障时可以通过 Outbox 和 MySQL 扫描恢复，且不丢任务。

如果 MySQL 在目标规模内表现足够好，就不引入 Redis，并在项目文档中记录：当前规模下 MySQL 方案更简单且足够。

## 实验结论模板

```text
在任务量为 ___、Worker 数为 ___、轮询间隔为 ___ 的条件下：

MySQL 轮询方案：
- throughput = ___
- claim p95 = ___
- empty claim ratio = ___
- MySQL CPU = ___
- lock wait = ___

Redis Streams 方案：
- throughput = ___
- claim p95 = ___
- empty claim ratio = ___
- MySQL CPU = ___
- lock wait = ___

因此 Redis 解决了 ___，没有解决 ___，最终决定 ___。
```

## 当前结论状态

截至本文件更新时，Redis Streams 未实施，不能填写 Redis 方案的性能数字。此前观察到 2 个 Worker 增加到 4 个 Worker 时，总吞吐接近翻倍，这只是“任务执行可并行”的初步信号；它不能证明 MySQL 没有瓶颈，也不能证明 Redis 有必要。最终结论必须由本实验的 JSON 报告和 MySQL 快照给出。

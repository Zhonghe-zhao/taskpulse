# ADR 0010：在具备 Dispatcher 前不写入 Task Outbox

## 状态

已接受。

## 背景

TaskPulse 预留了 `task_outbox` 表，计划在 MySQL 轮询被实测证明为任务发现瓶颈后，使用
Outbox + Redis Streams 发送 `task_id` 通知。

审查发现当前代码只在创建任务时插入 `task_outbox(status=pending)`，但不存在 Publisher、
Consumer 或清理器。这会让每个新任务留下永久 pending 记录，并不能提供任何调度收益。

## 决策

当前 MySQL 轮询版本不写入 Outbox。`task_outbox` 表保留为未来演进的 schema 预留，但不参与
运行时路径。

只有下列能力作为完整集合实现时，才恢复 Outbox 写入：

```text
Task + task_created Event + Outbox record 原子提交
-> Publisher 使用 SKIP LOCKED 扫描 pending Outbox
-> Redis Streams XADD
-> 标记 published 或以退避策略重试
-> Publisher 崩溃后重复投递安全
-> Worker 仍必须通过 MySQL Claim 建立唯一有效 Lease
```

## 后果

- 当前系统没有无消费者数据积压；
- 不能宣称已经实现 Redis Streams 或可靠 Outbox 发布；
- Redis 是否值得实现仍取决于 MySQL 压测中的 Claim 延迟、空轮询比例、连接和锁等待数据；
- 引入 Redis 时必须同时实现 Publisher、恢复扫描、指标和故障实验，而不是单独打开表写入。

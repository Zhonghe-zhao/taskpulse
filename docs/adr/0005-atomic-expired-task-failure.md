# ADR-0005：原子提交过期任务失败与失败事件

- 状态：已采纳
- 日期：2026-07-28

## 背景

租约过期且重试额度耗尽后，Reaper 原先先调用 `TaskStore.FailNextExpired`
将任务改为 `failed`，再单独写入 `task_failed` 事件。进程崩溃或事件写入失败时，
会出现“任务已经失败，但没有失败事件”的不一致状态。

## 决策

通过 `TaskTransitionStore.FailNextExpiredWithEvent` 在同一原子边界中完成：

```text
BEGIN
→ 锁定一条租约过期且重试额度耗尽的 running 任务
→ 更新为 failed，清理租约并增加版本号
→ 写入 task_failed 事件
→ COMMIT
```

MySQL 实现复用事务内部的过期任务清理函数。内存实现同时持有 Task Store 与
Event Store 的写锁，并保留任务变更前的快照。

事件 ID 由 Reaper 生成，失败消息和
`{"reason":"retry_budget_exhausted"}` payload 由 Domain 统一构造。

## 原因

- 失败状态与失败事件描述的是同一个生命周期事实。
- Reaper 不应负责协调两个无法保证原子的存储操作。
- 领域层统一失败原因，避免内存实现、MySQL 实现和 Reaper 出现不同语义。
- 事件冲突或数据库错误时，任务必须继续保持 `running`，等待下一次清理。

## 代价与边界

- `TaskTransitionStore` 承担了更多跨聚合持久化职责。
- 原子提交只保证数据一致性，不代表任务副作用恰好执行一次。
- 当前失败结果是终态；通用错误分类与重试语义已由 ADR-0006 实现。死信状态与人工重放仍未实现。

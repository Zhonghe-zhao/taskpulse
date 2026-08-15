# 17：MySQL 分发核心实验矩阵

- 文档状态：已记录初步结论
- 相关手册：[15-dispatch-benchmark-runbook.md](15-dispatch-benchmark-runbook.md)
- 产物目录：`artifacts/benchmarks/`、`artifacts/mysql/`

## 目的

用最少的几组对照，回答：

```text
1. 真实短任务负载下，系统吞吐和排队怎样？
2. 去掉 Fake LLM 睡眠后，调度链路本身有多快？
3. 增加 Worker 能否有效提高吞吐、降低排队？
```

本轮证据尚不足以单独证明「必须引入 Redis」。

## 关键参数含义

| 参数 / 指标 | 含义 |
|---|---|
| `TASKPULSE_LLM_FAKE_DELAY` | Fake LLM 执行睡眠。领到任务后才生效；`100ms` 模拟短推理，`0` 用于隔离调度开销。 |
| `TASKPULSE_EXTERNAL_POLL_INTERVAL` | 空闲轮询间隔。仅当 claim 返回无任务（204）时休眠；有积压时 complete 后会立刻再 claim。 |
| llm-worker 副本数 | 真正执行任务的 Worker 数量（Compose `--scale`）。 |
| `--create-workers` / `--status-workers` | `dispatch-benchmark` 客户端并发：创建任务 / 查询状态，不是 llm-worker 数。 |
| `queue_wait` | `started_at - created_at`，任务在队列里等到被 claim 的时间。 |
| `total_time` | `finished_at - created_at`，创建到完成的端到端时间。 |
| `throughput` | `完成任务数 / 墙钟秒数`。 |
| `claim_attempts` | Worker 调用 claim 的总次数（Prometheus）。 |
| `tasks_claimed` | claim 成功领到任务的次数。 |
| `claim_misses` | claim 时无可用任务（空查）的次数。 |
| `empty_claim_ratio` | `claim_misses / claim_attempts`，空转比例。 |

Worker 主循环（便于理解参数分工）：

```text
claim
  ├─ 有任务 → 执行（FAKE_DELAY）→ complete → 立刻再 claim
  └─ 无任务 → sleep(POLL_INTERVAL) → 再 claim
```

## 固定流程

```text
确认队列为空、Worker 参数正确
  → capture-mysql-baseline（before）
  → dispatch-benchmark（保存 JSON）
  → capture-mysql-baseline（after）
  → 记录终端输出与 claim 指标
```

命名示例：`before-w8-delay100-p1s` / `mysql-1000-w8-delay100-p1s.json` / `after-w8-delay100-p1s`。

## 实验 1：真实负载基线（已完成）

```text
8 Worker · FAKE_DELAY=100ms · POLL=1s · 1000 任务
标签：rerun-v2-w8-p1s
```

| 指标 | 结果 |
|---|---|
| 吞吐 | 29.9 task/s |
| queue_wait P50 / P95 | 16.1s / 30.9s |
| empty_claim_ratio | 10.87% |
| 完成 | 1000/1000 |

结论：系统可靠完成任务；持续负载下空轮询不高；不能据此证明 MySQL 是瓶颈，也不能据此引入 Redis。

## 实验 2：隔离基础设施开销（已完成）

```text
8 Worker · FAKE_DELAY=0 · POLL=1s · 1000 任务
标签：w8-delay0-p1s
```

| 指标 | 结果 |
|---|---|
| 吞吐 | 53.9 task/s（约实验 1 的 1.8×） |
| queue_wait P50 / P95 | 9.9s / 16.8s |
| empty_claim_ratio | 9.67% |
| 完成 | 1000/1000 |

结论：去掉 100ms 假执行后吞吐明显上升，说明上一轮慢有一部分来自任务执行本身（Redis 解决不了）；但仍远未「起飞」，说明 8 槽位与调度/HTTP/写库开销仍然存在。

## 实验 3：Worker 扩容是否近似线性（已完成）

固定：`FAKE_DELAY=100ms` · `POLL=1s` · `1000` 任务；只改 Worker 数。

| Worker | 吞吐 | vs 上一档 | queue_wait P50 | queue_wait P95 | empty_claim |
|---:|---:|---:|---:|---:|---:|
| 2 | 10.0/s | — | 51.3s | 95.5s | 3.4% |
| 4 | 15.8/s | 1.58×（理想 2×） | 26.4s | 58.3s | 8.6% |
| 8 | 29.9/s | 1.89×（理想 2×） | 16.1s | 30.9s | 10.9% |

对应产物：

- `artifacts/benchmarks/mysql-1000-w2-delay100-p1s.json`
- `artifacts/benchmarks/mysql-1000-w4-delay100-p1s.json`
- `artifacts/benchmarks/mysql-1000-rerun-v2-w8-p1s.json`

### 如何读这张表

- **吞吐上升、queue_wait 下降**：加 Worker 有效；同时能执行的坑位变多，任务更早被 claim。
- **未达到完美线性（2→8 约 3× 而非 4×）**：存在 HTTP、MySQL 事务、写事件等固定开销，属预期。
- **empty_claim 随 Worker 略升**：主要来自收尾阶段「人多粥少」和短暂抢空，不是有积压时故意空转更久；10% 左右仍不高。

结论：在本矩阵下应优先水平扩展 Worker；尚未看到「加到 8 就完全不动」或「MySQL 锁/慢查询打爆」的证据。

## 总收束

```text
可靠完成 1000 任务
→ 执行时间（FAKE_DELAY）影响吞吐
→ 增加 Worker 能提高吞吐、降低排队
→ 有负载时空 Claim 比例不高
→ 当前不支持“必须上 Redis”的结论
```

## 可选后续（实验 4）

仅在准备讨论 Redis 通知能力时做：队列保持空闲，固定较多 Worker（如 32），比较 poll `1s / 200ms / 50ms` 下的空 Claim 与 MySQL CPU/连接/锁。只有「空 Claim 很高且库压明显上升」时，Redis 才有明确引入理由。

## 边界

- 本矩阵 Fake LLM，不是真实 Provider 延迟与限流。
- MySQL 快照为辅助证据；行锁等待在已采样轮次中未显著恶化，但不代替更大规模（如 1 万、10 万任务）压测。
- `create-workers` / `status-workers` 只影响压测客户端灌载与观测，解释结果时不要与 llm-worker 副本数混淆。

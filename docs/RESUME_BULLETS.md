# TaskPulse 简历表述

## 推荐项目名

**TaskPulse：面向 AI 外部任务的可靠异步执行运行时**

## 项目描述

使用 Go 实现持久化任务、并发 Claim、Lease/Fencing、Heartbeat、Retry/Backoff、幂等完成、崩溃恢复、进度事件和 Prometheus 指标，并通过公开 Client/Worker SDK 接入 MemoBridge 的 SemanticProfile 生成任务。

## 简历 bullet 初稿

- 设计基于 MySQL 事务和 `FOR UPDATE SKIP LOCKED` 的并发任务领取模型，使用 Worker Lease、Heartbeat 和版本 fencing 防止过期 Worker 修改任务状态。
- 实现失败分类、指数退避、重试预算、任务事件和幂等 Complete/Fail，覆盖 Provider 暂时不可用、Worker 崩溃和重复请求等故障场景。
- 抽象公开 Go Client/Worker Runtime，支持 workflow 注册、外部 Worker 执行和上层业务解耦；MemoBridge 仅提交 `source_item_id`、`content_hash` 和 `prompt_version` 引用。
- 使用 Docker Compose、Kubernetes 和 Prometheus 完成运行、扩缩容、租约恢复和任务吞吐实验；初步 2/4 Worker 实验吞吐分别为 `0.06649 task/s` 和 `0.13289 task/s`。

## 使用前必须补齐的数字

- 完整 dispatch benchmark 的任务数、Worker 数和执行延迟；
- Claim p50/p95、空 Claim 比例和队列等待 p95；
- MySQL CPU、连接数和锁等待；
- Compose 真实 SemanticProfile 成功率和崩溃恢复耗时。

没有实测的数据不要写进简历；先用 `scripts/verify-taskpulse.ps1` 和 `cmd/dispatch-benchmark` 生成证据。

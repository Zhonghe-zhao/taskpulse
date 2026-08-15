# TaskPulse 演示与证据指南

目标是在五分钟内证明 TaskPulse 解决了什么问题，而不是只展示页面和技术名词。

## 推荐演示顺序

1. 打开 Dashboard，说明 TaskPulse 管理长耗时任务生命周期；
2. 从 MemoBridge 创建 `memobridge.semantic_profile` 或 `memobridge.embedding_index` 任务；
3. 展示 `queued -> running -> succeeded`、Worker、租约、心跳、进度和结果引用；
4. 展示重复请求返回同一个 Task ID；
5. 展示一次 Worker 崩溃后的 `task_recovered` 事件；
6. 用一句话说明 at-least-once 与业务幂等写回边界。

## 已验证场景

- 真实 SemanticProfile 任务完成；
- 真实 bge-m3 Embedding Index 任务完成并写回；
- 相同请求幂等返回原任务；
- Provider 暂时不可用时指数退避重试；
- Worker 优雅退出后主动 Release 并由其他 Worker 接管；
- Worker 崩溃后租约过期恢复；
- 无匹配 Workflow Worker 时任务持久排队，Worker 恢复后执行；
- 多 Worker 竞争下单一有效领取；
- Docker Compose 与 Docker Desktop Kubernetes 多副本运行；
- MySQL 调度吞吐和空 Claim 压力实验。

详细实验步骤和原始结论位于 [experiments/README.md](experiments/README.md)。

## 建议截图

截图不是运行证据的替代品，但有助于 README 和面试快速展示。建议提供以下四张，统一使用 16:9 或清晰的宽屏裁剪，并遮盖 Token、密码、数据库连接串和用户隐私数据。

| 文件名建议 | 内容 | 必须可见 |
|---|---|---|
| `dashboard-overview.png` | Dashboard 总览 | 状态统计、运行信号、任务列表 |
| `task-success-timeline.png` | 真实任务成功详情 | Workflow、Worker、进度、结果引用、事件链 |
| `task-recovery-timeline.png` | Worker 崩溃恢复 | `task_started`、`task_recovered`、最终成功 |
| `kubernetes-workloads.png` | Kubernetes 工作负载 | TaskPulse/Worker/MySQL Pod、READY、节点 |

建议放入：

```text
docs/images/
```

图片提供后，再将其中两张加入根 README；其余图片保留在实验文档中，避免 README 过长。

## 演示停止线

演示前只需确认：

```powershell
go test ./...
go vet ./...
docker compose ps
Invoke-WebRequest -UseBasicParsing http://127.0.0.1:8085/metrics
```

不要在演示当天升级依赖、引入 Redis/Kafka 或修改任务状态机。


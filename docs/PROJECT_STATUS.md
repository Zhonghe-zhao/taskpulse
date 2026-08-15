# TaskPulse 当前状态

- 状态日期：2026-08-16
- 当前阶段：功能闭环完成，进入发布、文档和面试收束

## 已实现能力

- MySQL 持久化 Task、TaskEvent 和任务生命周期状态；
- `workflow + idempotency_key` 幂等创建与冲突检测；
- `FOR UPDATE SKIP LOCKED` 并发 Claim；
- Lease、Heartbeat、Lease Token 与 Version Fencing；
- 通用错误分类、指数退避、重试预算和过期任务恢复；
- 进度、取消、主动 Release、执行超时和幂等 Complete/Fail；
- 公共 Go Client `pkg/taskpulse`；
- 公共 Worker Runtime `pkg/taskpulseworker`；
- Prometheus 指标、结构化日志和中英文 Dashboard；
- Docker Compose 与 Docker Desktop Kubernetes 清单；
- 基准工具、协议冒烟和故障实验脚本。

## 已完成真实验证

- `go test ./...` 与 `go vet ./...` 通过；
- MemoBridge SemanticProfile 任务完成真实 LLM 调用和幂等写回；
- MemoBridge Embedding Index 任务完成 bge-m3 向量生成和写回；
- 相同请求幂等返回原 Task ID，不同请求复用键返回冲突；
- 可重试 Provider 错误进入退避并在预算内重试；
- Worker 优雅退出产生 `task_released` 并由其他 Worker 接管；
- Worker 崩溃后租约过期，任务产生 `task_recovered` 并最终成功；
- 无匹配 Workflow Worker 时任务保持排队，Worker 恢复后继续执行；
- Kubernetes 中 TaskPulse 2 副本、Semantic Worker 2 副本、Embedding Worker 1 副本完成真实联调；
- 已记录 Worker 数量、吞吐、队列等待和空 Claim 压力基线。

## 当前边界

- 提供 at-least-once 执行尝试，不承诺外部副作用 exactly-once；
- 业务 Worker 必须对最终写回实施幂等；
- TaskPulse 不访问业务数据库，不理解 SourceItem 等领域概念；
- 控制面接口尚无用户鉴权，默认只能绑定回环地址；
- Kubernetes 仅为 Docker Desktop 本地多节点实验，不是生产部署方案；
- 未引入 Redis、Kafka、etcd、DAG 或 Agent 编排，且当前没有证据要求引入；
- Redis Outbox/Streams 仍是有条件的候选演进，不是当前能力。

## 发布前剩余工作

1. 建立可恢复的稳定 Git 提交；
2. 统一 GitHub 仓库名称与 Go Module 地址；
3. 从干净克隆执行测试、镜像构建和快速开始；
4. 发布 `v0.1.0`；
5. MemoBridge 移除 `replace => ../TaskPulse`，改用正式版本；
6. 补充经过脱敏的 Dashboard、恢复事件和 Kubernetes 截图；
7. 完成 README、简历表述和五分钟演示复核。

## 权威入口

- 使用：[GETTING_STARTED.md](GETTING_STARTED.md)
- 协议：[API_REFERENCE.md](API_REFERENCE.md)
- 部署：[DEPLOYMENT.md](DEPLOYMENT.md)
- 架构：[ARCHITECTURE.md](ARCHITECTURE.md)
- 演示证据：[DEMO_AND_EVIDENCE.md](DEMO_AND_EVIDENCE.md)
- 最终验收：[ACCEPTANCE_CHECKLIST.md](ACCEPTANCE_CHECKLIST.md)


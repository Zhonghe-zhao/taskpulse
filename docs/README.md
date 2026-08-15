# TaskPulse 文档索引

本文档目录按“目标、当前事实、决策、计划、证据”分工，避免把未来设想写成已经实现的能力。

## 文档职责

| 文档 | 回答的问题 | 更新时机 |
|---|---|---|
| [GETTING_STARTED.md](GETTING_STARTED.md) | 如何启动系统并完成第一个任务 | 端口、启动方式或示例任务变化时 |
| [API_REFERENCE.md](API_REFERENCE.md) | 控制面和 Worker 的真实 HTTP 契约是什么 | 路由或请求语义变化时 |
| [DEPLOYMENT.md](DEPLOYMENT.md) | Compose 与 Kubernetes 如何部署和排错 | 镜像、清单或环境变量变化时 |
| [DEMO_AND_EVIDENCE.md](DEMO_AND_EVIDENCE.md) | 如何展示项目并保存可靠性证据 | 新增已验证实验时 |
| [AUTUMN_RECRUITMENT_RUNBOOK.md](AUTUMN_RECRUITMENT_RUNBOOK.md) | 秋招前按什么顺序完成验证、证据、演示和收束 | 每完成一个收束阶段时 |
| [PROJECT_CHARTER.md](PROJECT_CHARTER.md) | 项目为什么存在，边界和成功标准是什么 | 项目定位或边界改变时 |
| [ARCHITECTURE.md](ARCHITECTURE.md) | 当前系统由什么组成，模块如何依赖 | 架构或运行链路改变时 |
| [PROJECT_BLUEPRINT.md](PROJECT_BLUEPRINT.md) | 最终交付蓝图、已完成能力和下一步收束顺序 | 里程碑推进时 |
| [PROJECT_STATUS.md](PROJECT_STATUS.md) | 当前代码事实与尚缺的运行时证据 | 每次验收后 |
| [ACCEPTANCE_CHECKLIST.md](ACCEPTANCE_CHECKLIST.md) | 最终验收项、命令和证据要求 | 运行时验证前后 |
| [MVP.md](MVP.md) | 当前阶段准备实现什么，完成标准是什么 | 里程碑推进时 |
| [STUDY_PATH.md](STUDY_PATH.md) | 开发过程中需要掌握哪些知识 | 学习重点改变时 |
| [adr/](adr/) | 为什么选择某个重要方案 | 作出或替换架构决策时 |
| [experiments/](experiments/) | 问题如何复现，方案如何验证 | 完成实验、压测或故障测试时 |

## ADR 索引

| 编号 | 决策 | 状态 |
|---|---|---|
| [ADR-0001](adr/0001-use-mysql-as-system-of-record.md) | 使用 MySQL 8 作为任务状态的持久化真相源和第一版持久化队列 | 已接受，已实现 |
| [ADR-0004](adr/0004-redis-streams-for-high-volume-dispatch.md) | Redis Streams 作为高规模任务发现层的候选演进 | 提议，未实施 |

## 维护规则

1. 当前代码没有实现的能力必须标记为“计划”或“目标”。
2. 重要技术选型先写 ADR，再进入实现。
3. ADR 被替换时不删除旧文件，而是标记为“已废弃”并链接新 ADR。
4. 每个工程问题按“现象、复现、决策、验证、边界”记录。
5. README 只做入口；详细设计只保留一个权威位置，避免重复维护。

## 当前文档状态

- 当前代码：MySQL 持久化、Task/TaskEvent、幂等创建、Claim/Lease/Heartbeat、重试与过期恢复、单次执行时限、主动 Lease Release、公共 Go Client/Worker Runtime、Prometheus 指标和 Dashboard 均已实现并有测试覆盖。
- 真实接入：MemoBridge 已按 SDK 接入 `memobridge.semantic_profile` 和 `memobridge.embedding_index`，完成真实 LLM 与 bge-m3 向量任务闭环。
- 运行证据：Compose 联调、Kubernetes 多副本、无 Worker 持久排队、Worker 优雅交接、Worker 崩溃恢复、幂等和失败重试均已完成；证据入口见 [DEMO_AND_EVIDENCE.md](DEMO_AND_EVIDENCE.md) 与 [experiments/README.md](experiments/README.md)。
- 部署边界：Kubernetes 是 Docker Desktop 本地集群实验，不宣称生产可用。
- Redis Streams 仅是备选方案；未获得 MySQL 基准证据前，不引入实现。

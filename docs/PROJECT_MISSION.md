# TaskPulse 项目立意

## 一句话定位

TaskPulse 是一个面向耗时、易失败外部任务的可靠异步执行运行时。

它让上层业务能够把不能在 HTTP 请求中同步完成的工作交给后台执行，并获得持久状态、租约、重试、进度、取消和崩溃恢复能力。

## 它为谁提供服务

TaskPulse 面向需要处理以下任务的业务系统：

- 调用大模型或其他外部 API；
- 批量处理资料、文件或图片；
- 执行耗时的导出、转换和索引任务；
- 发送可能失败的通知或 Webhook；
- 执行需要几十秒到数分钟的后台计算。

MemoBridge 的 SemanticProfile 批量分析是第一个真实接入方，但不是 TaskPulse 的业务定义。MemoBridge 拥有 SourceItem、Prompt、LLM 调用和 SemanticProfile 数据；TaskPulse 只拥有任务执行生命周期。

## TaskPulse 解决的核心问题

上层业务不应该把下面这些可靠性逻辑重复实现一遍：

```text
请求立即返回 202
任务持久化
Worker 并发领取
任务租约和心跳
执行超时
失败重试和退避
Worker 崩溃后恢复
任务取消
进度和事件记录
幂等创建和幂等完成
```

TaskPulse 的核心问题可以描述为：

> 当一个后台任务执行时间长、依赖外部服务、可能超时或遇到 Worker 崩溃时，如何保证任务不会因为一次进程故障而丢失，并且让业务方能够准确知道任务当前状态。

## 它不是什么

TaskPulse 不是：

- Kafka、RabbitMQ 或 Redis Streams 的替代品；
- LLM 或 Agent 推理引擎；
- 业务数据库；
- 自动理解所有 workflow 的通用执行器；
- 保证外部副作用绝对只执行一次的系统。

TaskPulse 可以使用消息队列作为分发层，但任务状态、租约和重试仍然属于 TaskPulse 的职责。

## TaskPulse 和 MQ 的区别

```text
MQ：可靠传输消息
TaskPulse：可靠管理任务执行生命周期
```

MQ 关注生产者、消费者、消息确认和吞吐；TaskPulse 关注任务的 queued、running、retrying、succeeded、failed、cancelled 状态，以及执行期间的租约、心跳、错误分类和恢复。

TaskPulse 内部确实包含一个持久化工作队列，但项目目标不是重新实现 Kafka，而是提供更高层的任务执行语义。

## 业务接入方式

上层业务只提交不透明的任务引用：

```json
{
  "workflow": "memobridge.semantic_profile",
  "input": {
    "source_item_id": 11778,
    "content_hash": "sha256:...",
    "prompt_version": "source_semantic_profile:v1"
  }
}
```

TaskPulse 不理解这些字段，只负责可靠调度。

MemoBridge Worker 负责：

```text
读取 SourceItem
校验 content_hash
调用 LLM
校验模型输出
幂等写回 SemanticProfile
向 TaskPulse 汇报成功或失败
```

因此通用性体现在任务生命周期，而不是业务执行逻辑。

## 当前项目的核心范围

当前阶段只聚焦以下能力：

1. 持久化任务和任务事件；
2. workflow 过滤的并发 Claim；
3. Lease、Heartbeat 和 Fencing；
4. 超时、重试和退避；
5. Worker 崩溃后的租约恢复；
6. 幂等创建和幂等完成；
7. 进度、指标和故障实验；
8. MemoBridge 真实任务联调。

## 技术演进原则

所有新技术必须由可观测问题驱动：

```text
真实负载
→ 测量当前方案
→ 定位瓶颈
→ 提出候选方案
→ 实施最小改动
→ 对照压测
→ 记录收益和边界
```

因此 Redis 只有在 MySQL 轮询的空 Claim、任务发现延迟或数据库压力被实验验证后才引入。DAG、服务网格、ZooKeeper 和 etcd 不属于当前核心范围。

## 一致性边界

TaskPulse 提供的是“至少一次执行尝试”和“单个有效 Lease 的并发控制”，不是外部副作用的绝对 Exactly-Once。

Worker 可能在业务写回成功后、TaskPulse 收到 Complete 前崩溃，因此业务 Worker 必须使用幂等写回。MemoBridge 使用 `source_item_id + content_hash + prompt_version` 保证旧结果不会覆盖新版本。

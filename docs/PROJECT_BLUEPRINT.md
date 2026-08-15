# TaskPulse 项目最终蓝图

## 1. 最终产品

TaskPulse 是一个面向长耗时、易失败外部任务的可靠异步执行运行时。

它为上层业务提供统一能力：

```text
创建任务
持久化任务
并发领取
租约和心跳
超时和重试
崩溃恢复
取消和进度
幂等完成
事件和指标
```

TaskPulse 不负责理解具体业务，也不直接调用 LLM。

## 2. 真实使用者

第一个真实使用者是 MemoBridge。

MemoBridge 的实际问题是：

```text
用户选择一份资料进行 SemanticProfile 分析
LLM 调用耗时不可控
外部模型可能超时、限流或暂时不可用
Worker 可能在执行过程中崩溃
用户需要知道任务状态
成功写回的资料不能因为重试而重复覆盖
```

TaskPulse 解决可靠执行问题，MemoBridge 保留业务逻辑和数据所有权。

## 3. 核心边界

### TaskPulse 负责

```text
Task / TaskEvent
状态机
MySQL 持久化
Claim
Lease / Fencing
Heartbeat
Retry / Backoff
Worker 崩溃恢复
Cancel
Progress
Idempotency
Prometheus 指标
```

### MemoBridge 负责

```text
SourceItem
content_hash
Prompt
LLM 调用
模型输出校验
SemanticProfile Upsert
业务幂等
```

## 4. 最终架构

```text
MemoBridge API
    │ 创建任务，只提交业务引用
    ▼
TaskPulse HTTP API
    │ MySQL 持久化任务和事件
    ▼
TaskPulse Scheduler / Claim
    │ Lease + Heartbeat
    ▼
MemoBridge Worker
    │ 读取业务数据并调用 LLM
    ▼
MemoBridge PostgreSQL
```

当实验确认 MySQL 轮询成为瓶颈后，演进为：

```text
MySQL Task + Outbox
    ▼
Redis Streams 分发 task_id
    ▼
TaskPulse MySQL 原子 Claim
    ▼
MemoBridge Worker
```

Redis 不是任务最终状态存储，也不替代 Lease 和 MySQL Claim。

## 5. 当前代码已完成

```text
任务状态机
MySQL 持久化
内存存储
workflow 过滤
幂等创建
Claim
Lease Token
Heartbeat
Complete / Fail 幂等
Retry / Backoff
过期任务恢复
外部 Worker 协议
Prometheus 指标
Docker Compose
Kubernetes 部署
MemoBridge SDK 接入代码
```

上述内容中，Docker Compose 与 Kubernetes 清单已经静态校验；跨项目容器运行、MemoBridge 真实任务闭环和容器崩溃恢复仍需保留运行日志与事件作为最终证据。具体以 [ACCEPTANCE_CHECKLIST.md](ACCEPTANCE_CHECKLIST.md) 为准。

## 6. 当前还缺什么

### 必须完成

```text
1. 在干净的 Compose 环境完成 TaskPulse 与 MemoBridge 一键联调，并保存脚本输出；
2. 完成容器 Worker 崩溃恢复演示，并保存 TaskEvent 与 Worker 日志；
3. 完成 MySQL 轮询基线压测并保存原始 JSON 和 MySQL 快照；
4. 根据数据决定是否保持 MySQL 方案，或实施 Redis Streams；
5. 整理故障实验和性能报告。
```

### 有时间再做

```text
优先级调度
按 workflow 限流
死信任务管理
任务管理页面
```

### 当前不做

```text
DAG 工作流
多 Agent 编排
ZooKeeper
etcd 集群协议
服务网格
Kafka 替代一切
动态脚本执行
插件市场
```

## 7. Redis 的决策条件

先测量当前 MySQL 方案：

```text
任务数量：1,000 / 10,000 / 100,000
Worker：2 / 4 / 8 / 32
轮询间隔：200ms / 1s / 2s
```

记录：

```text
任务发现延迟 p50/p95/p99
空 Claim 比例
吞吐
MySQL CPU
活跃连接数
行锁等待
```

只有当 MySQL 轮询确实成为瓶颈时，才实施：

```text
Outbox Publisher
Redis Streams
MySQL 降级扫描
Redis 故障恢复
```

如果 MySQL 足够好，就保留 MySQL，并在报告中说明没有引入 Redis 的原因。

## 8. 最终验收场景

### 场景一：正常任务

```text
MemoBridge 创建任务
Worker Claim
读取 SourceItem
调用 LLM
SemanticProfile Upsert
TaskPulse Complete
```

### 场景二：重复创建

```text
相同 workflow + 幂等键重复提交
返回原任务
不重复执行
```

### 场景三：Worker 崩溃

```text
Worker Claim
Worker 进程退出
Lease 过期
任务恢复
新 Worker 重新 Claim
任务最终完成
```

### 场景四：外部 LLM 暂时失败

```text
provider_timeout
→ retrying
→ 指数退避
→ 恢复后重新执行
```

### 场景五：资料发生变化

```text
执行前后 content_hash 不一致
→ 任务 source_changed
→ 旧结果不覆盖新资料
```

## 9. 接下来按这个顺序做

```text
第 1 步：运行当前完整测试并提交已有修改
第 2 步：执行 Compose 真实联调脚本并保存输出
第 3 步：执行容器 Worker 崩溃恢复并保存事件序列
第 4 步：运行 MySQL 轮询基线压测
第 5 步：根据数据决定 Redis
第 6 步：整理项目文档、故障报告和面试材料
```

## 10. 面试中的核心表达

```text
我没有把 TaskPulse 设计成 Kafka 或 Redis 的替代品。
它解决的是长耗时、易失败外部任务的执行生命周期问题。
上层业务只负责业务逻辑，TaskPulse 负责任务持久化、租约、重试、崩溃恢复和幂等协议。
Redis 是否引入不是预设结论，而是通过 MySQL 轮询基线和高并发实验决定。
```

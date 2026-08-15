# Worker 租约设计

## 要解决的问题

任务被 Worker 领取并变成 `running` 后，Worker 可能在写回结果前退出。如果系统只记录任务状态，就无法判断这个任务仍在执行，还是已经失去执行者。

租约为一次领取增加两个事实：

- `lease_owner`：当前领取任务的 Worker；
- `lease_expires_at`：本次执行权的失效时间。

租约表示的是一段有限时间内的执行权，不表示任务一定执行成功。

## 当前实现

Worker 调用：

```text
ClaimNext(worker_id, now, lease_duration)
```

MySQL 在同一事务中完成：

```text
锁定最早的 queued 任务
→ 跳过其他事务已锁定的任务
→ 修改为 running
→ 写入租约所有者和过期时间
→ version + 1
→ 提交事务
```

当前保证：

- 多个 Worker 不会同时成功领取同一条 `queued` 任务；
- 领取结果可以追溯到具体 Worker；
- 任务进入终态时清除租约；
- 旧版本 Worker 写回结果时会受到乐观锁保护。
- Worker 执行任务期间按租约时长的三分之一周期续租；
- 只有租约未过期且 `lease_owner` 匹配的 Worker 可以续租；
- 外部 Worker 的 Heartbeat、Progress、Complete 和 Fail 必须携带 LeaseToken；空 token、旧 token 或错误 token 会被拒绝；
- 续租不修改任务业务版本，避免心跳与完成写回发生版本冲突。
- 过期的 `running` 任务可以被新 Worker 原子接管；
- 接管会增加 `retry_count` 和 `version`，隔离旧 Worker 的写回；
- 接管产生 `task_recovered` 事件。
- Reaper 将重试额度耗尽的过期任务转换为 `failed`；
- 清理失败任务时会清除租约、记录结束时间、增加版本并写入失败事件。

## 当前边界与待验证项

- Worker 崩溃恢复的代码路径、Kubernetes/Compose 清单与实验步骤已完成；最终交付仍需要保留一次运行时 TaskEvent 证据。
- TaskPulse 提供至少一次执行尝试；MemoBridge 已使用 `source_item_id + content_hash + prompt_version` 做业务写回幂等。其他接入方也必须自行保证外部副作用幂等。
- 可查询和人工重放的死信任务尚未实现；重试预算耗尽后的任务当前进入 `failed`。

只有 `retry_count < max_retries` 的过期任务可以被接管。达到重试上限后，Reaper 会将任务收敛为 `failed`，避免任务永久停留在 `running`。

## 交付语义

未来加入过期恢复后，TaskPulse 提供的是 `at-least-once`（至少一次）执行，而不是 `exactly-once`（恰好一次）执行。

原因是 Worker 可能已经完成外部副作用，但在写回任务结果前崩溃。系统无法仅通过任务表判断副作用是否发生，因此恢复后可能再次执行。最终需要执行器使用幂等键、唯一约束或结果去重来处理重复执行。

## 后续演进

1. 保存 Compose/Kubernetes 崩溃恢复实验的原始事件与指标。
2. 依据 MySQL 调度基线决定是否需要 Redis 通知层。
3. 只有出现真实运维需求后再设计死信查询与人工重放接口。


---

# 历史故障记录：Heartbeat `context canceled` 误判（已修复）

> 以下是 2026-08-06 的旧版本故障复盘，用于解释为什么需要区分“正常执行结束后的 Context 取消”和“真正的 Heartbeat 失败”。当前 Worker Runtime 已在取消后忽略该正常取消路径，且相关回归测试已通过。不要把下方的 `external_worker_error: context canceled` 结论描述为当前版本仍存在的问题。

## RetryCount

从时间线看，答案可以非常明确：

> **最终把任务推入 `failed` 的直接原因，是最后一次执行再次发生 `external_worker_error: context canceled`，但此时 `retry_count=3` 已达到 `max_retries=3`，没有剩余重试额度。**
>
> **删除 Pod 没有直接把任务标记为失败，但它触发了一次 Lease Recovery，并消耗了一次重试额度，因此间接加速了预算耗尽。**

也就是说：

```text
直接原因：
最后一次错误发生时，重试次数已经耗尽

间接因素：
删除 Pod 触发恢复执行，消耗了 1 次 RetryCount

更根本的问题：
外部 Worker 将 heartbeat 的 context canceled 错误地视为执行失败
```

# 一、从创建到最终失败，一共用了多久

任务创建：

```text
12:27:09.701647
```

最终失败：

```text
12:29:12.931320
```

总耗时：

```text
2 分 3.230 秒
```

完整时间轴：

```text
12:27:09.701  创建任务
12:27:09.805  首次领取

12:27:39.822  第一次执行报错，安排重试
12:27:40.443  第一次重试开始

12:28:10.448  原 Worker 的 Lease 过期，新 Worker 恢复任务

12:28:40.461  恢复执行再次报错，安排重试
12:28:42.912  第二次普通重试开始

12:29:12.931  再次报错，重试预算耗尽，最终失败
```

# 二、逐段分析每一段时间

## 1. 创建任务到首次领取

```text
task_created
12:27:09.701647

task_started
12:27:09.805463
```

间隔：

```text
0.104 秒
```

表示任务创建后，外部 Worker 很快轮询到它。

状态：

```text
queued → running
```

此时：

```text
retry_count = 0
```

这属于初次执行，不算重试。

---

## 2. 第一次执行持续约 30 秒

```text
task_started
12:27:09.805463

task_retrying
12:27:39.822781
```

间隔：

```text
30.017 秒
```

然后出现：

```text
error_code = external_worker_error
error = heartbeat context canceled
```

系统将它判断为可重试，于是：

```text
running → retrying
retry_count: 0 → 1
```

这里非常关键：

> **第一次失败发生在 Pod 恢复事件之前。**

因此，第一次 `task_retrying` 不是由你删除 Pod 导致的。

它已经暴露了外部 Worker 的 Heartbeat Context 处理问题。

---

## 3. 第一次退避等待约 0.538 秒

事件中记录：

```text
delay_ms = 538
available_at = 12:27:40.361108
```

任务实际重新领取：

```text
12:27:40.443520
```

从安排重试到重新领取，一共：

```text
0.621 秒
```

其中：

```text
0.538 秒
是计划的退避时间

约 0.082 秒
是到期以后等待 Worker 下一次轮询和数据库处理的时间
```

状态：

```text
retrying → running
```

事件：

```text
task_retry_started
```

此时：

```text
retry_count = 1
```

---

# 三、Pod 删除发生在什么位置

你删除的应该是这一轮正在执行任务的 Pod：

```text
task_retry_started
12:27:40.443520
```

此后没有立刻出现：

```text
task_failed
```

也没有立刻出现：

```text
task_retrying
```

而是在约 30 秒后出现：

```text
task_recovered
12:28:10.448243
```

时间间隔：

```text
30.005 秒
```

这几乎精确对应你的配置：

```text
TASKPULSE_EXTERNAL_LEASE = 30s
```

所以这段过程非常清晰：

```text
12:27:40.443
Worker A 领取并开始执行

你删除 Worker A 所在 Pod
→ 进程消失
→ Heartbeat 停止
→ Task 在数据库里仍然是 running

等待 Lease 到期约 30 秒

12:28:10.448
Worker B 发现 Lease 已过期
→ 接管任务
→ 产生 task_recovered
```

这证明 Lease Recovery 正常工作了。

# 四、Pod 删除有没有导致任务失败

没有直接导致。

如果删除 Pod 会直接导致失败，那么事件应该类似：

```text
task_retry_started
→ task_failed
```

但实际是：

```text
task_retry_started
→ 等待 30 秒
→ task_recovered
```

说明 TaskPulse 没有把 Worker 消失立即判成任务失败，而是允许其他 Worker 恢复执行。

这正是系统希望实现的行为：

```text
Worker 崩溃
≠ Task 立即失败

Worker 崩溃
→ Lease 过期
→ 其他 Worker 接管
```

但是，恢复领取会消耗一次 RetryCount。

在这次恢复中：

```text
retry_count: 1 → 2
```

所以删除 Pod 的影响是：

```text
消耗了一次重试预算
```

而不是：

```text
直接将任务变成 failed
```

# 五、恢复执行后又持续约 30 秒

```text
task_recovered
12:28:10.448243

task_retrying
12:28:40.461734
```

间隔：

```text
30.013 秒
```

恢复 Worker 执行了约 30 秒，又出现：

```text
external_worker_error
heartbeat context canceled
```

于是系统安排下一次重试：

```text
running → retrying
```

此时 payload 中：

```text
retry_count = 3
```

为什么从之前的 1 跳到了 3？

因为中间的恢复消耗了一次：

```text
第一次普通重试：
0 → 1

Pod 删除后的恢复：
1 → 2

恢复执行后再次安排普通重试：
2 → 3
```

因此：

```text
retry_count = 3
max_retries = 3
```

到这里，所有重试额度已经被占满。

# 六、第二次退避等待约 2.419 秒

第二次 `task_retrying`：

```text
12:28:40.461734
```

事件中记录：

```text
delay_ms = 2419
available_at = 12:28:42.881490
```

实际领取：

```text
12:28:42.912284
```

时间拆分：

```text
计划退避：
约 2.420 秒

到期后到实际领取：
约 0.031 秒
```

状态：

```text
retrying → running
```

事件：

```text
task_retry_started
```

此时仍然是：

```text
retry_count = 3
max_retries = 3
```

这表示任务获得了最后一次执行机会。

# 七、最后一次执行再次持续约 30 秒

```text
task_retry_started
12:28:42.912284

task_failed
12:29:12.931320
```

间隔：

```text
30.019 秒
```

再次出现相同错误：

```text
external_worker_error:
Post ".../heartbeat": context canceled
```

事件 payload：

```text
retryable = True
error_code = external_worker_error
```

这里容易误解：

```text
retryable=True
```

只表示：

> 这种错误类型具有重试资格。

但此时：

```text
retry_count = 3
max_retries = 3
```

系统想再安排一次重试，就必须变成：

```text
next_retry_count = 4
```

但是：

```text
4 > 3
```

所以无法继续重试，最终执行：

```text
running → failed
```

# 八、整个 RetryCount 是怎么消耗的

这次共有四次执行尝试：

| 执行尝试  | 如何获得执行机会       | RetryCount | 结果                                |
| ----- | -------------- | ---------: | --------------------------------- |
| 第 1 次 | 初次领取           |          0 | Heartbeat `context canceled`      |
| 第 2 次 | 普通重试           |          1 | Pod 被删除                           |
| 第 3 次 | Lease Recovery |          2 | Heartbeat `context canceled`      |
| 第 4 次 | 普通重试           |          3 | Heartbeat `context canceled`，预算耗尽 |

所以：

```text
MaxRetries = 3
```

不是“总共最多执行三次”，而是：

```text
初次执行 1 次
+
额外执行机会最多 3 次
=
总共最多执行 4 次
```

本次正好进行了四次执行尝试。

# 九、用因果链回答你的问题

## Pod 删除造成了什么

```text
第二次执行中的 Worker 消失
→ Heartbeat 停止
→ 等待 30 秒 Lease 到期
→ 另一个 Worker 恢复执行
→ RetryCount 从 1 增加到 2
```

Pod 删除没有直接产生：

```text
task_failed
```

它产生的是：

```text
task_recovered
```

---

## 重试次数耗尽造成了什么

最后一次发生错误时：

```text
retry_count = 3
max_retries = 3
```

错误虽然标记为：

```text
retryable=True
```

但已经没有额度。

因此产生：

```text
task_failed
```

这才是最终状态转换的直接原因。

---

## 真正让预算反复消耗的是什么

除了一次人为删除 Pod，其余失败都来自：

```text
heartbeat context canceled
```

具体出现了三次：

```text
第一次执行结束
→ context canceled
→ RetryCount 0 → 1

恢复执行结束
→ context canceled
→ RetryCount 2 → 3

最后执行结束
→ context canceled
→ 已无预算
→ failed
```

因此更准确的因果关系是：

```text
Heartbeat 竞态错误
消耗第 1 次额度

删除 Pod
消耗第 2 次额度

Heartbeat 竞态错误
消耗第 3 次额度

Heartbeat 竞态错误再次发生
但已无额度
→ 最终 failed
```

# 十、反事实分析

## 假如你没有删除 Pod

按照当前错误模式，很可能是：

```text
初次执行报 context canceled
→ retry_count=1

第一次重试再次报 context canceled
→ retry_count=2

第二次重试再次报 context canceled
→ retry_count=3

第三次重试再次报 context canceled
→ failed
```

也就是说：

> 即使不删除 Pod，只要 Heartbeat Context Bug 每次都出现，任务最终仍然可能失败，只是事件中不会出现 `task_recovered`。

---

## 假如 Heartbeat Bug 不存在，但你删除一次 Pod

理想事件序列应该是：

```text
task_created
→ task_started
→ Pod 被删除
→ Lease 到期
→ task_recovered
→ task_succeeded
```

最终：

```text
status = succeeded
retry_count = 1
```

或者根据删除发生时此前是否已有一次重试，可能是相应的计数。

所以：

> **仅删除一个 Pod，并且仍有恢复预算，正常情况下不应该导致最终失败。**

# 十一、最后给出精确结论

```text
任务总耗时：
2 分 3.230 秒

首次领取延迟：
0.104 秒

第一次执行：
30.017 秒

第一次重试等待：
0.621 秒

Pod 删除后的 Lease 恢复等待：
30.005 秒

恢复后的执行：
30.013 秒

第二次重试等待：
2.451 秒

最后一次执行：
30.019 秒
```

最终判断：

```text
Pod 删除是一次中间故障
→ 被 Lease Recovery 成功处理
→ 消耗一次重试额度

最终 failed 的直接原因
→ 最后一次 heartbeat context canceled
→ 此时 RetryCount 已达到 MaxRetries
→ 系统拒绝继续重试

根本代码问题
→ 正常 Context 取消被外部 Worker误判为 Heartbeat 故障
```

因此不能简单说：

```text
“删除 Pod 导致任务失败”
```

更准确的说法是：

> **删除 Pod 消耗了一个恢复机会，但系统成功恢复了任务；真正让任务最终失败的是外部 Worker 的 Heartbeat Context 错误反复发生，并最终耗尽了重试预算。**

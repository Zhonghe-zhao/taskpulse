# TaskPulse 秋招收束执行手册

## 1. 目标与完成标准

本手册用于把 TaskPulse 收束为可以写进简历、现场演示并经得起追问的项目，不再无边界增加功能。

最终必须能用证据说明：

1. TaskPulse 能可靠执行长耗时任务，而不只是传递消息；
2. 多个 Worker 不会同时持有同一任务的有效租约；
3. 可重试失败、Worker 优雅退出和进程崩溃都有确定行为；
4. TaskPulse 重启后任务事实仍保存在 MySQL；
5. MemoBridge SemanticProfile 是已经跑通的真实业务负载；
6. 性能结论来自实验，而不是为了简历强行引入 Redis；
7. 能准确说明 at-least-once、业务幂等和系统边界。

达到上述标准后停止增加功能，进入简历和面试准备。

## 2. 当前基线

已经完成，不需要重复开发：

- MySQL 持久化任务、状态机和 TaskEvent；
- workflow 过滤与并发安全 Claim；
- Lease Token、Heartbeat 和版本 fencing；
- 指数退避重试、最终失败和租约过期恢复；
- 幂等创建、幂等 Complete/Fail；
- 排队/运行任务取消、Worker 主动 Release、执行超时和优雅退出；
- Go Client 与 Worker Runtime SDK；
- Prometheus 指标、结构化日志和 Dashboard；
- Docker Compose 与 Kubernetes 清单；
- MemoBridge 真实 DeepSeek 成功、幂等、重试、优雅交接和崩溃恢复实验。

已完成的真实 LLM 证据见 `docs/experiments/23-memobridge-real-llm-reliability.md`。

## 3. 执行顺序

```text
A. 静态质量门禁
-> B. TaskPulse 协议冒烟
-> C. Compose 一键闭环
-> D. Kubernetes 可靠性验收
-> E. 性能证据归档
-> F. 文档、演示和简历收束
```

上一阶段未通过时，不进入下一阶段。

## 4. A：静态质量门禁

### 环境

- PowerShell，当前目录 `E:\CS\TaskPulse`；
- Go、Docker Desktop、kubectl 可用；
- MemoBridge 位于 `E:\CS\memobridge`。

### 执行

```powershell
cd E:\CS\TaskPulse
.\scripts\verify-taskpulse.ps1
go test -race ./...
go vet ./...
git status --short
```

Docker Desktop 可用时再执行镜像构建：

```powershell
.\scripts\verify-taskpulse.ps1 -BuildIntegration
```

### 通过条件

- TaskPulse 和 MemoBridge 测试通过；
- Compose 配置可解析，Kubernetes 清单可渲染；
- race detector 和 vet 无错误；
- 明确知道 `git status` 中每项修改的来源。

失败时不要跳过。记录错误、修复和复测结果。

## 5. B：TaskPulse 协议冒烟

### 启动

```powershell
cd E:\CS\TaskPulse
docker compose up -d --build mysql taskpulse
docker compose ps
```

默认地址：TaskPulse `http://127.0.0.1:8085`，MySQL `127.0.0.1:3306`。

### 测试

```powershell
$env:TASKPULSE_WORKER_AUTH_TOKEN = "taskpulse_dev_worker_token"
.\scripts\smoke-taskpulse-protocol.ps1
```

### 通过条件

```text
duplicate_task_id == task_id
lease_token_present == true
completed_status == succeeded
replayed_status == succeeded
```

这组实验独立验证创建幂等、Claim、Lease、Progress、Complete 和 Complete 重放。

## 6. C：Compose 真实业务闭环

真实 DeepSeek 五组实验已经完成。本轮使用 fake provider 做可重复的一键验收，不消耗模型额度。

### 启动完整环境

```powershell
cd E:\CS\TaskPulse
docker compose -f compose.integration.yaml up -d --build --scale memobridge-worker=2
docker compose -f compose.integration.yaml ps
```

默认地址：TaskPulse `http://127.0.0.1:8085`，MemoBridge API `http://127.0.0.1:8081`。

### 执行和保存证据

```powershell
.\scripts\smoke-memobridge-integration.ps1

.\scripts\capture-task-evidence.ps1 `
  -TaskID <脚本输出的-task_id> `
  -Label memobridge-compose-success
```

证据写入 `artifacts/evidence`。

### 通过条件

- 相同请求返回相同 Task ID；
- 任务最终 `succeeded`，事件包含创建、领取、进度和成功；
- `result_ref` 只有业务引用，没有正文、Prompt 或完整模型输出；
- MemoBridge 能读取写回的 SemanticProfile；
- Dashboard 不展示 `lease_token`。

## 7. D：Kubernetes 可靠性验收

Kubernetes 的价值是验证副本调度、自愈和 TaskPulse 租约恢复如何配合。

### D0：构建与部署

Docker Desktop Kubernetes 使用当前 Docker image store 时执行：

```powershell
cd E:\CS\TaskPulse
docker build --build-arg APP=taskpulse -t taskpulse:dev .
docker build --build-arg APP=llm-worker -t taskpulse-llm-worker:dev .

kubectl apply -k .\deploy\k8s
kubectl rollout status deployment/taskpulse -n taskpulse
kubectl rollout status deployment/llm-worker -n taskpulse
kubectl get pods -n taskpulse -o wide
```

预期副本：TaskPulse Server 2 个、LLM Worker 2 个、MySQL 1 个。

单独打开 PowerShell 窗口并保持端口转发：

```powershell
kubectl port-forward -n taskpulse service/taskpulse 18080:8080
```

访问 `http://127.0.0.1:18080`。

### D1：多 Worker 单一有效领取

创建一批执行时间足够长的 `llm_analysis` 任务，观察两个 Worker 日志和任务事件。

通过条件：每个任务同一时刻只有一个有效 Lease；错误 Token 或过期 Worker 不能提交结果。

```powershell
.\scripts\capture-kubernetes-task-evidence.ps1 `
  -TaskID <task_id> `
  -TaskPulseURL http://127.0.0.1:18080 `
  -Label k8s-multi-worker-claim
```

### D2：Worker Pod 强制崩溃

```powershell
kubectl set env deployment/llm-worker -n taskpulse TASKPULSE_LLM_FAKE_DELAY=45s
kubectl rollout status deployment/llm-worker -n taskpulse
```

创建任务，确认进入 `running` 后找到并删除正在执行的 Worker：

```powershell
kubectl get pods -n taskpulse -l app=llm-worker
kubectl delete pod -n taskpulse <worker-pod-name>
```

预期事件：

```text
task_created -> task_started -> task_recovered -> task_succeeded
```

```powershell
.\scripts\capture-kubernetes-task-evidence.ps1 `
  -TaskID <task_id> `
  -TaskPulseURL http://127.0.0.1:18080 `
  -Label k8s-worker-crash-recovery
```

### D3：TaskPulse Server 滚动重启

在任务排队或运行时执行：

```powershell
kubectl rollout restart deployment/taskpulse -n taskpulse
kubectl rollout status deployment/taskpulse -n taskpulse
```

通过条件：任务和事件不丢失；服务恢复后 Worker 继续工作；任务最终状态符合预期。

### D4：Worker 优雅交接

```powershell
kubectl rollout restart deployment/llm-worker -n taskpulse
```

通过条件：事件出现 `task_released`，替代 Worker 重新领取，`retry_count` 不因交接增加。它必须与等待 Lease 过期的崩溃恢复区分开。

### 恢复默认配置

```powershell
kubectl set env deployment/llm-worker -n taskpulse TASKPULSE_LLM_FAKE_DELAY-
kubectl rollout status deployment/llm-worker -n taskpulse
```

## 8. E：性能证据归档

### 已有结论

- 2/4/8 Worker 的吞吐与空 Claim 比例已有初步矩阵；
- 8 Worker 空闲轮询 1 秒约产生 8 次空 Claim/秒；
- 8 Worker 空闲轮询 200 毫秒约产生 39.5 次空 Claim/秒；
- 这证明轮询成本存在，但尚未证明 MySQL 已成为瓶颈。

### 最小补充基线

只补一组固定配置的大样本，不无限压测：

```powershell
$mysqlRootPassword = "<compose 当前 root 密码>"

.\scripts\capture-mysql-baseline.ps1 `
  -Password $mysqlRootPassword `
  -Label before-mysql-10000-w8-p200 `
  -Workflow llm_analysis

go run ./cmd/dispatch-benchmark `
  --tasks 10000 `
  --create-workers 32 `
  --status-workers 32 `
  --status-poll-interval 250ms `
  --output .\artifacts\benchmarks\mysql-10000-w8-p200.json `
  --timeout 30m

.\scripts\capture-mysql-baseline.ps1 `
  -Password $mysqlRootPassword `
  -Label after-mysql-10000-w8-p200 `
  -Workflow llm_analysis
```

实验前记录实际 Worker 数和关键参数：

```powershell
@(docker compose ps -q llm-worker).Count
$workers = @(docker compose ps -q llm-worker)
docker inspect $workers[0] --format '{{range .Config.Env}}{{println .}}{{end}}' |
  Select-String 'TASKPULSE_LLM_FAKE_DELAY|TASKPULSE_EXTERNAL_POLL_INTERVAL'
```

必须分析：吞吐、创建错误、未完成任务、queue wait/total time p50/p95/p99、Claim attempts/misses、空 Claim 比例、MySQL CPU/连接数/锁等待，以及 Worker 数、fake delay、poll interval。

### Redis 决策门槛

只有同时满足以下条件才做 Redis 通知层原型：

1. 任务发现延迟或吞吐不满足目标；
2. MySQL 快照显示 CPU、连接或锁等待压力；
3. 空 Claim 是主要压力，不是 LLM/业务执行耗时；
4. 能用同一负载做引入前后对照；
5. MySQL 仍是事实源，Redis 故障时不丢任务。

否则把“不引入 Redis”作为有实验依据的工程决策。

## 9. F：文档、演示和简历收束

### 需要同步的文档

- `README.md`：定位、快速启动、架构图和演示路径；
- `docs/PROJECT_STATUS.md`：真实完成状态；
- `docs/ACCEPTANCE_CHECKLIST.md`：实际运行项；
- `docs/experiments/*.md`：环境、步骤、结果、结论和边界；
- `docs/RESUME_BULLETS.md`：只写有证据的数字；
- `docs/INTERVIEW_GUIDE.md`：失败案例和技术取舍。

不要提交 Token、数据库密码、DeepSeek Key 或含密钥的截图。

### 五分钟演示

```text
第 1 分钟：同步 HTTP 为什么不适合数十秒 LLM 任务。
第 2 分钟：MemoBridge 创建任务，Dashboard 展示状态和事件。
第 3 分钟：解释 Claim、Lease、Heartbeat、Token 和版本 fencing。
第 4 分钟：删除 Worker，展示 task_recovered 和最终成功。
第 5 分钟：展示压测、at-least-once 边界和 Redis 决策。
```

### 面试必须能回答

1. TaskPulse 与 MQ、Asynq、Temporal 的边界是什么？
2. 为什么使用 MySQL `FOR UPDATE SKIP LOCKED`？
3. Lease、Heartbeat、Lease Token 和版本号分别解决什么？
4. 为什么只能承诺 at-least-once，不能承诺外部副作用 exactly-once？
5. Complete 响应丢失后如何处理？
6. Worker 崩溃与优雅退出的事件链有何不同？
7. 幂等键为什么包含 `source_item_id + content_hash + prompt_version`？
8. 为什么没有引入 Redis/Kafka/etcd？什么数据会触发演进？
9. TaskPulse 与 MemoBridge 为什么不共享数据库？
10. 多个 TaskPulse Server 如何保证一个有效持有者？

## 10. 最终停止线

- [ ] 静态质量门禁全部通过；
- [ ] 协议冒烟通过；
- [ ] Compose 一键闭环通过并保存证据；
- [ ] Kubernetes 多副本、强制崩溃、滚动重启和优雅交接通过；
- [ ] 性能基线有固定配置、原始数据和结论；
- [ ] README、状态、实验、简历和面试文档一致；
- [ ] 五分钟演示可以独立完成；
- [ ] 上述十个问题可以不背稿解释。

达到停止线后，不再新增 Kafka、Redis、DAG、Raft、etcd 或服务网格。剩余时间优先投入 Go、MySQL、计算机网络、操作系统、算法和项目面试模拟。

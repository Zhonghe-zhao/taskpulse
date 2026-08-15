# TaskPulse MySQL 调度基准手册

本手册测量当前基于 MySQL 的轮询调度器，为是否引入 Redis 提供证据。它不是 Redis 性能宣传，也不能用 LLM 本身的慢响应来证明 MySQL 有瓶颈。

## 前置条件

- TaskPulse 可从 `http://127.0.0.1:8085` 访问。
- MySQL 健康。
- 目标 workflow 至少有一个可用 Worker。
- Worker ID 必须唯一。Kubernetes 使用 Pod 名；Compose 在 `TASKPULSE_EXTERNAL_WORKER_ID` 为空时从容器主机名派生 ID。
- 使用独立的 Compose/Kubernetes 环境，启动前不得存在 `queued`、`retrying` 或 `running` 任务。基准程序默认检查此条件，并按 workflow 读取 Claim 指标；任务创建出现任何错误时会取消已创建任务并终止本轮，不能把部分成功的结果用于比较。

为测量“分发”而非 LLM 执行时间，请给 fake LLM 设置确定的短延迟，并按测试轮次扩缩容：

```powershell
$env:TASKPULSE_LLM_FAKE_DELAY = "100ms"
$env:TASKPULSE_EXTERNAL_POLL_INTERVAL = "200ms"
docker compose up -d --scale llm-worker=32
```

完成 100ms 轮次后，可使用 `1s` 再跑一次，观察真实长任务场景。两种延迟的结果不能混在同一张对比表中。

`TASKPULSE_EXTERNAL_POLL_INTERVAL` 才是 Worker 空闲时发送 Claim 请求的实际轮询间隔。修改它后应使用
`docker compose up -d --force-recreate --scale llm-worker=<数量>` 让 Worker 读取新配置。

## 执行

### 空队列轮询压力

先单独测量空队列场景。它回答的是“Worker 没有任务时，对 MySQL 产生多少无效 Claim 请求”，不能由批量
任务吞吐结果替代。保持队列为空，设置 Worker 数和实际 Claim 间隔后执行：

```powershell
$env:TASKPULSE_EXTERNAL_POLL_INTERVAL = "200ms"
docker compose up -d --force-recreate --scale llm-worker=32

.\scripts\capture-mysql-baseline.ps1 -Label before-idle-w32-p200 -Workflow llm_analysis
.\scripts\measure-idle-claim-pressure.ps1 `
  -WorkerCount 32 `
  -Workflow llm_analysis `
  -DurationSeconds 60 `
  -Label idle-w32-p200
.\scripts\capture-mysql-baseline.ps1 -Label after-idle-w32-p200 -Workflow llm_analysis
```

在 32 Worker、200ms Claim 间隔下，理论上限约为 `32 / 0.2 = 160` 次 Claim/秒。实际数值受调度和网络抖动
影响，不要求完全相等；关注的是空 Claim 比例是否接近 1、MySQL CPU/连接/锁等待是否因此显著增加，以及不同
轮询间隔的趋势。

### 有任务时的调度吞吐

每一轮负载的前后都立刻捕获 MySQL 证据：

```powershell
.\scripts\capture-mysql-baseline.ps1 -Label before-mysql-10000-w32-p200
```

采集脚本默认查询 `taskpulse` 数据库；若你通过 `TASKPULSE_MYSQL_DATABASE` 使用其他库名，必须同步传入
`-Database <库名>`，否则表级 I/O 快照不会对应本轮任务表。

```powershell
go run ./cmd/dispatch-benchmark --base-url http://127.0.0.1:8085 --tasks 1000
```

建议的正式命令：

```powershell
go run ./cmd/dispatch-benchmark `
  --base-url http://127.0.0.1:8085 `
  --tasks 10000 `
  --create-workers 32 `
  --status-workers 32 `
  --status-poll-interval 200ms `
  --output .\artifacts\benchmarks\mysql-10000-w32-p200.json `
  --timeout 30m

.\scripts\capture-mysql-baseline.ps1 -Label after-mysql-10000-w32-p200
```

采集脚本将 Docker 资源、MySQL 连接、查询数、慢查询、InnoDB 锁等待、表 I/O、`tasks` 索引和两条
workflow Claim 查询的执行计划写入 `artifacts/mysql`。基准工具将任务级 JSON 报告写入 `artifacts/benchmarks`。

命令输出并持久化以下指标：

- completed, failed, and unfinished tasks;
- total duration and throughput;
- queue wait p50/p95/p99/max;
- total task time p50/p95/p99/max;
- claim attempts, successful claims, claim misses, and empty-claim ratio.

`--status-workers` 和 `--status-poll-interval` 只控制基准程序读取任务状态的观测流量，不改变 Worker Claim
轮询。避免基准自身为每个任务串行发起大量 `GET /tasks/{id}` 请求，从而把观测流量误当成调度压力。
`duration_seconds` 与 `throughput` 表示从开始提交这批任务到全部终态的端到端时间；队列等待和任务总时间仍按每个任务自己的 `created_at` 计算。

指定 `--output` 后，JSON 会包含负载参数和全部测量值。Redis 决策以这些原始文件为准，不要只手工抄终端中的数字。

## 推荐矩阵

保持任务输入和 fake 执行延迟不变。使用 2、8、32、64 个 Worker 运行同一负载，然后分别设置
`TASKPULSE_EXTERNAL_POLL_INTERVAL=200ms/1s/2s` 重复。每一轮必须记录 MySQL CPU、活跃连接、锁等待和 TaskPulse JSON 报告。

建议顺序：先 `1000 tasks / 8 workers / 1s poll` 验证环境，再运行 `10000 tasks / 32 workers / 200ms poll`。只有机器资源允许且 10,000 轮出现调度压力时，才继续 100,000 任务。

## 判定规则

不要只因任务数量大就增加 Redis。只有同时能说明 MySQL 轮询/Claim 路径是瓶颈、目标延迟或吞吐未满足、且可复现的替代方案确实降低该瓶颈，同时不削弱 MySQL 任务状态持久性时，才进入 Redis 实施。

若 100ms 轮次的队列等待 p95 很低、MySQL 锁等待很低，但 1s 轮次总耗时很高，结论应是“执行端主导，Redis 无法解决”，而不是强行引入 Redis。

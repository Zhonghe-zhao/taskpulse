# 数据库迁移

## 当前迁移

`000001_init.up.sql` 创建：

- `tasks`：任务当前状态、输入输出、重试、租约和幂等信息。
- `task_events`：任务生命周期事件。

`000002_binary_idempotency_key.up.sql` 将 `idempotency_key` 改为
`VARBINARY(128)`，使 MySQL 与内存实现都按照原始字节精确比较幂等键。

`000005_task_list_indexes.up.sql` 为运维控制台的任务时间线、状态筛选和
Workflow 筛选增加倒序分页索引。

`000006_task_last_heartbeat.up.sql` 独立记录 Worker 最近一次成功 Heartbeat，
避免用包含进度更新和状态迁移的 `updated_at` 冒充心跳时间。

`000001_init.down.sql` 按外键依赖的逆序删除表。

## 本地初始化

`compose.yaml` 将所有 up 迁移按编号挂载到 MySQL 的 `/docker-entrypoint-initdb.d/`。
MySQL 官方镜像只在数据目录为空时执行初始化脚本。

因此：

- 第一次创建 `mysql_data` 数据卷时会自动建表。
- 新增迁移文件后，仅重启已有容器不会执行该迁移。
- 开发阶段需要重新初始化时，应明确删除本项目的数据卷；该操作会清空本地数据库，不能用于生产环境。
- 已有数据库应通过 MySQL 客户端单独执行尚未应用的 up 迁移。

当前仍由 Compose 在空数据卷中执行迁移。继续演进 Schema 前应接入正式迁移工具，
记录迁移版本并按编号追加迁移，不能修改已经执行过的迁移。

## 时间约定

应用和数据库统一使用 UTC，数据库时间字段使用 `DATETIME(6)`。进入或离开 HTTP 边界时再决定展示时区。

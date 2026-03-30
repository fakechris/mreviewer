# Admin Dashboard

## Routes

- `/admin/`
- `/admin/api/queue`
- `/admin/api/concurrency`
- `/admin/api/failures`

## Auth

如果配置了 `MREVIEWER_ADMIN_TOKEN` 或 `ADMIN_TOKEN`，访问这些页面和接口时必须带：

```http
Authorization: Bearer <token>
```

如果没有配置 token，路由会开放在内网环境中，但生产环境不建议这样使用。

## What To Look At

### Queue

`/admin/api/queue` 提供：

- pending queue 数量
- retry-scheduled queue 数量
- 最老等待时长
- queue depth 最高的项目
- 近 24 小时 `superseded_by_new_head` 数量

### Concurrency

`/admin/api/concurrency` 提供：

- 当前 active worker
- 每个 worker 的 `configured_concurrency`
- 每个 worker 当前 `running_runs`
- 总运行数和总容量

### Failures

`/admin/api/failures` 提供：

- 最近失败 run
- 按 `error_code` 聚合的失败桶
- webhook `rejected`
- webhook `deduplicated`

## Relationship With Grafana

Grafana 更适合看趋势、历史时序和容量变化；`/admin/` 更适合看“现在到底卡在哪里”。

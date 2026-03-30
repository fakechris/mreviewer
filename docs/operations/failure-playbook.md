# Failure Playbook

## Common Failure Codes

### `superseded_by_new_head`

含义：

- 同一个 MR 来了更新的 head
- 旧 run 被 `latest-head-wins` 语义替换

处理方式：

- 通常不需要人工处理
- 到 `/admin/api/queue` 确认是否有新的 run 已进入队列或正在运行；请求时带上 `Authorization: Bearer <token from MREVIEWER_ADMIN_TOKEN>`

### `provider_failed`

含义：

- LLM provider 返回错误、超时或协议异常

处理方式：

- 查看 `/admin/api/failures`；请求时带上 `Authorization: Bearer <token from MREVIEWER_ADMIN_TOKEN>`
- 用 `scripts/show-run-audit.sh --latest` 或指定 run id 检查 provider audit detail
- 检查 provider key、base URL、model、rate limit

### `worker_timeout`

含义：

- run 被 scheduler reaper 视为超时

处理方式：

- 查看 `/admin/api/concurrency` 是否有 worker 心跳缺失；请求时带上 `Authorization: Bearer <token from MREVIEWER_ADMIN_TOKEN>`
- 检查 worker 日志、数据库连接和 provider latency
- 确认是否需要提升 worker 数量或 provider 并发

## Webhook-Level Failures

### `rejected`

常见原因：

- webhook secret 错误
- 请求体损坏
- payload 不符合支持格式

### `deduplicated`

常见原因：

- GitLab 重试了同一个 delivery
- 相同 delivery key 被重复投递

这通常不是故障，而是系统按设计去重。

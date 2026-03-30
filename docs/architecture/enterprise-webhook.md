# Enterprise Webhook Architecture

## Goal

把 GitLab Review 链路从“Webhook 收到后发生了什么只能看日志”升级成企业可控的异步控制面。

## Request Path

1. GitLab 发送 Merge Request webhook 到 `ingress`
2. `ingress` 验签、去重、记录 `hook_events` / `audit_logs`
3. `ingress` 创建或更新 `review_runs`
4. `worker` 异步 claim run，调用 LLM，回写 GitLab discussion
5. `worker_heartbeats` 持久化活跃 worker、并发容量和最后心跳时间
6. `/admin/` 和 `/admin/api/*` 用数据库读模型暴露实时 control-plane 状态

## Queue Semantics

企业默认语义是 `latest-head-wins`：

- 同一个 MR 出现新的 `head_sha` 时，旧的 pending run 会被 superseded
- 旧的 running run 会被标记取消，并在 processor / writer 的关键写点停止继续产出
- superseded run 会保留审计痕迹，错误码为 `superseded_by_new_head`

这让系统能在高频 push 场景下优先处理最新版本，而不是被旧 diff 拖住。

## Concurrency Model

- `worker` 通过 scheduler 并发处理 review run
- `worker_heartbeats` 记录 durable worker presence 与 `configured_concurrency`
- `review_runs.claimed_by` 表示当前 run 被哪个 worker 占用
- Grafana 负责趋势与历史时序
- `/admin/` 负责当前队列、当前并发、当前失败态

## Control Plane

第一阶段 control plane 直接嵌在 `ingress`：

- `/admin/`
- `/admin/api/queue`
- `/admin/api/concurrency`
- `/admin/api/failures`

这些接口默认只读，并支持 `Authorization: Bearer <ADMIN_TOKEN>`。

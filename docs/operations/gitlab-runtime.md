# GitLab Runtime

## Webhook

GitLab webhook 入口：

- `POST /webhook`

需要：

- `GITLAB_BASE_URL`
- `GITLAB_TOKEN`
- `GITLAB_WEBHOOK_SECRET`
- 可选：`REVIEW_PACKS`
- 可选：`REVIEW_MODEL_CHAIN`
- 可选：`REVIEW_ADVISOR_CHAIN`
- 可选：`REVIEW_COMPARE_REVIEWERS`

建议 webhook 事件：

- `Merge request events`
- 可选：`Note events`

## Worker Path

GitLab 自动运行面当前支持：

- webhook normalize
- run 创建
- snapshot 抓取
- review engine / legacy runtime 共享调度
- selected reviewer packs
- advisor second opinion
- external reviewer comparison
- external status
- summary note / inline discussion writeback

## Status Lifecycle

GitLab status context：

- `mreviewer/ai-review`

阶段包括：

- `loading_target`
- `running_packs`
- `running_advisor`
- `publishing`
- `comparing_external`
- `comparing_targets`
- `completed`

## CLI Smoke Test

```bash
go run ./cmd/mreviewer \
  --target https://gitlab.example.com/group/service/-/merge_requests/17 \
  --output both \
  --publish full-review-comments
```

## Manual Trigger Smoke Test

```bash
go run ./cmd/manual-trigger \
  --project-id 123 \
  --mr-iid 456 \
  --wait --json
```

## 运行问题排查

优先检查：

- `GITLAB_TOKEN` 权限
- `GITLAB_WEBHOOK_SECRET` 是否和项目 webhook 一致
- GitLab 是否能访问 `ingress`
- `worker` 是否能访问 GitLab API
- GitLab 实例是否允许 webhook 访问局域网地址

如果你是本机联调，配合根目录的 [WEBHOOK.md](../../WEBHOOK.md) 一起看。

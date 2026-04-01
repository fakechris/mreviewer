# GitHub Runtime

## Webhook

GitHub webhook 入口：

- `POST /github/webhook`

需要：

- `GITHUB_BASE_URL`
- `GITHUB_TOKEN`
- `GITHUB_WEBHOOK_SECRET`
- 可选：`REVIEW_PACKS`
- 可选：`REVIEW_ADVISOR_ROUTE`
- 可选：`REVIEW_COMPARE_REVIEWERS`

建议 webhook 事件：

- `Pull requests`

## Worker Path

GitHub 自动运行面当前支持：

- webhook normalize
- run 创建
- snapshot 抓取
- review engine 执行
- selected reviewer packs
- advisor second opinion
- external reviewer comparison
- GitHub commit status
- summary + inline review comment writeback

## Status Lifecycle

GitHub status context：

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
  --target https://github.com/acme/service/pull/24 \
  --output both \
  --publish full-review-comments
```

## 运行问题排查

优先检查：

- `GITHUB_TOKEN` 权限
- `GITHUB_WEBHOOK_SECRET` 是否和仓库 webhook 一致
- `ingress` 是否暴露了 `/github/webhook`
- `worker` 是否能访问 GitHub API

如果只是本机联调，确认远端能访问当前机器，不要把 webhook 指到 `localhost`。

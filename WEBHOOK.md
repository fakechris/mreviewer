# Webhook Setup

`mreviewer` 支持两类 webhook 入口：

- GitLab: `POST /webhook`
- GitHub: `POST /github/webhook`

默认 `ingress` 监听端口是 `3100`，所以常见地址是：

- GitLab: `http(s)://<your-host>:3100/webhook`
- GitHub: `http(s)://<your-host>:3100/github/webhook`

## GitLab

1. 进入目标项目 `Settings -> Webhooks`
2. 配置：
   - URL: `http(s)://<your-host>:3100/webhook`
   - Secret token: 与 `.env` 里的 `GITLAB_WEBHOOK_SECRET` 一致
   - Trigger: 勾选 `Merge request events`
   - 可选：如果你希望评论命令触发，也勾 `Note events`
3. 点击 `Add webhook`
4. 在项目里创建或更新一个 MR，确认 `ingress` 收到 webhook

## GitHub

1. 进入目标仓库 `Settings -> Webhooks`
2. 点击 `Add webhook`
3. 配置：
   - Payload URL: `http(s)://<your-host>:3100/github/webhook`
   - Content type: `application/json`
   - Secret: 与 `.env` 里的 `GITHUB_WEBHOOK_SECRET` 一致
   - Event: 选择 `Let me select individual events`
   - 勾选 `Pull requests`
4. 保存 webhook
5. 打开或更新一个 PR，确认 `ingress` 收到 webhook

## 本机联调

如果你把 `ingress` 直接跑在本机，远端 GitHub / GitLab 访问不到 `localhost`。联调时应使用：

- 当前机器的局域网 IP 或可访问域名
- 或一条公网 HTTPS tunnel

例如：

- `http://10.0.0.16:3100/webhook`
- `http://10.0.0.16:3100/github/webhook`

## GitLab 本地网络限制

某些 GitLab 实例默认不允许 webhook 访问局域网地址。如果你在 GitLab 配 webhook 时看到：

- `Invalid url given`

通常不是 `mreviewer` 的问题，而是 GitLab 实例策略拦住了本地网络地址。需要 GitLab 管理员打开：

- `Allow requests to the local network from web hooks and services`

如果你不想改实例策略，就改用一个公网 HTTPS 地址。

## 快速检查

服务启动后，至少确认：

- `GET /health` 返回 `200`
- GitLab `POST /webhook` 能到达本机
- GitHub `POST /github/webhook` 能到达本机
- `worker` 能把 review 结果回写到对应平台

## 相关环境变量

- `PORT`
- `GITLAB_BASE_URL`
- `GITLAB_TOKEN`
- `GITLAB_WEBHOOK_SECRET`
- `GITHUB_BASE_URL`
- `GITHUB_TOKEN`
- `GITHUB_WEBHOOK_SECRET`

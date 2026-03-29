# GitLab Webhook 配置指南

## 方式 1: 逐个项目配置（所有版本适用）

在每个 GitLab 项目中：

1. 进入 **Settings → Webhooks**
2. 填写：
   - URL: `http://your-server:3100/webhook`
   - Secret Token: `.env` 中的 `GITLAB_WEBHOOK_SECRET`
   - Trigger: 勾选 **Merge request events**
3. 点击 **Add webhook**

## 方式 2: 组级别配置（Premium/Ultimate）

如果你有 GitLab Premium 或 Ultimate：

1. 进入 **Group → Settings → Webhooks**
2. 配置同上
3. 组内所有项目自动生效

## 方式 3: 全实例配置（管理员）

如果你是 GitLab 管理员：

1. 进入 **Admin Area → System Hooks**
2. 填写：
   - URL: `http://your-server:3100/webhook`
   - Secret Token: `.env` 中的 `GITLAB_WEBHOOK_SECRET`
   - Trigger: 勾选 **Merge Request Hook**
3. 点击 **Add system hook**
4. 全实例所有项目自动生效

## 验证 Webhook

创建或更新一个 MR，检查：

```bash
# 查看 ingress 日志
docker-compose logs ingress | grep webhook

# 查看 worker 处理日志
docker-compose logs worker | grep "review run"
```

成功标志：
- Ingress 收到 webhook 请求（200 响应）
- Worker 开始处理 review run
- GitLab MR 页面出现 AI 评论

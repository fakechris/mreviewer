# GitLab Webhook 配置指南

本文档提供三种 Webhook 配置方式的详细步骤。

---

## 企业语义概览

- Webhook 入口只做验签、去重、审计、入队和快速返回
- Review 队列采用 `latest-head-wins` 语义
- 同一个 MR 出现更新 head 时，旧的 run 会被 superseded，而不是继续无限排队
- 运维可以通过 `/admin/`、`/admin/api/queue`、`/admin/api/concurrency`、`/admin/api/failures` 观察当前状态
- Webhook、`manual-trigger` 和新的 `mreviewer` CLI 现在共享同一条 review engine 主路径；Webhook 不再是独立实现

这意味着 Webhook 不是同步调用大模型的黑盒链路，而是企业可观测的异步控制面。

## 当前主路径

当前有三条入口，但核心 review engine 已共享：

- `ingress / webhook`: 自动接收 GitLab Merge Request 事件
- `manual-trigger`: 由容器内命令手动触发同一条 review pipeline
- `mreviewer` CLI: 直接对 GitHub / GitLab PR URL 运行 portable review council

这三条入口最终都会进入同一套 reviewer packs、judge、canonical bundle 和 write-back 语义，因此 webhook 链路与 CLI 链路的行为差异已经大幅收敛。

## 方式 1: 项目级别配置（所有版本适用）

### 适用场景
- 任何 GitLab 版本（CE/EE）
- 只需要为特定项目启用 AI Review
- 无需管理员权限

### 配置步骤

1. **进入项目设置**
   - 打开你的 GitLab 项目
   - 左侧菜单：`Settings` → `Webhooks`

2. **添加 Webhook**
   - **URL**: `http://your-server:3100/webhook`
     - 替换 `your-server` 为实际服务器地址
     - 如果是本机开发、GitLab 也跑在同一台机器上：`http://localhost:3100/webhook`
     - 如果是局域网联调，必须改成你机器的局域网 IP，例如：`http://10.0.0.16:3100/webhook`
     - 不要在远端 GitLab 上配置 `localhost`，那只会指向 GitLab 自己

   - **Secret token**:
     - 复制 `.env` 文件中的 `GITLAB_WEBHOOK_SECRET` 值
     - 粘贴到此处

   - **Trigger**:
     - ✅ 勾选 `Merge request events`
     - 其他选项保持默认（不勾选）

   - **SSL verification**:
     - 生产环境建议启用
     - 本地测试可以取消勾选

   - **GitLab 实例设置（局域网联调时必看）**:
     - 如果 GitLab 在保存 webhook 时返回 `Invalid url given`
     - 需要 GitLab 管理员在实例级打开 `Allow requests to the local network from web hooks and services`
     - 如果不想改实例设置，请改用公网 HTTPS tunnel，而不是直接使用局域网 IP

3. **保存并测试**
   - 点击 `Add webhook` 按钮
   - 在 Webhook 列表中找到刚添加的 webhook
   - 点击 `Test` → `Merge Request events`
   - 应该看到 HTTP 200 响应
   - 注意：GitLab 的测试事件只验证连通性和 secret，不一定会创建真实 review run；完整验证要靠真实创建或更新一个 MR

### 验证
创建或更新一个 MR，检查：
```bash
docker compose logs ingress | grep "webhook"
docker compose logs worker | grep "processing review run"
```

---

## 方式 2: 组级别配置（Premium/Ultimate）

### 适用场景
- GitLab Premium 或 Ultimate 版本
- 需要为整个组（Group）的所有项目启用
- 有组的 Owner 或 Maintainer 权限

### 配置步骤

1. **进入组设置**
   - 打开你的 GitLab 组（Group）页面
   - 左侧菜单：`Settings` → `Webhooks`
   - 注意：如果看不到此选项，说明你的 GitLab 版本不是 Premium/Ultimate

2. **添加组级 Webhook**
   - **URL**: `http://your-server:3100/webhook`
   - **Secret token**: `.env` 中的 `GITLAB_WEBHOOK_SECRET`
   - **Trigger**: ✅ 勾选 `Merge request events`
   - 其他配置同项目级别

3. **保存**
   - 点击 `Add webhook`
   - 此 webhook 将自动应用到组内所有项目

### 验证
在组内任意项目创建 MR，应该触发 review。

---

## 方式 3: 全实例配置（管理员级别）

### 适用场景
- 需要为整个 GitLab 实例的所有项目启用
- 你是 GitLab 管理员（Admin）
- 适用于 GitLab CE 和 EE

### 配置步骤

1. **进入管理员区域**
   - 点击顶部导航栏右上角的 **扳手图标** 或你的头像
   - 选择 `Admin Area`（管理中心）
   - 如果看不到此选项，说明你不是管理员

2. **进入 System Hooks 设置**
   - 左侧菜单：`System Hooks`
   - 路径：`Admin Area` → `System Hooks`
   - 或直接访问：`https://your-gitlab.com/admin/hooks`

3. **添加 System Hook**
   - **URL**: `http://your-server:3100/webhook`
     - 注意：System Hook 的 URL 必须是服务器可访问的地址
     - 不能使用 `localhost`（除非 GitLab 和 mreviewer 在同一台机器）

   - **Secret Token**:
     - 复制 `.env` 文件中的 `GITLAB_WEBHOOK_SECRET`
     - 粘贴到此处

   - **Trigger**:
     - ✅ 勾选 `Merge Request Hook`
     - 其他选项保持默认

   - **Enable SSL verification**:
     - 生产环境建议启用
     - 内网测试可以取消勾选

4. **保存**
   - 点击 `Add system hook` 按钮
   - 此 hook 将应用到实例内所有项目

5. **测试（可选）**
   - 在 System Hooks 列表中找到刚添加的 hook
   - 点击 `Test` → `Merge Request Hook`
   - 应该看到 HTTP 200 响应

### 验证
在任意项目创建 MR，应该触发 review。

---

## 常见问题

### Q: Webhook 返回 401/403 错误
A: 检查 Secret Token 是否正确，确保与 `.env` 中的 `GITLAB_WEBHOOK_SECRET` 一致。

### Q: Webhook 返回 Connection refused
A: 检查 mreviewer 服务是否启动，端口 3100 是否可访问。

### Q: 保存 webhook 时 GitLab 返回 `Invalid url given`
A:
1. 确认你填的不是 `localhost`，而是 mreviewer 机器可从 GitLab 访问到的地址
2. 如果你填的是局域网 IP，联系 GitLab 管理员打开 `Allow requests to the local network from web hooks and services`
3. 如果不能改 GitLab 实例设置，就使用公网 HTTPS tunnel

### Q: MR 创建后没有触发 review
A:
1. 检查 webhook 是否配置成功
2. 查看 ingress 日志：`docker-compose logs ingress`
3. 查看 worker 日志：`docker-compose logs worker`

### Q: 为什么同一个 MR 连续 push 时，旧 review run 消失了？
A: 这是 `latest-head-wins` 的预期行为。旧 run 会被标记为 superseded / cancelled，避免对过期 diff 继续消费模型和写回评论。

### Q: 如何确认 webhook 当前有没有堆积？
A: 打开 `/admin/`，或查询 `/admin/api/queue`。这里能直接看到 pending queue、retry-scheduled queue、以及近 24 小时 superseded run 数。

### Q: 如何禁用某个项目的 AI Review
A: 在项目的 `.gitlab/ai-review.yaml` 中添加：
```yaml
enabled: false
```

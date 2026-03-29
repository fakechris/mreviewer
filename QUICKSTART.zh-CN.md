# 快速开始指南

本指南提供两种部署方式，适合不同技术背景的用户。

---

## 方式 1：极简部署（推荐新手）

**适合人群**：不熟悉 Git，只想快速试用

### 步骤 1：下载配置文件

创建一个新文件夹，下载以下两个文件：

1. **docker-compose.prod.yaml**
   - 访问：https://raw.githubusercontent.com/fakechris/mreviewer/main/docker-compose.prod.yaml
   - 右键 → 另存为 → 保存到你的文件夹

2. **.env**
   - 访问：https://raw.githubusercontent.com/fakechris/mreviewer/main/.env.prod.example
   - 右键 → 另存为 → 重命名为 `.env`（注意开头有个点）

### 步骤 2：编辑 .env 文件

用文本编辑器打开 `.env`，填入你的信息：

```bash
# 你的 GitLab 地址
GITLAB_BASE_URL=https://gitlab.example.com

# GitLab Personal Access Token（需要 api 权限）
GITLAB_TOKEN=glpat-xxxxxxxxxxxx

# Webhook 密钥（随便设置一个复杂字符串）
GITLAB_WEBHOOK_SECRET=my_secret_key_123

# MiniMax API Key
MINIMAX_API_KEY=your_minimax_key
MINIMAX_GROUP_ID=your_group_id
```

### 步骤 3：启动服务

打开终端（命令行），进入你的文件夹：

```bash
cd /path/to/your/folder
docker-compose -f docker-compose.prod.yaml up -d
```

### 步骤 4：验证运行

```bash
# 查看服务状态
docker-compose -f docker-compose.prod.yaml ps

# 查看日志
docker-compose -f docker-compose.prod.yaml logs -f worker
```

看到 "worker started" 就成功了！

---

## 方式 2：完整部署（推荐开发者）

**适合人群**：熟悉 Git，需要自定义配置或参与开发

### 步骤 1：克隆仓库

```bash
git clone https://github.com/fakechris/mreviewer.git
cd mreviewer
```

### 步骤 2：配置环境变量

```bash
cp .env.example .env
# 编辑 .env 填入你的凭证
```

### 步骤 3：启动服务

```bash
docker-compose up -d
```

### 步骤 4：查看日志

```bash
docker-compose logs -f worker
```

---

## 配置 GitLab Webhook

无论使用哪种方式，都需要配置 GitLab Webhook 才能自动触发代码审查。

详细步骤请参考：[WEBHOOK.md](./WEBHOOK.md)

**快速配置**：
1. 进入 GitLab 项目 → Settings → Webhooks
2. URL: `http://your-server:3100/webhook`
3. Secret: 填入 `.env` 中的 `GITLAB_WEBHOOK_SECRET`
4. 勾选 "Merge request events"
5. 点击 "Add webhook"

---

## 手动触发审查（可选）

如果不想配置 Webhook，可以手动触发审查。

### 前置条件

手动触发需要以下配置（通过 `.env` 文件或环境变量）：

```bash
# GitLab 配置（必需）
GITLAB_BASE_URL=https://gitlab.example.com
GITLAB_TOKEN=your_gitlab_token

# LLM 配置（必需）
MINIMAX_API_KEY=your_minimax_key
MINIMAX_GROUP_ID=your_group_id

# 数据库配置（已在 docker-compose 中配置）
MYSQL_DSN=mreviewer:mreviewer_password@tcp(mysql:3306)/mreviewer?parseTime=true
```

### 使用方法

```bash
docker exec -it mreviewer-worker /app/manual-trigger \
  --project-id 123 \
  --mr-iid 456 \
  --wait
```

**参数说明**：
- `--project-id`: GitLab 项目 ID（在项目首页可以看到）
- `--mr-iid`: Merge Request 编号（MR URL 中的数字）
- `--wait`: 等待审查完成并返回结果（可选）
```

---

## 常见问题

### Q: 如何停止服务？

**方式 1 用户**：
```bash
docker-compose -f docker-compose.prod.yaml down
```

**方式 2 用户**：
```bash
docker-compose down
```

### Q: 如何更新到最新版本？

```bash
# 拉取最新镜像
docker-compose pull

# 重启服务
docker-compose up -d
```

### Q: 数据会丢失吗？

不会。数据库数据存储在 Docker volume `mysql_data` 中，即使删除容器也不会丢失。

### Q: 如何查看所有日志？

```bash
docker-compose logs
```

### Q: 端口冲突怎么办？

编辑 `.env` 文件，修改端口：
```bash
PORT=3200          # 改成其他端口
MYSQL_PORT=3307    # 改成其他端口
```

---

## 下一步

- 阅读 [WEBHOOK.md](./WEBHOOK.md) 配置自动触发
- 阅读 [DEPLOYMENT.md](./DEPLOYMENT.md) 了解生产部署
- 查看 [CONTRIBUTING.md](./CONTRIBUTING.md) 参与贡献

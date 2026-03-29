# Docker 部署指南

## 一键启动（本地测试）

```bash
# 1. 配置环境变量
cp .env.example .env
# 编辑 .env 填入 GitLab 和 MiniMax 凭证

# 2. 启动所有服务
docker-compose up -d

# 3. 查看日志
docker-compose logs -f worker
```

## 构建和推送到私有仓库

### 1. 构建镜像

```bash
# 构建 worker
docker build --build-arg TARGET_CMD=./cmd/worker -t your-registry/mreviewer-worker:latest .

# 构建 ingress
docker build --build-arg TARGET_CMD=./cmd/ingress -t your-registry/mreviewer-ingress:latest .
```

### 2. 推送到仓库

```bash
docker push your-registry/mreviewer-worker:latest
docker push your-registry/mreviewer-ingress:latest
```

### 3. 服务器部署

在服务器上创建 `docker-compose.prod.yaml`:

```yaml
services:
  mysql:
    image: mysql:8.4
    restart: unless-stopped
    environment:
      MYSQL_ROOT_PASSWORD: ${MYSQL_ROOT_PASSWORD}
      MYSQL_DATABASE: mreviewer
      MYSQL_USER: mreviewer
      MYSQL_PASSWORD: ${MYSQL_PASSWORD}
    volumes:
      - mysql_data:/var/lib/mysql

  redis:
    image: redis:7
    restart: unless-stopped

  ingress:
    image: your-registry/mreviewer-ingress:latest
    restart: unless-stopped
    ports:
      - "3100:3100"
    env_file: .env
    depends_on:
      - mysql
      - redis

  worker:
    image: your-registry/mreviewer-worker:latest
    restart: unless-stopped
    env_file: .env
    depends_on:
      - mysql
      - redis

volumes:
  mysql_data:
```

启动：

```bash
docker-compose -f docker-compose.prod.yaml up -d
```

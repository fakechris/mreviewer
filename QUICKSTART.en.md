# Quick Start Guide

This guide provides two deployment methods for different user backgrounds.

---

## Method 1: Minimal Setup (Recommended for Beginners)

**For**: Users unfamiliar with Git who want to try quickly

### Step 1: Download Configuration Files

Create a new folder and download these two files:

1. **docker-compose.prod.yaml**
   - Visit: https://raw.githubusercontent.com/fakechris/mreviewer/main/docker-compose.prod.yaml
   - Right-click → Save As → Save to your folder

2. **.env**
   - Visit: https://raw.githubusercontent.com/fakechris/mreviewer/main/.env.prod.example
   - Right-click → Save As → Rename to `.env` (note the leading dot)

### Step 2: Edit .env File

Open `.env` with a text editor and fill in your credentials:

```bash
# Your GitLab URL
GITLAB_BASE_URL=https://gitlab.example.com

# GitLab Personal Access Token (needs api scope)
GITLAB_TOKEN=glpat-xxxxxxxxxxxx

# Webhook secret (set any complex string)
GITLAB_WEBHOOK_SECRET=my_secret_key_123

# MiniMax API Key
MINIMAX_API_KEY=your_minimax_key
MINIMAX_GROUP_ID=your_group_id
```

### Step 3: Start Services

Open terminal and navigate to your folder:

```bash
cd /path/to/your/folder
docker-compose -f docker-compose.prod.yaml up -d
```

### Step 4: Verify

```bash
# Check service status
docker-compose -f docker-compose.prod.yaml ps

# View logs
docker-compose -f docker-compose.prod.yaml logs -f worker
```

Success when you see "worker started"!

---

## Method 2: Full Setup (Recommended for Developers)

**For**: Users familiar with Git who need customization or development

### Step 1: Clone Repository

```bash
git clone https://github.com/fakechris/mreviewer.git
cd mreviewer
```

### Step 2: Configure Environment

```bash
cp .env.example .env
# Edit .env with your credentials
```

### Step 3: Start Services

```bash
docker-compose up -d
```

### Step 4: View Logs

```bash
docker-compose logs -f worker
```

---

## Configure GitLab Webhook

Both methods require GitLab Webhook configuration for automatic code review.

See detailed steps: [WEBHOOK.md](./WEBHOOK.md)

**Quick Setup**:
1. Go to GitLab project → Settings → Webhooks
2. URL: `http://your-server:3100/webhook`
3. Secret: Value from `GITLAB_WEBHOOK_SECRET` in `.env`
4. Check "Merge request events"
5. Click "Add webhook"

---

## Manual Trigger (Optional)

Trigger review without webhook:

```bash
docker exec -it mreviewer-worker /app/manual-trigger \
  --project-id 123 \
  --mr-iid 456
```

---

## FAQ

### Q: How to stop services?

**Method 1 users**:
```bash
docker-compose -f docker-compose.prod.yaml down
```

**Method 2 users**:
```bash
docker-compose down
```

### Q: How to update to latest version?

```bash
# Pull latest images
docker-compose pull

# Restart services
docker-compose up -d
```

### Q: Will data be lost?

No. Database data is stored in Docker volume `mysql_data` and persists even after container removal.

### Q: How to view all logs?

```bash
docker-compose logs
```

### Q: Port conflict?

Edit `.env` file to change ports:
```bash
PORT=3200          # Change to another port
MYSQL_PORT=3307    # Change to another port
```

---

## Next Steps

- Read [WEBHOOK.md](./WEBHOOK.md) for automatic triggering
- Read [DEPLOYMENT.md](./DEPLOYMENT.md) for production deployment
- See [CONTRIBUTING.md](./CONTRIBUTING.md) to contribute

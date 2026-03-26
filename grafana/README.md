# MReviewer Grafana Dashboards

Pre-built dashboard templates for monitoring review operations, provider performance, and finding quality.

## Prerequisites

- Grafana 9+ (tested with Grafana 11)
- MySQL datasource configured to point at the mreviewer database

## Setup

### 1. Configure MySQL Datasource

In Grafana → Configuration → Data Sources → Add data source → MySQL:

| Field | Value |
|-------|-------|
| Host | `your-mysql-host:3306` |
| Database | `mreviewer` |
| User | Read-only user recommended |
| TLS | Match your MySQL config |

### 2. Import Dashboards

For each JSON file in `grafana/dashboards/`:

1. Go to Dashboards → Import
2. Upload or paste the JSON file
3. Select your MySQL datasource when prompted

### Dashboards

| Dashboard | File | Description |
|-----------|------|-------------|
| Review Operations | `review-operations.json` | Throughput, success rate, error distribution, retry analysis, write action success |
| Provider Performance | `provider-performance.json` | Latency percentiles, token consumption, latency vs token scatter, audit log table |
| Finding Quality | `finding-quality.json` | Severity/category breakdown, confidence distribution, state analysis, top files |

## Data Sources

All queries use the `review_runs`, `review_findings`, `comment_actions`, `gitlab_discussions`, and `audit_logs` tables. No additional data export or Prometheus setup required — Grafana queries MySQL directly.

### Key Tables

- **review_runs** — `provider_latency_ms`, `provider_tokens_total`, `status`, timestamps
- **review_findings** — `severity`, `confidence`, `category`, `state`, `path`
- **comment_actions** — `action_type`, `status`, `latency_ms`
- **audit_logs** — `action`, `detail` (JSON with provider model/latency/tokens)
- **gitlab_discussions** — `resolved` status for resolution tracking

## Customization

All dashboards use a `${DS_MYSQL}` template variable for the datasource. To use a different datasource name, update the variable in each dashboard's settings.

Time ranges default to 7 days (operations/performance) and 30 days (quality). Adjust via Grafana's time picker.

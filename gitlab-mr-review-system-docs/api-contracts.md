# API Contracts：GitLab MR Review System

版本：v0.1
日期：2026-03-16

## 1. 设计原则

- 外部输入先归一化，再进入内部管线。
- 内部 contract 稳定，尽量不让上游 GitLab payload 或下游 provider response 污染核心域模型。
- 所有 contract 都要带 `schema_version`。

## 2. NormalizedWebhookEvent

```json
{
  "schema_version": "1.0",
  "event_id": "evt_01H...",
  "source_type": "system_hook",
  "delivery_key": "gitlab:instance-a:merge_request:123456789",
  "gitlab_instance": {
    "id": "gl_inst_prod",
    "base_url": "https://gitlab.example.com"
  },
  "project": {
    "gitlab_project_id": 1001,
    "full_path": "payments/api"
  },
  "merge_request": {
    "iid": 42,
    "gitlab_mr_id": 987654,
    "action": "update",
    "state": "opened",
    "source_branch": "feature/fix-timezone",
    "target_branch": "main",
    "oldrev": "a1b2c3",
    "head_sha": "d4e5f6"
  },
  "actor": {
    "gitlab_user_id": 12,
    "username": "alice"
  },
  "received_at": "2026-03-16T10:00:00Z",
  "raw_payload_ref": "s3://bucket/webhooks/evt_01H...json"
}
```

### 字段说明

- `delivery_key`：用于 webhook 去重。
- `oldrev`：仅在某些 update/push 相关场景存在。
- `head_sha`：若 payload 不可靠，worker 需在 run 中重新查询。

## 3. Internal ReviewRequest

```json
{
  "schema_version": "1.0",
  "review_run_id": "rr_01H...",
  "project": {
    "project_id": "proj_1001",
    "full_path": "payments/api",
    "default_branch": "main"
  },
  "merge_request": {
    "iid": 42,
    "title": "Fix timezone fallback",
    "description": "...",
    "author": "alice"
  },
  "version": {
    "base_sha": "111111",
    "start_sha": "222222",
    "head_sha": "333333",
    "patch_id_sha": "444444"
  },
  "rules": {
    "platform_policy": "...",
    "project_policy": "...",
    "review_markdown": "# Review Guidelines\n...",
    "rules_digest": "sha256:..."
  },
  "changes": [
    {
      "path": "src/auth/session.ts",
      "status": "modified",
      "generated": false,
      "too_large": false,
      "collapsed": false,
      "hunks": [
        {
          "old_start": 80,
          "old_lines": 4,
          "new_start": 80,
          "new_lines": 6,
          "patch": "@@ -80,4 +80,6 @@ ..."
        }
      ],
      "local_context": {
        "before": "...",
        "after": "..."
      }
    }
  ],
  "historical_context": {
    "active_bot_findings": [
      {
        "semantic_fingerprint": "sf_abc",
        "title": "Missing null guard",
        "path": "src/auth/session.ts"
      }
    ]
  }
}
```

## 4. LLM Response Schema（ReviewResult）

```json
{
  "type": "object",
  "required": ["schema_version", "run_summary", "findings"],
  "properties": {
    "schema_version": {"type": "string"},
    "run_summary": {
      "type": "object",
      "required": ["overall_risk", "summary_markdown"],
      "properties": {
        "overall_risk": {"type": "string", "enum": ["low", "medium", "high"]},
        "summary_markdown": {"type": "string"}
      }
    },
    "findings": {
      "type": "array",
      "items": {
        "type": "object",
        "required": [
          "category",
          "severity",
          "confidence",
          "title",
          "body_markdown",
          "path",
          "anchor"
        ],
        "properties": {
          "category": {"type": "string"},
          "severity": {"type": "string", "enum": ["nit", "low", "medium", "high"]},
          "confidence": {"type": "number", "minimum": 0, "maximum": 1},
          "title": {"type": "string"},
          "body_markdown": {"type": "string"},
          "path": {"type": "string"},
          "anchor": {
            "type": "object",
            "required": ["kind"],
            "properties": {
              "kind": {"type": "string", "enum": ["line", "range", "file", "general"]},
              "old_line": {"type": ["integer", "null"]},
              "new_line": {"type": ["integer", "null"]},
              "start_line": {"type": ["integer", "null"]},
              "end_line": {"type": ["integer", "null"]},
              "snippet": {"type": ["string", "null"]}
            }
          },
          "evidence": {
            "type": "array",
            "items": {"type": "string"}
          },
          "suggested_patch": {
            "type": ["object", "null"],
            "properties": {
              "language": {"type": "string"},
              "content": {"type": "string"}
            }
          },
          "rule_refs": {
            "type": "array",
            "items": {"type": "string"}
          },
          "canonical_key": {"type": ["string", "null"]}
        }
      }
    }
  }
}
```

## 5. FindingDecision（dedupe 之后）

```json
{
  "finding_id": "fd_01H...",
  "decision": "post_new_discussion",
  "matched_existing_finding_id": null,
  "discussion_mode": "diff",
  "resolved_previous_discussion_id": null,
  "reason": "new semantic issue"
}
```

### `decision` 枚举

- `post_new_discussion`
- `skip_duplicate`
- `resolve_existing`
- `supersede_existing`
- `post_file_discussion`
- `post_general_note`

## 6. GitLab Comment Writer Contract

```json
{
  "schema_version": "1.0",
  "review_run_id": "rr_01H...",
  "project": {
    "gitlab_project_id": 1001
  },
  "merge_request": {
    "iid": 42
  },
  "write_operations": [
    {
      "operation_id": "op_001",
      "mode": "diff_discussion",
      "finding_id": "fd_01H...",
      "body_markdown": "**Possible null dereference**\n\n...",
      "position": {
        "position_type": "text",
        "base_sha": "111111",
        "start_sha": "222222",
        "head_sha": "333333",
        "old_path": "src/auth/session.ts",
        "new_path": "src/auth/session.ts",
        "old_line": null,
        "new_line": 87
      },
      "metadata": {
        "anchor_fingerprint": "af_...",
        "semantic_fingerprint": "sf_..."
      }
    }
  ]
}
```

## 7. GitLab ResolveDiscussion Contract

```json
{
  "schema_version": "1.0",
  "project": {
    "gitlab_project_id": 1001
  },
  "merge_request": {
    "iid": 42
  },
  "discussion_id": "2a5f...",
  "resolve": true,
  "reason": "finding fixed in run rr_01H..."
}
```

## 8. External Status Check Update Contract（可选）

```json
{
  "schema_version": "1.0",
  "project": {
    "gitlab_project_id": 1001
  },
  "merge_request": {
    "iid": 42,
    "head_sha": "333333"
  },
  "status_check": {
    "name": "AI Review Summary",
    "status": "failed",
    "summary": "2 high-confidence findings remain unresolved"
  }
}
```

> 注意：如果采用 external status checks，必须保证结果在 GitLab 的 pending 超时窗口内完成，或把该机制仅用于快速摘要而非深度 review 唯一 gate。

## 9. Rules File Contract

### 9.1 `REVIEW.md`

- UTF-8 Markdown
- 最大大小建议：64 KB
- 可 root 与 directory scoped

### 9.2 `.gitlab/ai-review.yaml`

```yaml
version: 1
review:
  enabled: true
  confidence_threshold: 0.78
  severity_threshold: medium
  include_paths: ["src/**"]
  exclude_paths: ["vendor/**", "**/*.lock"]
  context:
    mode: hunk_plus_local
    lines_before: 20
    lines_after: 20
  publish:
    mode: discussions
    enable_suggestions: false
  gate:
    mode: threads_resolved
```

## 10. Parser Failure Fallback Contract

当 provider 输出无法解析时，系统生成内部 fallback：

```json
{
  "schema_version": "1.0",
  "review_run_id": "rr_01H...",
  "status": "parser_error",
  "summary_note": {
    "body_markdown": "AI review failed to parse structured output for this run. No inline comments were posted. Please rerun or inspect run rr_01H..."
  }
}
```

## 11. 版本兼容策略

- 所有内部 contract 以 `schema_version` 进行显式升级。
- Writer 端与 provider 端都只依赖内部 schema，而不是彼此直连。
- 若将来增加 SARIF / RDJSON adapter，应由 `ReviewResult -> AdapterFormat` 转换，不反向污染主 schema。

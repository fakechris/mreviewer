# GitLab REST API v4 Reference — MR Review System

> Target: GitLab Self-Managed 16.4+ (API v4)

## Authentication (All Endpoints)

```
Header: PRIVATE-TOKEN: <personal_access_token>
  — or —
Header: Authorization: Bearer <oauth_token>
```

Required PAT scopes: `api` (read-write) or `read_api` (read-only endpoints).

## Pagination (All List Endpoints)

GitLab uses **offset-based pagination** by default. Response headers:

| Header            | Description                         |
|-------------------|-------------------------------------|
| `X-Total`         | Total number of items               |
| `X-Total-Pages`   | Total number of pages               |
| `X-Per-Page`      | Items per page (default 20, max 100)|
| `X-Page`          | Current page number                 |
| `X-Next-Page`     | Next page number (empty if last)    |
| `X-Prev-Page`     | Previous page number                |

Query params: `page=1&per_page=100`

**Keyset pagination** is available on some endpoints for better performance on large datasets (use `pagination=keyset`).

---

## 1. Get Single Merge Request

```
GET /api/v4/projects/:id/merge_requests/:merge_request_iid
```

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | integer or string | Yes | Project ID or URL-encoded path (e.g., `my-group%2Fmy-project`) |
| `merge_request_iid` | integer | Yes | Internal MR ID within the project |
| `include_diverged_commits_count` | boolean | No | Include diverged commits count |
| `include_rebase_in_progress` | boolean | No | Include rebase status |
| `render_html` | boolean | No | Include `title_html` and `description_html` |

### Key Response Fields

```jsonc
{
  "id": 1,                        // Global MR ID
  "iid": 1,                       // Project-scoped MR IID (use this in URLs)
  "project_id": 3,
  "title": "Fix login CSS",
  "description": "...",
  "state": "opened",              // "opened" | "closed" | "merged" | "locked"
  "draft": false,
  "source_branch": "feature-x",
  "target_branch": "main",
  "source_project_id": 2,
  "target_project_id": 3,
  "sha": "abc123...",             // HEAD SHA of source branch
  "diff_refs": {                  // ⚠️ MAY BE NULL right after MR creation (async)
    "base_sha": "...",            // Merge-base between source & target
    "head_sha": "...",            // HEAD of source branch
    "start_sha": "..."           // HEAD of target branch
  },
  "merge_commit_sha": null,       // null until merged
  "squash_commit_sha": null,
  "changes_count": "5",           // String, not integer
  "has_conflicts": false,
  "detailed_merge_status": "mergeable",  // Preferred over deprecated merge_status
  "web_url": "https://gitlab.example.com/group/project/-/merge_requests/1",
  "author": { "id": 1, "username": "admin", ... },
  "reviewers": [{ "id": 2, "username": "reviewer1", ... }],
  "labels": ["bug", "backend"],
  "user_notes_count": 7
}
```

### Gotchas

- **`diff_refs` can be null** immediately after MR creation. It populates asynchronously. Poll or use webhooks to detect readiness. See [GitLab issue #386562](https://gitlab.com/gitlab-org/gitlab/-/issues/386562).
- **`changes_count` is a string**, not an integer.
- **`merge_status` is deprecated** since GitLab 15.6 — use `detailed_merge_status` instead.
- **`has_conflicts`** only returns `true` when `merge_status` is `cannot_be_merged`. Set `with_merge_status_recheck=true` to force an async recheck.
- `merge_status` may not be proactively updated unless `with_merge_status_recheck=true`.

---

## 2. Merge Request Diff Versions

```
GET /api/v4/projects/:id/merge_requests/:merge_request_iid/versions
```

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | Yes | Project ID |
| `merge_request_iid` | integer | Yes | MR IID |

### Response (Array, newest first)

```jsonc
[
  {
    "id": 110,                        // Version ID
    "head_commit_sha": "f9ce7e16...", // HEAD of source branch at this version
    "base_commit_sha": "5e6dffa2...", // Merge-base commit SHA
    "start_commit_sha": "5e6dffa2...",// HEAD of target branch at version time
    "created_at": "2021-03-30T09:18:27.351Z",
    "merge_request_id": 93958054,
    "state": "collected",             // "collected" | "overflow" | "without_files"
    "real_size": "2",                 // Number of changed files (string)
    "patch_id_sha": "d504412d..."     // git patch-id for this diff
  }
]
```

### SHA Field Meanings

| SHA Field | Meaning | Use For |
|-----------|---------|---------|
| `base_commit_sha` | Merge-base between source & target branches | `position[base_sha]` in discussions |
| `head_commit_sha` | HEAD of the source branch at this version | `position[head_sha]` in discussions |
| `start_commit_sha` | HEAD of the target branch when version was created | `position[start_sha]` in discussions |
| `patch_id_sha` | `git patch-id` hash — identifies the logical diff content | Detecting if a diff actually changed between versions |

### Get Single Version (with diffs)

```
GET /api/v4/projects/:id/merge_requests/:merge_request_iid/versions/:version_id
```

Additional params: `unidiff=true` (unified diff format, since GitLab 16.5)

Response includes `commits[]` and `diffs[]` arrays. Each diff entry:

```jsonc
{
  "old_path": "lib/foo.rb",
  "new_path": "lib/foo.rb",
  "a_mode": "100644",
  "b_mode": "100644",
  "diff": "@@ -1,3 +1,4 @@\n ...",   // Unified diff string
  "new_file": false,
  "renamed_file": false,
  "deleted_file": false,
  "generated_file": false,            // Since 16.9 — auto-generated files
  "collapsed": false,                 // Since 18.4 — diff excluded but fetchable
  "too_large": false                  // Since 18.4 — diff excluded permanently
}
```

### Gotchas

- **Latest version is first** in the array (index 0).
- **`state: "overflow"`** means the diff is too large and diffs may be truncated or missing.
- **Always use version SHAs** for creating discussions, not `diff_refs` from the MR object (they may be stale for non-latest versions).

---

## 3. Merge Request Diffs (List Changed Files)

### Preferred: List MR Diffs (replaces deprecated `/changes`)

```
GET /api/v4/projects/:id/merge_requests/:merge_request_iid/diffs
```

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | integer or string | Yes | Project ID or URL-encoded path |
| `merge_request_iid` | integer | Yes | MR IID |
| `page` | integer | No | Page number (default: 1) |
| `per_page` | integer | No | Items per page (default: 20) |
| `unidiff` | boolean | No | Unified diff format (since 16.5) |

### Response (Array of diff objects)

```jsonc
[
  {
    "old_path": "VERSION",
    "new_path": "VERSION",
    "a_mode": "100644",
    "b_mode": "100644",
    "diff": "@@ -1 +1 @@\n-1.9.7\n+1.9.8",
    "new_file": false,
    "renamed_file": false,
    "deleted_file": false,
    "generated_file": false,     // Auto-generated file marker (since 16.9)
    "collapsed": false,          // Diff excluded, but can be fetched via raw (since 18.4)
    "too_large": false           // Diff excluded, cannot be retrieved (since 18.4)
  }
]
```

### Deprecated: Retrieve MR Changes

```
GET /api/v4/projects/:id/merge_requests/:merge_request_iid/changes
```

Deprecated since 15.7. Scheduled for removal in API v5. Returns the full MR object + `changes[]` array + `overflow` boolean. Use `access_raw_diffs=true` to bypass database-backed diff size limits (slower, uses Gitaly directly).

### Key File Metadata Flags

| Flag | Meaning | Handling |
|------|---------|----------|
| `generated_file: true` | Auto-generated file (lockfiles, compiled code) | Skip in review or deprioritize |
| `collapsed: true` | Diff excluded due to size but CAN be fetched | Fetch via raw file API if needed |
| `too_large: true` | Diff excluded and CANNOT be retrieved via API | Skip, note limitation to user |
| `new_file: true` | Entirely new file | Review fully |
| `deleted_file: true` | File deleted | Check for unintended deletions |
| `renamed_file: true` | File renamed/moved | Check old_path → new_path |

### Gotchas

- **Paginated** — default 20 items per page. Always paginate through all pages.
- **Diff string may be empty** for collapsed/too_large files.
- **`overflow: true`** (on `/changes` endpoint) means results were truncated.

---

## 4. Create Discussion (Diff-Anchored Comment)

```
POST /api/v4/projects/:id/merge_requests/:merge_request_iid/discussions
```

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `body` | string | **Yes** | Comment content (Markdown supported) |
| `id` | integer or string | **Yes** | Project ID or URL-encoded path |
| `merge_request_iid` | integer | **Yes** | MR IID |
| `commit_id` | string | No | SHA to anchor the discussion to a specific commit |
| `position` | hash | No | **Required for diff comments**. Omit for overview/general comments |
| `position[position_type]` | string | Yes* | `"text"`, `"image"`, or `"file"` (file since 16.4) |
| `position[base_sha]` | string | Yes* | From `versions[0].base_commit_sha` |
| `position[head_sha]` | string | Yes* | From `versions[0].head_commit_sha` |
| `position[start_sha]` | string | Yes* | From `versions[0].start_commit_sha` |
| `position[old_path]` | string | Yes* | File path before change |
| `position[new_path]` | string | Yes* | File path after change |
| `position[old_line]` | integer | Conditional | Line number in old file (for removed/unchanged lines) |
| `position[new_line]` | integer | Conditional | Line number in new file (for added/unchanged lines) |
| `position[line_range]` | hash | No | For multi-line comments |

\* Required when `position` is supplied.

### Line Targeting Rules

| Comment Target | Set `old_line` | Set `new_line` |
|---------------|---------------|----------------|
| **Added line** (green in diff) | ❌ Do NOT set | ✅ Set to new line number |
| **Removed line** (red in diff) | ✅ Set to old line number | ❌ Do NOT set |
| **Unchanged line** (context) | ✅ Set to old line number | ✅ Set to new line number |

### Multi-line Comment (line_range)

```jsonc
{
  "position": {
    "position_type": "text",
    "base_sha": "...",
    "head_sha": "...",
    "start_sha": "...",
    "old_path": "file.js",
    "new_path": "file.js",
    "new_line": 18,                    // Anchor line (where comment appears)
    "line_range": {
      "start": {
        "line_code": "<sha1_of_filename>_<old>_<new>",  // SHA1(filename)_oldline_newline
        "type": "new",                 // "new" for added lines, "old" for removed/existing
        "new_line": 15
      },
      "end": {
        "line_code": "<sha1_of_filename>_<old>_<new>",
        "type": "new",
        "new_line": 18
      }
    }
  }
}
```

**Line code format:** `<SHA1_of_filename>_<old_line>_<new_line>`
- `<SHA1_of_filename>` = SHA1 hash of the file path string
- Example: `adc83b19e793491b1c6ea0fd8b46cd9f32e292fc_5_5`

### Response (201 Created)

```jsonc
{
  "id": "6a9c1750b37d513a...",    // Discussion ID (string, not integer)
  "individual_note": false,
  "notes": [
    {
      "id": 1128,                  // Note ID (integer)
      "type": "DiffNote",
      "body": "...",
      "author": { ... },
      "position": {
        "base_sha": "...",
        "start_sha": "...",
        "head_sha": "...",
        "old_path": "...",
        "new_path": "...",
        "position_type": "text",
        "old_line": null,
        "new_line": 18
      },
      "resolved": false,
      "resolvable": true
    }
  ]
}
```

### Creating a General (Non-Diff) Comment

Simply omit the `position` parameter:
```
POST /api/v4/projects/:id/merge_requests/:merge_request_iid/discussions
body=Your general comment here
```

### Gotchas

- **CRITICAL: Get SHAs from `/versions` endpoint**, not from `diff_refs` on the MR object. Using wrong SHAs causes `400 Bad Request` or comments appearing on wrong lines. See [GitLab issue #296829](https://gitlab.com/gitlab-org/gitlab/-/issues/296829).
- **Both `old_path` and `new_path` are always required** for text position types, even if the file wasn't renamed.
- **Discussion ID is a string** (hex hash), not an integer.
- **Note ID is an integer** — needed for modifying individual notes within a discussion.
- For **suggestions**, use special Markdown syntax in body: ` ```suggestion:-0+0\n<replacement>\n``` `

---

## 5. Resolve/Unresolve a Discussion

```
PUT /api/v4/projects/:id/merge_requests/:merge_request_iid/discussions/:discussion_id
```

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | integer or string | Yes | Project ID |
| `merge_request_iid` | integer | Yes | MR IID |
| `discussion_id` | string | Yes | Discussion ID (hex string) |
| `resolved` | boolean | **Yes** | `true` to resolve, `false` to unresolve |

### Prerequisites
- Developer, Maintainer, Owner role, **or** author of the change being reviewed.

### Response (200 OK)
Returns the updated discussion object.

---

## 6. List Discussions

```
GET /api/v4/projects/:id/merge_requests/:merge_request_iid/discussions
```

### Parameters

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | integer or string | Yes | Project ID |
| `merge_request_iid` | integer | Yes | MR IID |
| `page` | integer | No | Page number |
| `per_page` | integer | No | Items per page (default: 20) |

### Response (200 OK, Array)

```jsonc
[
  {
    "id": "6a9c1750b37d...",
    "individual_note": false,       // false = threaded discussion, true = standalone comment
    "notes": [
      {
        "id": 1126,
        "type": "DiffNote",         // "DiffNote" | "DiscussionNote" | null (regular note)
        "body": "This needs work",
        "author": { "id": 1, "username": "root" },
        "created_at": "2018-03-03T21:54:39.668Z",
        "system": false,            // true = system-generated note (merge, label change, etc.)
        "resolvable": true,
        "resolved": false,
        "resolved_by": null,
        "position": {               // Only present for DiffNote type
          "base_sha": "...",
          "start_sha": "...",
          "head_sha": "...",
          "old_path": "package.json",
          "new_path": "package.json",
          "position_type": "text",
          "old_line": 27,
          "new_line": 27,
          "line_range": { ... }     // Present for multi-line comments
        }
      }
    ]
  }
]
```

### Filtering Tips

- **Diff comments**: `notes[0].type === "DiffNote"` and `notes[0].position` is present
- **System notes**: `notes[0].system === true` (merge events, label changes, etc.)
- **Resolved threads**: check `notes[0].resolved === true`
- **Standalone comments**: `individual_note === true`

### Gotchas

- **Paginated**: Default 20 items per page. Must paginate to get all discussions.
- **System notes are included** — filter by `system: false` to get only human comments.
- **`individual_note: true`** means it's a standalone note, not a threaded discussion.

---

## 7. Get Raw File Content at Specific SHA

### Option A: Repository Files API (Base64 encoded)

```
GET /api/v4/projects/:id/repository/files/:file_path?ref=<sha>
```

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | integer or string | Yes | Project ID |
| `file_path` | string | Yes | **URL-encoded** file path (e.g., `lib%2Fclass%2Erb`) |
| `ref` | string | Yes | Branch name, tag, or **commit SHA** |

Response:
```jsonc
{
  "file_name": "key.rb",
  "file_path": "app/models/key.rb",
  "size": 1476,
  "encoding": "base64",
  "content": "IyA9PSBTY2hlbWEgSW5mb3...",   // Base64 encoded
  "ref": "abc123def",
  "blob_id": "79f7bbd2...",
  "commit_id": "d5a3ff13...",
  "last_commit_id": "570e7b2a..."
}
```

### Option B: Raw File Content (binary-safe)

```
GET /api/v4/projects/:id/repository/files/:file_path/raw?ref=<sha>
```

Returns raw file content with appropriate `Content-Type`. Best for large or binary files.

Additional params:
- `lfs=true` — return actual LFS file content instead of pointer

### Gotchas

- **File path MUST be URL-encoded**: `lib/class.rb` → `lib%2Fclass%2Erb`
- **Rate limit**: For blobs > 10 MB, limited to **5 requests per minute**.
- **Use `ref=<sha>`** with a commit SHA to get file at a specific point in time (e.g., `base_commit_sha` or `head_commit_sha` from versions).
- `HEAD` method available for metadata only (no content transfer).

---

## 8. Webhook Payloads — Merge Request Events

### Project/Group Webhook

**Request Header:** `X-Gitlab-Event: Merge Request Hook`

**Trigger conditions:**
- MR created, updated, approved, unapproved, merged, closed
- Individual user adds/removes approval
- Commit pushed to source branch
- All threads resolved
- Reviewer re-requested

### `object_attributes.action` Values

| Action | Meaning |
|--------|---------|
| `open` | MR created |
| `close` | MR closed |
| `reopen` | Closed MR reopened |
| `update` | MR updated (check `changes` for specifics) |
| `approval` | User adds their approval |
| `approved` | MR fully approved by all required approvers |
| `unapproval` | User removes approval |
| `unapproved` | MR loses approved status |
| `merge` | MR merged |

### Payload Structure

```jsonc
{
  "object_kind": "merge_request",
  "event_type": "merge_request",
  "user": {                            // User who triggered the event
    "id": 1,
    "name": "Administrator",
    "username": "root",
    "avatar_url": "...",
    "email": "admin@example.com"
  },
  "project": {
    "id": 1,
    "name": "My Project",
    "path_with_namespace": "group/project",
    "web_url": "https://gitlab.example.com/group/project",
    "git_ssh_url": "git@gitlab.example.com:group/project.git",
    "git_http_url": "https://gitlab.example.com/group/project.git",
    "default_branch": "main",
    "visibility_level": 20
  },
  "object_attributes": {
    "id": 99,
    "iid": 1,
    "title": "Fix something",
    "description": "...",
    "state": "opened",
    "action": "open",                  // ← Key field for determining event type
    "source_branch": "feature-x",
    "target_branch": "main",
    "source_project_id": 14,
    "target_project_id": 14,
    "author_id": 51,
    "assignee_ids": [6],
    "reviewer_ids": [7],
    "draft": false,
    "merge_status": "unchecked",
    "detailed_merge_status": "not_open",
    "url": "https://gitlab.example.com/group/project/-/merge_requests/1",
    "last_commit": {
      "id": "da1560886d4f...",
      "message": "fixed readme",
      "timestamp": "2012-01-03T23:36:29+02:00",
      "url": "...",
      "author": { "name": "...", "email": "..." }
    },
    "oldrev": "e59094b8...",           // ⚠️ Only present on "update" with code changes
    "source": { /* source project details */ },
    "target": { /* target project details */ },
    "head_pipeline_id": 123,
    "prepared_at": "2021-03-30T09:18:27.351Z"
  },
  "changes": {                         // ⚠️ Only contains CHANGED fields
    "updated_by_id": { "previous": null, "current": 1 },
    "updated_at": { "previous": "...", "current": "..." },
    "labels": { "previous": [...], "current": [...] }
    // May be EMPTY even when event fires
  },
  "assignees": [{ "id": 6, "username": "user1", ... }],
  "reviewers": [{ "id": 7, "username": "reviewer1", ... }],
  "labels": [{ "id": 206, "title": "API", "color": "#ffffff" }]
}
```

### System Hook (Admin-level)

**Request Header:** `X-Gitlab-Event: System Hook` (always this, not `Merge Request Hook`)

The system hook MR payload is **structurally identical** to the project webhook payload, with these differences:

| Aspect | Project Webhook | System Hook |
|--------|----------------|-------------|
| Header | `X-Gitlab-Event: Merge Request Hook` | `X-Gitlab-Event: System Hook` |
| Scope | Per-project or per-group | Instance-wide (all projects) |
| Setup | Project Settings → Webhooks | Admin Area → System Hooks |
| MR payload fields | Full payload with `changes`, `assignees`, `reviewers` | Same structure as project webhook |
| Available events | All MR lifecycle events | Only `merge_request` (create/update/merge/close) |

### Key Webhook Fields for MR Review System

| Use Case | Field Path |
|----------|------------|
| Detect new MR | `object_attributes.action === "open"` |
| Detect new commits pushed | `object_attributes.action === "update"` + `object_attributes.oldrev` present |
| Get MR IID | `object_attributes.iid` |
| Get project ID | `project.id` |
| Get source branch | `object_attributes.source_branch` |
| Get target branch | `object_attributes.target_branch` |
| Check if draft | `object_attributes.draft` |
| Get latest commit SHA | `object_attributes.last_commit.id` |
| Detect MR merged | `object_attributes.action === "merge"` |

### Gotchas

- **`changes` field can be empty** even when the webhook fires. Always check its contents.
- **`oldrev` only present** on `update` events with actual code changes (push, suggestion applied).
- **System-initiated events** (auto-unapproval after push) include `object_attributes.system: true` and `object_attributes.system_action`.
- **Webhook may fire before `diff_refs` is populated** for new MRs — poll the API if `diff_refs` is null.
- **Secret token validation**: Use `X-Gitlab-Token` header to verify webhook authenticity.
- **429 rate limiting** applies to webhook re-delivery retries.

---

## Rate Limits & Performance Notes

| Endpoint | Rate Limit | Notes |
|----------|-----------|-------|
| Repository files (>10MB blobs) | 5 req/min | Per-project |
| MR list with `search` param | Varies | Can return 429 |
| General API | Configurable per-instance | Default: 2000 req/min for authenticated users |
| Discussions list | Standard pagination | Default 20/page, paginate to get all |
| MR diffs list | Standard pagination | Default 20/page |

### Best Practices

1. **Always paginate** — never assume a single page has all results.
2. **Use `/versions` SHAs for discussion positions** — not `diff_refs` from MR object.
3. **Check for `diff_refs: null`** after MR creation before processing diffs.
4. **Handle `collapsed` and `too_large` diffs** — fetch raw file content separately if needed.
5. **Filter system notes** when listing discussions to get only human comments.
6. **URL-encode project paths** when using path-based IDs (e.g., `my-group%2Fmy-project`).
7. **Cache diff versions** — they are immutable once created.
8. **Use `unidiff=true`** (since 16.5) for cleaner diff parsing.

---

## Quick Reference: Endpoint Summary

| Purpose | Method | Endpoint |
|---------|--------|----------|
| Get MR details | GET | `/projects/:id/merge_requests/:iid` |
| List MR diff versions | GET | `/projects/:id/merge_requests/:iid/versions` |
| Get single diff version | GET | `/projects/:id/merge_requests/:iid/versions/:vid` |
| List MR diffs (files) | GET | `/projects/:id/merge_requests/:iid/diffs` |
| List discussions | GET | `/projects/:id/merge_requests/:iid/discussions` |
| Get single discussion | GET | `/projects/:id/merge_requests/:iid/discussions/:did` |
| Create discussion | POST | `/projects/:id/merge_requests/:iid/discussions` |
| Resolve discussion | PUT | `/projects/:id/merge_requests/:iid/discussions/:did` |
| Add note to thread | POST | `/projects/:id/merge_requests/:iid/discussions/:did/notes` |
| Get file (base64) | GET | `/projects/:id/repository/files/:path?ref=:sha` |
| Get file (raw) | GET | `/projects/:id/repository/files/:path/raw?ref=:sha` |
| **Deprecated** MR changes | GET | `/projects/:id/merge_requests/:iid/changes` |

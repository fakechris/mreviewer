# Portable Review Council Architecture

`mreviewer` 的核心执行路径是：

1. 平台目标解析
2. 平台快照抓取
3. `ReviewInput` 组装
4. specialist reviewer packs 执行
5. judge 归并与裁决
6. 生成 canonical `ReviewBundle`
7. 可选发布到 GitHub / GitLab
8. 可选 comparison / benchmark 输出

## Canonical Types

### `ReviewTarget`

- `platform`
- `repository`
- `number`
- `url`

### `PlatformSnapshot`

- 仓库元信息
- PR / MR 元信息
- base / start / head sha
- 平台原始 diff / comment / review 数据

### `ReviewInput`

- `target`
- `snapshot`
- `metadata`
- `policy`
- `system_prompt`
- `request_payload`
- `context_text`
- `sections`

### `ReviewerArtifact`

- `reviewer_id`
- `reviewer_type`
- `summary`
- `verdict`
- `findings`

### `ReviewBundle`

- `target`
- `artifacts`
- `advisor_artifact`
- `judge_verdict`
- `judge_summary`
- `publish_candidates`
- `comparisons`

## ReviewInput Sections

`ReviewInput` 不再只是一块大 prompt，而是拆成 section：

- `target`
- `policy`
- `system_prompt`
- `request_payload`
- `platform_metadata`
- `assembled_context`

每个 section 都带 `cache_key`，后续可以做更细的缓存和 trace。

## Reviewer Packs

默认 packs：

- `security`
- `architecture`
- `database`

每个 pack 都是 capability-shaped contract，不只是 prompt 名：

- `scope`
- `rubric`
- `evidence_requirements`
- `output_schema`
- `standards`
- `hard_exclusions`
- `confidence_gate`

## Judge

judge 不是纯 summary 层，而是 decision engine：

- finding dedupe
- severity reconciliation
- reviewer attribution
- final verdict
- canonical publish candidates

## Publish Model

`ReviewBundle` 只产出平台中立的 publish candidates：

- `summary`
- `finding`

平台 writer 再把它们翻译成：

- GitLab note / discussion
- GitHub issue comment / review comment

## Runtime Surfaces

当前运行面：

- `cmd/mreviewer`
- `cmd/manual-trigger`
- `ingress`
- `worker`

它们共享同一套 canonical contracts，并逐步复用同一条 engine / bundle / writeback 主路径。

## Status Model

运行过程使用统一阶段状态：

- `loading_target`
- `running_packs`
- `running_advisor`
- `publishing`
- `comparing_external`
- `comparing_targets`
- `completed`

这些阶段会映射到 GitHub / GitLab 的 in-review status。

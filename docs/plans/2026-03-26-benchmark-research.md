# Benchmark Research & Evaluation Roadmap

## Context

This document records research into existing code review benchmarks and proposes directions for mreviewer's own evaluation infrastructure.

## Key Findings from Martian code-review-benchmark

### Dataset Structure

Martian's offline benchmark uses 50 PRs from 5 major OSS projects:

| Project | Language | Domain |
|---------|----------|--------|
| Sentry | Python | Error tracking |
| Grafana | Go | Observability |
| Cal.com | TypeScript | Scheduling |
| Discourse | Ruby | Forum |
| Keycloak | Java | Auth |

Each PR has multiple human-verified "golden comments" with severity labels (Low/Medium/High/Critical). Example:

```json
{
  "pr_title": "Fix race condition in worker pool",
  "url": "https://github.com/getsentry/sentry/pull/93824",
  "comments": [
    {
      "comment": "This lock acquisition can deadlock if worker is interrupted between lock A and lock B",
      "severity": "High"
    }
  ]
}
```

Key insight: multiple golden comments per PR (2-5 avg), not single bug per PR.

### Evaluation Architecture

**Offline**: Controlled comparison on fixed dataset. Fork PRs into eval org, trigger tool, run LLM judge.

**Online**: Behavioral ground truth — did the developer fix what the bot flagged? Uses post-review commits as signal.

**LLM-as-Judge**: Match tool comments against golden comments via semantic similarity, not string match.

**Critical innovation — Deduplication**: Tools often post same issue in both summary and inline comments. Without dedup, this creates false positive penalties. LLM groups duplicate candidates before scoring.

### Methodology Strengths

1. **Semantic matching**: Judge accepts "different wording, same underlying problem" as a match.
2. **Dual benchmarks check each other**: Offline is controlled but risks data leakage. Online is behavioral but lacks counterfactuals. Together they validate.
3. **Judge model tracking**: Results stored per judge model (Claude Opus 4.5, Sonnet 4.5, GPT-5.2).
4. **Developer behavior as ground truth**: sidesteps human annotation bottleneck.

### Known Limitations They Acknowledge

- **Gold set caps at human performance**: If tool finds a real bug humans missed, it's scored as false positive. Cannot measure superhuman recall.
- **Training data contamination**: Static dataset PRs may have been seen during model training.
- **Bug definitions are ill-defined**: Different users want different things (style vs. critical-only). Precision/recall are undefined without conditioning on preferences.
- **Format heterogeneity**: Tools output line-comments, summaries, or both. Hard to compare fairly.

---

## Other Relevant Benchmarks

| Benchmark | Method | Scale | Open Source | Key Limitation |
|-----------|--------|-------|-------------|----------------|
| SWR-Bench (2025) | Backtracking | 1,000 PRs | ❌ | Not reproducible |
| ContextCRBench (2025) | Fine-grained context | - | ❌ | Not reproducible |
| Greptile (Jul 2025) | Backtracking | 50 PRs | ✅ | Single bug/PR, no precision |
| Augment (Dec 2025) | Backtracking + precision | 50 PRs | ✅ | Still capped by human gold set |
| Qodo (2025) | **Injection-based** | 100 PRs, 580 issues | ✅ | Synthetic bugs may not match real distribution |

---

## Evaluation Approaches for mreviewer

### Approach A: Offline Fixed Dataset (Martian-style)

**Build a golden comment dataset** from real MRs:

1. Collect MRs where mreviewer left comments
2. Follow up: did the author fix what mreviewer flagged?
3. Build golden set of (diff + real issue + severity)
4. On new model/version: replay same MRs, compare findings against golden set

**Pros**: Reproducible, comparable across versions, isolated from external factors.
**Cons**: Static dataset eventually leaks into training data. Requires ongoing curation.

**Implementation path**:
- Use existing mreviewer audit logs to bootstrap golden comments (real reviews, real outcomes)
- Build `internal/benchmark/` package: corpus, judge, scoring
- Reuse existing dedup logic from parser

### Approach B: Online Behavioral (Martian-style)

**Use developer actions as ground truth**:

```
mreviewer reviews MR with findings F
  ↓
author responds: fixes issues in F?
  ↓
Yes → true positive (finding was actionable)
No → false positive (finding was noise)
```

**Pros**: No annotation cost. Ground truth is real developer behavior. Continuous.
**Cons**: No counterfactual comparison. Can't isolate model quality from harness quality.

**Implementation path**:
- Add `review_findings.outcome` tracking: `actioned`, `ignored`, `rejected`
- Add outcome collection to the GitLab webhook handler
- Build dashboard: precision over time per model/route

### Approach C: LLM-as-Judge (Reference Comparison)

Run two models on same MRs, use a third "judge" model to compare quality:

```
MR diff → Model A findings
MR diff → Model B findings
         ↓
   Judge LLM: which is better?
   (semantic comparison, not string match)
```

**Pros**: Relative comparison without ground truth. Can use weaker judge.
**Cons**: Judge itself is variable. Doesn't tell you if either is actually good.

---

## Proposed Direction for mreviewer

### Phase 1: Behavioral Outcome Tracking (Online, Low Effort)

Add outcome tracking to existing review pipeline:

```sql
ALTER TABLE review_findings ADD COLUMN outcome ENUM('unknown', 'actioned', 'ignored', 'rejected');
```

**Flow**:
1. mreviewer posts finding to GitLab discussion
2. Author pushes commit that touches the same file/line → mark finding `actioned`
3. Author explicitly resolves discussion → mark `actioned`
4. Author responds "not a problem" → mark `rejected`
5. MR closes without addressing → mark `ignored` after N days

**Output**: Per-route precision = `actioned / (actioned + ignored + rejected)`

This is the lowest-lift path to meaningful metrics. No new infrastructure, no golden dataset, just tracking what already happens.

### Phase 2: Fixed Corpus Evaluation (Offline, Medium Effort)

Build a small benchmark corpus from mreviewer's own production reviews:

1. **Seed corpus** from audit logs: top 20 highest-signal MRs (longest diff, most findings)
2. **Human annotation**: for each finding in corpus, label: valid / noise / missed
3. **Automated scoring**: replay corpus against new model versions, compute precision/recall
4. **Regression gate**: PR cannot merge if recall drops >5% vs baseline

**Corpus structure**:

```json
{
  "id": "corpus-001",
  "source_mr": "group/project!123",
  "diff": "...",
  "golden_findings": [
    {
      "path": "src/auth.go",
      "line": 42,
      "issue": "nil pointer dereference if token is expired",
      "severity": "high",
      "validated": true
    }
  ],
  "mreviewer_findings": [...],
  "false_negatives": [...]
}
```

**Judge prompt** (semantic matching):

```
Golden: {issue description from corpus}
Candidate: {finding from mreviewer}
Do these describe the same underlying code issue?
Accept paraphrases and different severity framings.
Output: {"match": true/false, "reasoning": "..."}
```

### Phase 3: Multi-Model Comparative Eval

Add support for running the same corpus through multiple routes simultaneously:

```
corpus MR
  ├─→ ark-anthropic (kimi-k2.5) → findings
  ├─→ ark-openai (deepseek-v3.2) → findings
  ├─→ minimax → findings
  └─→ [baseline: claude-sonnet] → findings
         ↓
      Judge LLM: rank findings by usefulness
```

**Metrics per run**:
- Precision (per-route vs golden)
- Recall (per-route vs golden)
- Inter-route agreement: do both models flag the same issues?
- False negative overlap: did both miss the same real bugs?

---

## Technical Considerations

### Dedup Before Scoring

Tools often flag the same issue in multiple places. Score at the **issue level**, not the finding level.

Implementation: LLM dedup group candidates before scoring, same as Martian's Step 2.5.

### Schema vs Freeform

mreviewer uses structured JSON output (tool call with schema). This is actually an advantage:
- Finding format is consistent across models
- Easier to compute metrics without parsing freeform text

But it also constrains what models can express. Consider:
- Models that don't fully follow schema → repair/retry mechanism
- Models with very high refusal rates on strict schema

### Speed vs Quality Tradeoff

Kimi k2.5: 30s-4min response time. Not suitable for inline real-time review. But may produce higher quality findings per review.

For benchmark: measure quality per unit time, not quality in isolation.

### Judge Model Selection

For Phase 1-2, use a fixed judge (e.g., Claude Sonnet 4.5) to ensure comparability across runs. Store results keyed by judge model.

---

## Open Questions

1. **What is mreviewer's "gold set"?** Human annotations from the team, or developer behavioral signals?
2. **Single-model vs multi-model eval**: Is the goal to pick the best model, or to measure whether mreviewer is improving over time?
3. **Regression threshold**: What precision/recall delta should block a PR?
4. **Corpus maintenance**: Who updates the golden set as the codebase evolves?
5. **Contamination**: If we use mreviewer's own historical reviews as corpus, newer models may have memorized these. Is this a real risk for our use case?

---

## Appendix: Related Reading

- [Martian code-review-benchmark](https://github.com/withmartian/code-review-benchmark)
- [Martian ares (RL framework)](https://github.com/withmartian/ares)
- [SWE-bench](https://github/princeton-nlp/SWE-bench) — code修复能力评测
- [LiveCodeBench](https://livecodebench.github.io) — 持续刷题能力评测
- [Qodo benchmark methodology](https://www.qodo.ai/blog/code-review-benchmark)

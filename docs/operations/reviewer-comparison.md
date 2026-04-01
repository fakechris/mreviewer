# Reviewer Comparison

`mreviewer` 的 comparison 层比较的是 canonical artifacts，不是直接比较平台原始评论文本。

## 输入来源

comparison 可以吃三类输入：

1. 当前 council 产出的 reviewer artifacts
2. advisor artifact
3. GitHub / GitLab 上抓到的外部 reviewer comments

## 输出内容

当前输出包括：

- shared findings
- reviewer-unique findings
- agreement rate
- aggregate comparison
- decision benchmark

## Reviewer Identity

内部 reviewer 会归一成稳定 id：

- `council:security`
- `council:architecture`
- `council:database`
- `advisor:<route>`

外部 reviewer 会带平台前缀：

- `github:<reviewer>`
- `gitlab:<reviewer>`

## CLI 用法

对单个 PR / MR 做 compare：

```bash
go run ./cmd/mreviewer \
  --target https://github.com/acme/service/pull/24 \
  --compare-reviewer coderabbit \
  --compare-reviewer gemini \
  --output json \
  --publish artifact-only
```

做 multi-target aggregate comparison：

```bash
go run ./cmd/mreviewer \
  --target https://github.com/acme/service/pull/24 \
  --targets https://gitlab.example.com/group/service/-/merge_requests/17,https://github.com/acme/api/pull/42 \
  --output json \
  --publish artifact-only
```

## 结果解释

- `agreement_rate` 高，表示 reviewer 之间发现的问题更重合
- `unique_findings` 高，不代表一定更强，可能是补充，也可能是噪音
- `decision_benchmark` 更适合做第一阶段的判断基准

当前不把 “当前 PR 是否马上修复” 当成唯一真相源，因为真实开发流程里这个信号往往不稳定。

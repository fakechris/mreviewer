# Evaluation：GitLab MR 自动代码审查系统评估方案

版本：v0.1
日期：2026-03-16

## 1. 评估目标

系统评估不是只看“发了多少评论”，而是要评估它是否真的提高了 review 质量且没有制造噪声。评估需覆盖：

- 准确性
- 重复评论控制
- 时延与稳定性
- 开发者接受度
- 成本

## 2. 核心指标

### 2.1 准确性指标

#### True Positive Rate（TPR）

定义：被人工判定为真实问题的 finding 占比。

公式：

`TPR = true_findings / reviewed_findings`

#### False Positive Rate（FPR）

定义：被人工判定为误报的 finding 占比。

#### Miss Rate / Recall Proxy

漏报难以直接在线统计，建议用离线回放集估计：

`RecallProxy = detected_known_bugs / total_known_bugs_in_dataset`

### 2.2 生命周期质量指标

#### Duplicate Comment Rate

同一 semantic issue 在 MR 演进过程中被重复发布为新 discussion 的比例。

#### Auto-Resolved Rate

被系统自动判定 fixed/stale 并成功 resolve 的 bot finding 占比。

#### Supersede Accuracy

行号漂移后被正确识别为“同一问题迁移”的比例。

### 2.3 体验指标

#### Developer Acceptance Rate

可用以下代理指标衡量：

- 被保留未 dismiss 的 finding 占比
- 被回复/讨论的 finding 占比
- 被修复后 auto-resolve 的 finding 占比

#### Noise Rate

- 被 maintainer 标记 ignore / not useful 的 finding 占比
- 某项目 opt-out 或阈值上调事件数

### 2.4 系统指标

- P50 / P90 review latency
- provider timeout rate
- parser failure rate
- comment writer failure rate
- webhook dedupe hit rate
- average tokens / run
- cost / run

## 3. 评估方法

### 3.1 在线人工抽样评估

每周随机抽样一定数量的 findings，按以下标签标注：

- TP：真实 bug
- FP：误报
- Unclear：需更多上下文
- Duplicate：重复评论
- Wrong severity：问题存在但严重性不合适

建议至少由 2 名 reviewer 交叉标注高严重度 finding。

### 3.2 离线回放集

建立一个固定的 replay dataset：

- 历史 MR diff
- 对应 base/start/head
- 真实 bug 标签或后续修复信息
- 历史 bot comments（若有）

离线回放可用于：

- 对比不同 provider/model
- 对比不同 prompt / rules / threshold
- 回归测试 dedupe / anchor 逻辑

### 3.3 A/B 或 Shadow 评估

在试点期可运行：

- `shadow mode`：只生成结果不发评论
- `comment mode`：真实发评论

对比两者：

- 命中率
- 噪声率
- reviewer 反馈

## 4. 基线建议

与以下基线对比：

1. 无 AI review，仅人审
2. 仅 lint / SAST / Code Quality
3. 旧版 prompt / 模型 / 阈值

## 5. 验收阈值建议

以下是可操作的 MVP 阈值建议，可按团队成熟度调整：

- TPR >= 0.65
- Duplicate Comment Rate <= 0.05
- Parser Failure Rate <= 0.01
- Comment Writer Success Rate >= 0.99
- 小中型 MR P90 review latency <= 10 min

对于 high-severity findings，建议额外约束：

- high-severity TPR >= 0.80

## 6. 数据采集设计

### 6.1 事件表采样字段

- run_id
- project_id
- mr_iid
- provider/model
- token_in / token_out
- latency_ms
- findings_count
- posted_count
- deduped_count
- resolved_count
- parser_error

### 6.2 人工标注表字段

- finding_id
- label（TP/FP/Unclear/Duplicate/WrongSeverity）
- reviewer
- note
- labeled_at

## 7. 典型误差分析框架

每周或每双周输出：

### 7.1 误报 Top N 原因

- 缺上下文
- 误判为 bug 的 intentional behavior
- 历史代码问题被误归因到本 MR
- 规则文件冲突或缺失
- anchor 定位错误导致评论看起来不相关

### 7.2 漏报 Top N 原因

- 上下文预算不足
- 未拉到相关文件
- prompt 未覆盖该类问题
- provider 对特定语言/框架弱
- 阈值过高

### 7.3 重复评论原因

- fingerprint 设计不稳定
- line 漂移重定位失败
- rerun 幂等键不正确
- bot 历史 discussion 拉取不全

## 8. 上线策略建议

### 8.1 阶段化发布

- Week 1-2：shadow mode
- Week 3-4：只发 high confidence findings
- Week 5+：扩展到 medium confidence，并开放 rerun

### 8.2 项目分层

优先试点：

- 代码规模中等
- 语言栈相对统一
- maintainer 反馈积极
- 非最高敏项目

暂缓：

- 超大型 monorepo
- 高度生成代码项目
- 对代码外发极其敏感但尚未完成 provider 评审的项目

## 9. 长期评估扩展

后续可增加：

- 评论到修复的平均 lead time
- MR merge 前 bug removal rate
- 人工 reviewer 花费时间变化
- provider ROI（成本 / 有效 finding）
- 规则文件命中率与效果

## 10. 结论

只有同时看“准确性、重复率、时延、接受度、成本”，这个系统才算真的可用。单纯追求 finding 数量会直接把产品推向噪声 bot 的失败路径。

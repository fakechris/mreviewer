# Webhook Runtime Acceptance Matrix

This matrix defines the minimum operator-facing acceptance coverage for the GitLab and GitHub webhook runtime after control-plane productization.

## Scope

- Webhook intake and verification
- Queueing and worker execution
- Retry, rerun, requeue, cancel operator actions
- Stale worker visibility
- Identity mapping observation and manual repair
- Dashboard/API observability for GitLab and GitHub

## Acceptance Scenarios

| Scenario | Setup | Expected Runtime Outcome | Expected Control-Plane Signal |
| --- | --- | --- | --- |
| Valid GitLab webhook | Send signed GitLab merge request webhook for enabled project | Hook accepted, run created, worker processes review | Queue increments then drains, run visible with `platform=gitlab`, run detail shows hook action |
| Valid GitHub webhook | Send signed GitHub pull request webhook for enabled repo | Hook accepted, run created, worker processes review | Queue increments then drains, run visible with `platform=github`, run detail shows hook action |
| Invalid signature | Send webhook with bad secret | Hook rejected, no run created | Failure stats increment `webhook_rejected_last_24h`; trend bucket shows rejected webhook |
| Duplicate delivery | Replay same signed webhook delivery key | Request deduplicated, no duplicate run | Failure stats increment `webhook_deduplicated_last_24h`; trend bucket shows deduplicated webhook |
| New head supersedes old run | Trigger second webhook with newer head SHA while first run is pending/running | Older run superseded, newer run becomes authoritative | Queue and runs list show new run; superseded run remains visible in run detail/history |
| Retry failed run | Run fails with retryable error | Operator invokes `retry` | Run detail updates, audit log written, queue reflects retry eligibility |
| Rerun completed/failed run | Existing finished run selected in dashboard | Operator invokes `rerun` | New pending run cloned from source run, audit log written |
| Requeue cancelled run | Cancelled or failed-no-retry run exists | Operator invokes `requeue` | Same run re-enters queue as pending, claimed worker cleared |
| Cancel stuck run | Running run selected in dashboard | Operator invokes `cancel` | Run enters `cancelled`, error code indicates operator cancellation |
| Stale worker heartbeat | Worker heartbeat stops for more than active window | Worker remains visible but marked stale | Concurrency card shows stale worker count; warning banner appears in dashboard |
| Identity observation | Review input includes commit author and committer observations | Identity rows upserted from runtime | Identity mappings table shows new rows with observed role and head SHA |
| Manual identity repair | Operator resolves mapping in dashboard | Mapping becomes `manual`, audit log written | Identity table reflects resolved user; ownership table groups by resolved user |
| Suggestion-assisted repair | Unresolved mapping exists with similar observed identities | Operator opens suggestions | Local suggestions API returns ranked candidates with reasons and scores |

## Dashboard/API Readiness

The control plane is considered product-ready only if all of the following are true:

1. `/admin/api/queue`, `/admin/api/concurrency`, `/admin/api/failures`, `/admin/api/trends`, `/admin/api/runs`, `/admin/api/runs/{id}`, `/admin/api/identities`, `/admin/api/identities/{id}/suggestions`, `/admin/api/ownership` all return valid JSON under load.
2. `/admin/` auto-refreshes without full-page reload and surfaces stale worker warnings.
3. Runs table, run detail, ownership table, and identity mappings table stay coherent after operator actions.
4. GitLab and GitHub webhook failures are visible through both aggregate cards and time-bucket trend views.

## Non-Goals

- Fine-grained RBAC beyond the current admin bearer-token gate
- Historical BI dashboards beyond the last-24-hour operational window
- Remote platform user search for identity suggestions

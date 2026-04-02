# Release Distribution

`mreviewer` ships as a standalone Go CLI. Release distribution is built around GitHub Releases plus a Homebrew tap formula that the release workflow generates and commits after the first tagged release.

## What a release publishes

The `Release CLI` workflow publishes:

- `mreviewer_<version>_darwin_amd64.tar.gz`
- `mreviewer_<version>_darwin_arm64.tar.gz`
- `mreviewer_<version>_linux_amd64.tar.gz`
- `mreviewer_<version>_linux_arm64.tar.gz`
- per-archive `.sha256` files
- a consolidated `checksums.txt`
- a generated `mreviewer.rb` Homebrew formula asset

After the release assets are published, the workflow also opens a formula-sync pull request that updates `Formula/mreviewer.rb` on `main`.

For a fully automatic formula-sync PR, configure `RELEASE_PR_TOKEN` as either a GitHub App installation token or a PAT with permission to push the `release/formula-*` branch and open pull requests. The default `github.token` is sufficient for publishing release assets, but GitHub does not reliably attach normal PR checks when the formula branch and PR are both created by `GITHUB_TOKEN`.

## How to publish

Two supported paths:

1. Push a release tag:

```bash
git tag v1.2.3
git push origin v1.2.3
```

2. Or run the `Release CLI` workflow manually with `version=v1.2.3`.

The version must match `vMAJOR.MINOR.PATCH` or `vMAJOR.MINOR.PATCH-PRERELEASE`.

## User install paths

Installer script:

```bash
curl -fsSL https://raw.githubusercontent.com/fakechris/mreviewer/main/scripts/install.sh | bash
```

Homebrew tap, after the first tagged release has completed:

```bash
brew tap fakechris/mreviewer https://github.com/fakechris/mreviewer
brew install mreviewer
```

## Local release smoke checks

Before cutting a release, run:

```bash
bash scripts/install_test.sh
bash scripts/release_test.sh
bash scripts/verify-onboarding.sh
bash scripts/verify-onboarding_test.sh
go test ./... -count=1
```

## Release playbook

`mreviewer` does not create a formal release for every PR.

Normal flow:

1. Every PR runs CI and review checks.
2. PRs merge to `main` once they are stable.
3. A formal release is cut only when there is a batch of user-visible improvements or an important distribution/runtime fix worth shipping.

In practice, the release action is explicit:

1. The human decides a version should ship, for example `v0.1.4`.
2. The release operator verifies `main` is green and runs the local smoke checks above.
3. The release operator cuts the tag:

```bash
git tag v1.2.3
git push origin v1.2.3
```

4. `Release CLI` publishes the GitHub Release assets.
5. The release workflow opens the Homebrew formula sync PR.
6. That PR is reviewed and merged.
7. The installer path and Homebrew install path are verified against the real published release.

This keeps the release surface understandable:

- PRs validate code.
- Tags publish versions.
- Homebrew follows the tagged release, not individual feature branches.

## Formula PR token

Set `RELEASE_PR_TOKEN` in repository secrets to complete the release closure cleanly.

- Preferred: GitHub App installation token scoped to this repository.
- Acceptable: PAT with `contents:write` and `pull_requests:write`.

Behavior:

- With `RELEASE_PR_TOKEN`: the release workflow pushes `release/formula-*` and opens the formula PR using that token, so normal `push` and `pull_request` checks attach to the PR.
- Without `RELEASE_PR_TOKEN`: the workflow falls back to `github.token` and manually dispatches `CI` for the formula branch. That keeps the branch tested, but the dispatched run may not satisfy required PR checks on the PR itself.

## What counts as a successful release

A release is only considered fully successful when all of these are true:

1. GitHub Release assets exist for `darwin/linux` on `amd64/arm64`.
2. Per-archive `.sha256` files and `checksums.txt` are present.
3. The release workflow successfully creates the formula-sync PR.
4. `Formula/mreviewer.rb` is merged back to `main`.
5. The installer works against the real release assets.
6. The Homebrew tap/install path works against the merged formula.

Publishing only the GitHub Release assets is a partial success, not the full release closure.

## Current release status

As of `2026-04-02`, the first full production release cycle has already completed successfully on `v0.1.5`:

1. tag push
2. Release CLI asset publish
3. automatic formula-sync PR creation
4. formula PR merge
5. installer verification
6. Homebrew verification

The remaining release automation improvement is narrower:

- keep the formula-sync PR on the normal `push`/`pull_request` check path by using `RELEASE_PR_TOKEN`, instead of relying on a manually dispatched fallback CI run.

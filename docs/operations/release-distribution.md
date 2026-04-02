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

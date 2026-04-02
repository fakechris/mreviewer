#!/usr/bin/env bash
set -euo pipefail

repo="${MREVIEWER_GITHUB_REPO:-fakechris/mreviewer}"
version="${MREVIEWER_INSTALL_VERSION:-}"
install_dir="${MREVIEWER_INSTALL_DIR:-$HOME/.local/bin}"
dry_run=0

usage() {
  cat <<'EOF'
Usage: install.sh [--version <tag>] [--install-dir <dir>] [--dry-run]

Installs the standalone mreviewer CLI from GitHub Releases.

Options:
  --version <tag>       Install a specific release tag (for example v1.2.3)
  --install-dir <dir>   Destination directory for the mreviewer binary
  --dry-run             Print the selected asset URL and destination only
  -h, --help            Show this help message
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      version="${2:-}"
      shift 2
      ;;
    --install-dir)
      install_dir="${2:-}"
      shift 2
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "install.sh: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

detect_os() {
  case "$(uname -s)" in
    Linux) echo "linux" ;;
    Darwin) echo "darwin" ;;
    *)
      echo "install.sh: unsupported OS $(uname -s)" >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)
      echo "install.sh: unsupported architecture $(uname -m)" >&2
      exit 1
      ;;
  esac
}

resolve_latest_version() {
  curl -fsSL "https://api.github.com/repos/${repo}/releases/latest" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -1
}

os_name="$(detect_os)"
arch_name="$(detect_arch)"

if [[ -z "$version" ]]; then
  version="$(resolve_latest_version)"
fi

if [[ -z "$version" ]]; then
  echo "install.sh: unable to determine release version for ${repo}" >&2
  exit 1
fi

archive="mreviewer_${version#v}_${os_name}_${arch_name}.tar.gz"
url="https://github.com/${repo}/releases/download/${version}/${archive}"
target="${install_dir}/mreviewer"

if [[ "$dry_run" == "1" ]]; then
  echo "version=${version}"
  echo "url=${url}"
  echo "target=${target}"
  exit 0
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

mkdir -p "$install_dir"
curl -fsSL "$url" -o "$tmpdir/$archive"
tar -xzf "$tmpdir/$archive" -C "$tmpdir"
install -m 0755 "$tmpdir/mreviewer" "$target"

cat <<EOF
Installed mreviewer to ${target}

Next:
  1. Ensure ${install_dir} is in your PATH
  2. Run: mreviewer init
  3. Run: mreviewer doctor
EOF

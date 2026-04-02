#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 5 ]]; then
  echo "Usage: render-homebrew-formula.sh <version> <darwin-amd64-sha> <darwin-arm64-sha> <linux-amd64-sha> <linux-arm64-sha>" >&2
  exit 1
fi

version="$1"
darwin_amd64_sha="$2"
darwin_arm64_sha="$3"
linux_amd64_sha="$4"
linux_arm64_sha="$5"

cat <<EOF
class Mreviewer < Formula
  desc "Portable AI review council for GitHub and GitLab"
  homepage "https://github.com/fakechris/mreviewer"
  license "MIT"
  version "${version#v}"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/${version}/mreviewer_${version#v}_darwin_arm64.tar.gz"
      sha256 "${darwin_arm64_sha}"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/${version}/mreviewer_${version#v}_darwin_amd64.tar.gz"
      sha256 "${darwin_amd64_sha}"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/${version}/mreviewer_${version#v}_linux_arm64.tar.gz"
      sha256 "${linux_arm64_sha}"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/${version}/mreviewer_${version#v}_linux_amd64.tar.gz"
      sha256 "${linux_amd64_sha}"
    end
  end

  def install
    bin.install "mreviewer"
  end

  test do
    assert_match "mreviewer", shell_output("#{bin}/mreviewer --help", 0)
  end
end
EOF

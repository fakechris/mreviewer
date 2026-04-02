# typed: false
# frozen_string_literal: true

# This formula is updated automatically by the release workflow.
class Mreviewer < Formula
  desc "Portable AI review council for GitHub and GitLab"
  homepage "https://github.com/fakechris/mreviewer"
  license "MIT"
  version "0.0.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.0.0/mreviewer_0.0.0_darwin_arm64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.0.0/mreviewer_0.0.0_darwin_amd64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.0.0/mreviewer_0.0.0_linux_arm64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.0.0/mreviewer_0.0.0_linux_amd64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  def install
    bin.install "mreviewer"
  end

  test do
    assert_match "mreviewer", shell_output("#{bin}/mreviewer --help", 0)
  end
end

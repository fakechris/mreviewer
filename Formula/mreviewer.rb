class Mreviewer < Formula
  desc "Portable AI review council for GitHub and GitLab"
  homepage "https://github.com/fakechris/mreviewer"
  license "MIT"
  version "0.1.9"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.9/mreviewer_0.1.9_darwin_arm64.tar.gz"
      sha256 "a22a9cb99d940d545788e9cac42ea793157f6dadf93455ff0b306ee6e09d0c90"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.9/mreviewer_0.1.9_darwin_amd64.tar.gz"
      sha256 "0b3533d4386726b29e33bbdfbc240feb9469839b3970ccb8d759cf65b4f9806e"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.9/mreviewer_0.1.9_linux_arm64.tar.gz"
      sha256 "b4f9c9c16c9ae2a9b47b3c0a416c6a11c08ddd6ea051856a47f617f3529bf518"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.9/mreviewer_0.1.9_linux_amd64.tar.gz"
      sha256 "b2841050e056df0db24e3395d34a59a8e78b8a91d676a05c75974d891b51f21b"
    end
  end

  def install
    bin.install "mreviewer"
  end

  test do
    assert_match "mreviewer", shell_output("#{bin}/mreviewer --help", 0)
  end
end

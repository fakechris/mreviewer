class Mreviewer < Formula
  desc "Portable AI review council for GitHub and GitLab"
  homepage "https://github.com/fakechris/mreviewer"
  license "MIT"
  version "0.1.5"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.5/mreviewer_0.1.5_darwin_arm64.tar.gz"
      sha256 "36731f051d4974a39dad5b988c09b22fe5d29185ae07ef08db702df3ba9997f8"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.5/mreviewer_0.1.5_darwin_amd64.tar.gz"
      sha256 "c2e8c87b9063a1a91ddfc131fac47a7d607041caaf4a8a17e83319596cf5dd56"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.5/mreviewer_0.1.5_linux_arm64.tar.gz"
      sha256 "3ee60cf215fe4612af361bb3e22a3b203178f2f968ba039b7f6495f39f6ae32d"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.5/mreviewer_0.1.5_linux_amd64.tar.gz"
      sha256 "1e985aa088b9f4f85f709256946ec361ef7df99368727b560117f9c3e4bf46d5"
    end
  end

  def install
    bin.install "mreviewer"
  end

  test do
    assert_match "mreviewer", shell_output("#{bin}/mreviewer --help", 0)
  end
end

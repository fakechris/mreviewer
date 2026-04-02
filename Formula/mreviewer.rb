class Mreviewer < Formula
  desc "Portable AI review council for GitHub and GitLab"
  homepage "https://github.com/fakechris/mreviewer"
  license "MIT"
  version "0.1.6"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.6/mreviewer_0.1.6_darwin_arm64.tar.gz"
      sha256 "87502173cd7e5a987de763e4b75e0dc971a1c63e1482d21de8daceafbb5a6ab3"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.6/mreviewer_0.1.6_darwin_amd64.tar.gz"
      sha256 "ff4142a5b0a2ea80b8faba056b3f9011354f195ae17afb6fa13c5947da29d4e5"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.6/mreviewer_0.1.6_linux_arm64.tar.gz"
      sha256 "c5121947c4eed2f625ddf31ed659173dae84a48b8d6556f05e6d8a0c412825de"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.6/mreviewer_0.1.6_linux_amd64.tar.gz"
      sha256 "5e96869eadc66cd0f22318f8d8d360d7139decfd39410c6511c91da42cabe426"
    end
  end

  def install
    bin.install "mreviewer"
  end

  test do
    assert_match "mreviewer", shell_output("#{bin}/mreviewer --help", 0)
  end
end

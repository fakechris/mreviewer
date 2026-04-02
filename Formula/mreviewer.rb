class Mreviewer < Formula
  desc "Portable AI review council for GitHub and GitLab"
  homepage "https://github.com/fakechris/mreviewer"
  license "MIT"
  version "0.1.7"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.7/mreviewer_0.1.7_darwin_arm64.tar.gz"
      sha256 "7476d4451ee18a1c3a572c1227360db3d2c1a1d0431185c51d474cc5d837243a"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.7/mreviewer_0.1.7_darwin_amd64.tar.gz"
      sha256 "2313aadd26a96078a7111636071049283de8180a48b0dc7bd8df367eec96fb7c"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.7/mreviewer_0.1.7_linux_arm64.tar.gz"
      sha256 "535fde65c61b267e667422d70141a6926bd40b5c4150071ba9bd407e801994a3"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.7/mreviewer_0.1.7_linux_amd64.tar.gz"
      sha256 "609c719baca77c18ed0dc7ae31121a5fdf24e0f9e01410dcd8c23a8a663d034d"
    end
  end

  def install
    bin.install "mreviewer"
  end

  test do
    assert_match "mreviewer", shell_output("#{bin}/mreviewer --help", 0)
  end
end

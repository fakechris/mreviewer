class Mreviewer < Formula
  desc "Portable AI review council for GitHub and GitLab"
  homepage "https://github.com/fakechris/mreviewer"
  license "MIT"
  version "0.1.12"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.12/mreviewer_0.1.12_darwin_arm64.tar.gz"
      sha256 "4b02b029b35dc788b53178bfd5d9c41abf418aece5a67ab5703ae2c2c050c824"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.12/mreviewer_0.1.12_darwin_amd64.tar.gz"
      sha256 "d032621d42165df9eb4129352b88e36492a413a7273bc8dcd8581e2f61d85304"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.12/mreviewer_0.1.12_linux_arm64.tar.gz"
      sha256 "4df62bd47056e464c6f6e90bf725c6cf15691eaece1930e57f06c18e892b22e9"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.12/mreviewer_0.1.12_linux_amd64.tar.gz"
      sha256 "575e8917ed729b67f1e0b3e2ae2614822b524d49fa3718598bead350b365ac41"
    end
  end

  def install
    bin.install "mreviewer"
  end

  test do
    assert_match "mreviewer", shell_output("#{bin}/mreviewer --help", 0)
  end
end

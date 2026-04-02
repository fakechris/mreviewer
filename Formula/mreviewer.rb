class Mreviewer < Formula
  desc "Portable AI review council for GitHub and GitLab"
  homepage "https://github.com/fakechris/mreviewer"
  license "MIT"
  version "0.1.8"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.8/mreviewer_0.1.8_darwin_arm64.tar.gz"
      sha256 "4edb4952465b51010ac0bdd319123fa05390671ef29958db2de3c5c354fa924c"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.8/mreviewer_0.1.8_darwin_amd64.tar.gz"
      sha256 "65d2d829d7cc99894b31412641c3529a896fcb71abf73f07e0f22e2e878e43ce"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.8/mreviewer_0.1.8_linux_arm64.tar.gz"
      sha256 "92e85c12bd73fa052470f792e90c1767fa01a7a8dad932f6fd6d0325de40a231"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.8/mreviewer_0.1.8_linux_amd64.tar.gz"
      sha256 "b9e0bd4e5cf1753b1d7a3adfca1ba45bb878e6c0b9a1b1b08596acd0314914b0"
    end
  end

  def install
    bin.install "mreviewer"
  end

  test do
    assert_match "mreviewer", shell_output("#{bin}/mreviewer --help", 0)
  end
end

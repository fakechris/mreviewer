class Mreviewer < Formula
  desc "Portable AI review council for GitHub and GitLab"
  homepage "https://github.com/fakechris/mreviewer"
  license "MIT"
  version "0.1.3"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.3/mreviewer_0.1.3_darwin_arm64.tar.gz"
      sha256 "7de1b5597d887d9ca4f50c80ad4c6787489382dcfd9be2d0f09265c8633fb75b"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.3/mreviewer_0.1.3_darwin_amd64.tar.gz"
      sha256 "5ec0f28804a6763a5025a054acce0d3c6142afdf37e198f4e65154371d6512db"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.3/mreviewer_0.1.3_linux_arm64.tar.gz"
      sha256 "810b8ff15e8700ad20fa49791c688d48b6629d407ab30600e32e72ae82272d34"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.3/mreviewer_0.1.3_linux_amd64.tar.gz"
      sha256 "80a4935efa05d847ac66906bd74e2b91fe5b4800c16be1465607efa5e4d71b2e"
    end
  end

  def install
    bin.install "mreviewer"
  end

  test do
    assert_match "mreviewer", shell_output("#{bin}/mreviewer --help", 0)
  end
end

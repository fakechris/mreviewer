class Mreviewer < Formula
  desc "Portable AI review council for GitHub and GitLab"
  homepage "https://github.com/fakechris/mreviewer"
  license "MIT"
  version "0.1.11"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.11/mreviewer_0.1.11_darwin_arm64.tar.gz"
      sha256 "7539e61f4e13c21bc34f1f0a79098b569098f4b2c65603d8968d48aa369a7085"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.11/mreviewer_0.1.11_darwin_amd64.tar.gz"
      sha256 "1614affbe62ee4ae7dcc453385256c42a439fecfbc26ca3c6ed829973048034b"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.11/mreviewer_0.1.11_linux_arm64.tar.gz"
      sha256 "7e1e5bdb0ee80661e706e765eb00a39f4a3b3f6a6f74e7462d03e85f110c7929"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.11/mreviewer_0.1.11_linux_amd64.tar.gz"
      sha256 "26f0c67d34bcc28a74dd019d1d33a657b9bc8e568c9e19431083e16b577b1ce7"
    end
  end

  def install
    bin.install "mreviewer"
  end

  test do
    assert_match "mreviewer", shell_output("#{bin}/mreviewer --help", 0)
  end
end

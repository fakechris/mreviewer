class Mreviewer < Formula
  desc "Portable AI review council for GitHub and GitLab"
  homepage "https://github.com/fakechris/mreviewer"
  license "MIT"
  version "0.1.10"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.10/mreviewer_0.1.10_darwin_arm64.tar.gz"
      sha256 "51c6f6343f7afd0c1feb31ecbf31545dd63728debb4efb269bc0de1afe7b5abd"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.10/mreviewer_0.1.10_darwin_amd64.tar.gz"
      sha256 "e1f31ab213fabb4741500c2e7dc580c3c247e81e46a262511ef703ee0b4f27cb"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.10/mreviewer_0.1.10_linux_arm64.tar.gz"
      sha256 "cad5af8983c5e2f066f9fc072f453ef8491cfb7560d1639dc9be79859b768450"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.10/mreviewer_0.1.10_linux_amd64.tar.gz"
      sha256 "3ea0ed5cbc32efb76c23ea7f5e2044b737027a8f29439dea36ec6285ac8eebf3"
    end
  end

  def install
    bin.install "mreviewer"
  end

  test do
    assert_match "mreviewer", shell_output("#{bin}/mreviewer --help", 0)
  end
end

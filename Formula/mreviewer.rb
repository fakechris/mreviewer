class Mreviewer < Formula
  desc "Portable AI review council for GitHub and GitLab"
  homepage "https://github.com/fakechris/mreviewer"
  license "MIT"
  version "0.1.13"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.13/mreviewer_0.1.13_darwin_arm64.tar.gz"
      sha256 "102cf980e4bb07ece34ae67e1f5908e31dea4358d1195c7dbaafa9e5461781ed"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.13/mreviewer_0.1.13_darwin_amd64.tar.gz"
      sha256 "760ee2c54d009deb18623c11c009fb897c47984efa15ae0423b386b4dd4a1f7d"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.13/mreviewer_0.1.13_linux_arm64.tar.gz"
      sha256 "6c583d05833214b3f0278b4393bdc87b89d6b7df5b721a79ba0d649557c5455f"
    else
      url "https://github.com/fakechris/mreviewer/releases/download/v0.1.13/mreviewer_0.1.13_linux_amd64.tar.gz"
      sha256 "a5912bfb8c7f64984b97d49662dc198477d78aef544b6e812bfba5d8a8864aba"
    end
  end

  def install
    bin.install "mreviewer"
  end

  test do
    assert_match "mreviewer", shell_output("#{bin}/mreviewer --help", 0)
  end
end

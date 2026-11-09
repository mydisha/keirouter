# Auto-updated by release.yml on tag v0.3.0. Do not edit manually.
class Keirouter < Formula
  desc "Self-hosted AI gateway — one endpoint for 130+ LLM providers with fallback, caching, and a dashboard"
  homepage "https://github.com/mydisha/keirouter"
  version "0.3.0"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/mydisha/keirouter/releases/download/v0.3.0/keirouter_v0.3.0_darwin_arm64.tar.gz"
      sha256 "ddf3fdc2fb190713328145efe439eacfc957e2a86151ef7485fa7dbddf1c0006"
    else
      url "https://github.com/mydisha/keirouter/releases/download/v0.3.0/keirouter_v0.3.0_darwin_amd64.tar.gz"
      sha256 "84bcd22010a4924fb62fd2267975068e0e337bf4e4a27b1e6efd985ea063ae91"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/mydisha/keirouter/releases/download/v0.3.0/keirouter_v0.3.0_linux_arm64.tar.gz"
      sha256 "a1c3d3797e04c0af5e6378cba47d90dc84c682e86b61ac611a36a47d92fbf0e4"
    else
      url "https://github.com/mydisha/keirouter/releases/download/v0.3.0/keirouter_v0.3.0_linux_amd64.tar.gz"
      sha256 "5d9fe1ce98e04e3ec5be7710b3b4819a265e2d98d455a67ad5ef8461a596b5d6"
    end
  end

  def install
    bin.install "keirouter"
    (share/"keirouter").install "frontend"
  end

  service do
    run [opt_bin/"keirouter", "start", "--no-browser"]
    keep_alive true
    log_path var/"log/keirouter.log"
    error_log_path var/"log/keirouter.log"
  end

  def caveats
    <<~EOS
      Quick start:
        keirouter start                 # start server on :20180
        brew services start keirouter   # or run it in the background, on login

      Dashboard: http://localhost:20180  (default password: keirouter)
    EOS
  end

  test do
    assert_match "KeiRouter", shell_output("\#{bin}/keirouter --help 2>&1")
  end
end

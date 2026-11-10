# Auto-updated by release.yml on tag v0.4.0. Do not edit manually.
class Keirouter < Formula
  desc "Self-hosted AI gateway — one endpoint for 130+ LLM providers with fallback, caching, and a dashboard"
  homepage "https://github.com/mydisha/keirouter"
  version "0.4.0"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/mydisha/keirouter/releases/download/v0.4.0/keirouter_v0.4.0_darwin_arm64.tar.gz"
      sha256 "d7f44f0889ebc8ac5b2b4554002d0e7b9cde78b3eada795e11c3105c3d509c81"
    else
      url "https://github.com/mydisha/keirouter/releases/download/v0.4.0/keirouter_v0.4.0_darwin_amd64.tar.gz"
      sha256 "020c9196aafa6de5140ba4d2211feb92f7c567dd8f77c664bb2ac4f11f15f7f0"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/mydisha/keirouter/releases/download/v0.4.0/keirouter_v0.4.0_linux_arm64.tar.gz"
      sha256 "0f62a1981652d09f69b644c315699954b43f7f2ffa8254ad06e48091c409811f"
    else
      url "https://github.com/mydisha/keirouter/releases/download/v0.4.0/keirouter_v0.4.0_linux_amd64.tar.gz"
      sha256 "47c3af99b4ca17dd49964480ee5c39c64336db93a10abe7b3e9a3d7aa5d26dff"
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

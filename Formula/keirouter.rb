# Auto-updated by release.yml on tag v0.3.1. Do not edit manually.
class Keirouter < Formula
  desc "Self-hosted AI gateway — one endpoint for 130+ LLM providers with fallback, caching, and a dashboard"
  homepage "https://github.com/mydisha/keirouter"
  version "0.3.1"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/mydisha/keirouter/releases/download/v0.3.1/keirouter_v0.3.1_darwin_arm64.tar.gz"
      sha256 "7f8fdc6022aa22e786430697219ae33bd197ab9587356d0700b823e066e771c4"
    else
      url "https://github.com/mydisha/keirouter/releases/download/v0.3.1/keirouter_v0.3.1_darwin_amd64.tar.gz"
      sha256 "edb678c3b5f5332d88f6c72bed7fa9d177a24813e7f2e2a3b496c271e38dd470"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/mydisha/keirouter/releases/download/v0.3.1/keirouter_v0.3.1_linux_arm64.tar.gz"
      sha256 "3fd2c6d5bf59ec9bae7f6d8610e71efb6a1d29eb52f406a859a527c494534eba"
    else
      url "https://github.com/mydisha/keirouter/releases/download/v0.3.1/keirouter_v0.3.1_linux_amd64.tar.gz"
      sha256 "d1993594b599eb3d028f9d8bd4cceb30a2a6aaef7b8c624848e5a34c4400bf39"
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

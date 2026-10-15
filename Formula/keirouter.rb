# Auto-updated by release.yml on tag v0.1.29. Do not edit manually.
class Keirouter < Formula
  desc "Self-hosted AI gateway — one endpoint for 130+ LLM providers with fallback, caching, and a dashboard"
  homepage "https://github.com/mydisha/keirouter"
  version "0.1.29"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/mydisha/keirouter/releases/download/v0.1.29/keirouter_v0.1.29_darwin_arm64.tar.gz"
      sha256 "665be65c765bd6169a9700c4e8344c312e0d887a575dbd25267cbacc408c8031"
    else
      url "https://github.com/mydisha/keirouter/releases/download/v0.1.29/keirouter_v0.1.29_darwin_amd64.tar.gz"
      sha256 "ec3896d03a6171fe787e07d5690dc3f848d352845ea188c01c2735e592c71769"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/mydisha/keirouter/releases/download/v0.1.29/keirouter_v0.1.29_linux_arm64.tar.gz"
      sha256 "7778165521c55cf3e597a761bcdd0a5ed66478ed5d60751e25b88db98bde6756"
    else
      url "https://github.com/mydisha/keirouter/releases/download/v0.1.29/keirouter_v0.1.29_linux_amd64.tar.gz"
      sha256 "04301baad786a8883874895b79b82362f5b8523e237af9f41f12d1031f62b7c8"
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

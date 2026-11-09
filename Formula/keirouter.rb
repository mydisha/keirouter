# Auto-updated by release.yml on tag v0.3.2. Do not edit manually.
class Keirouter < Formula
  desc "Self-hosted AI gateway — one endpoint for 130+ LLM providers with fallback, caching, and a dashboard"
  homepage "https://github.com/mydisha/keirouter"
  version "0.3.2"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/mydisha/keirouter/releases/download/v0.3.2/keirouter_v0.3.2_darwin_arm64.tar.gz"
      sha256 "6a73d7c756612c5f0ed21ae2515ab9444c6ff765da8911f968795f4a1d572490"
    else
      url "https://github.com/mydisha/keirouter/releases/download/v0.3.2/keirouter_v0.3.2_darwin_amd64.tar.gz"
      sha256 "bf48a922c2011219e31e2e5661723b98f883afe4c08c623fb9f40f0e2f30c33d"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/mydisha/keirouter/releases/download/v0.3.2/keirouter_v0.3.2_linux_arm64.tar.gz"
      sha256 "c3c5bf0bfa12e4c381abe20d319902b010819ef94a77e19905e26e9291c40609"
    else
      url "https://github.com/mydisha/keirouter/releases/download/v0.3.2/keirouter_v0.3.2_linux_amd64.tar.gz"
      sha256 "5392695fc595d2635ecf8177876245663148d2d22d7e3aad3e44421b79dbc70e"
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

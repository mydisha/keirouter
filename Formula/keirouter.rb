# Auto-updated by release.yml on tag v0.2.0. Do not edit manually.
class Keirouter < Formula
  desc "Self-hosted AI gateway — one endpoint for 130+ LLM providers with fallback, caching, and a dashboard"
  homepage "https://github.com/mydisha/keirouter"
  version "0.2.0"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/mydisha/keirouter/releases/download/v0.2.0/keirouter_v0.2.0_darwin_arm64.tar.gz"
      sha256 "acbefdd81f4ef2cea6dc7c05ae50da446aa2ed5fcaa1d68295b72615fac8b74d"
    else
      url "https://github.com/mydisha/keirouter/releases/download/v0.2.0/keirouter_v0.2.0_darwin_amd64.tar.gz"
      sha256 "1ecaa11f44b884d74804372ad68ff11a155c5c2dbb213a27be8244d8fcdb41b3"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/mydisha/keirouter/releases/download/v0.2.0/keirouter_v0.2.0_linux_arm64.tar.gz"
      sha256 "bca598eae365af50b06b5f2c6a67103e8db40be6480c2973b4081133aebd7c04"
    else
      url "https://github.com/mydisha/keirouter/releases/download/v0.2.0/keirouter_v0.2.0_linux_amd64.tar.gz"
      sha256 "4e682c4c5dc85c5ee4b75180292174fb3ad08beeed5aa9d01264e87a97a70202"
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

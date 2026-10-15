#!/usr/bin/env bash
set -euo pipefail

GITHUB_REPO="mydisha/keirouter"
REPO="${KEIROUTER_REPO:-https://github.com/${GITHUB_REPO}.git}"
BRANCH="${KEIROUTER_BRANCH:-main}"
VERSION="${KEIROUTER_VERSION:-}" # binary mode: release tag, default latest
# ~/.keirouter is the runtime data dir (database, master key). Source and
# Docker checkouts live in a subdirectory so they never collide with it; an
# existing checkout at ~/.keirouter itself (older installers) keeps working.
DATA_DIR="$HOME/.keirouter"
if [ -z "${KEIROUTER_DIR:-}" ] && [ -d "$DATA_DIR/.git" ]; then
  INSTALL_DIR="$DATA_DIR"
else
  INSTALL_DIR="${KEIROUTER_DIR:-$DATA_DIR/src}"
fi
BIN_DIR="${KEIROUTER_BIN_DIR:-/usr/local/bin}"
SHARE_DIR="${KEIROUTER_SHARE_DIR:-/usr/local/share/keirouter}"
FORCE_MODE="${KEIROUTER_FORCE:-}" # replace, sync/keep, or abort
INSTALL_MODE="${KEIROUTER_INSTALL_MODE:-binary}" # binary, source, or docker
WITH_SERVICE="${KEIROUTER_SERVICE:-}" # set to 1 to install a systemd user service (Linux)

info() { printf "\033[1;34m>\033[0m %s\n" "$*"; }
ok() { printf "\033[1;32mOK\033[0m %s\n" "$*"; }
warn() { printf "\033[1;33m!\033[0m %s\n" "$*"; }
die() {
  printf "\033[1;31mERROR\033[0m %s\n" "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Install KeiRouter.

Usage:
  install.sh [--binary|--source|--docker] [--service]

Modes:
  --binary  Download the prebuilt release for this OS/CPU (macOS or Linux,
            amd64 or arm64). Needs only curl or wget. This is the default.
  --source  Build and install a local keirouter binary. Requires Go 1.26+,
            Node.js 20.19+, npm, and git.
  --docker  Clone/update the repo and start KeiRouter with Docker Compose.
            Requires Docker with the compose plugin.

Options:
  --service Linux only: install and start a systemd user service so
            KeiRouter runs in the background and starts on boot.

Environment overrides:
  KEIROUTER_VERSION       Release tag for --binary (default: latest)
  KEIROUTER_DIR           Source/Docker checkout directory (default: ~/.keirouter/src)
  KEIROUTER_BIN_DIR       Binary install directory (default: /usr/local/bin)
  KEIROUTER_SHARE_DIR     Dashboard asset directory (default: /usr/local/share/keirouter)
  KEIROUTER_FORCE         replace, sync/keep, or abort for existing non-git dirs
  KEIROUTER_BRANCH        Git branch for --source/--docker (default: main)
  KEIROUTER_INSTALL_MODE  binary, source, or docker
  KEIROUTER_SERVICE       1 to behave as if --service was passed
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --binary)
      INSTALL_MODE="binary"
      ;;
    --source)
      INSTALL_MODE="source"
      ;;
    --docker)
      INSTALL_MODE="docker"
      ;;
    --service)
      WITH_SERVICE=1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "Unknown option: $1"
      ;;
  esac
  shift
done

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required but was not found"
}

version_ge() {
  awk -v have="$1" -v need="$2" 'BEGIN {
    split(have, h, ".")
    split(need, n, ".")
    for (i = 1; i <= 3; i++) {
      hv = h[i] + 0
      nv = n[i] + 0
      if (hv > nv) exit 0
      if (hv < nv) exit 1
    }
    exit 0
  }'
}

require_source_prereqs() {
  for cmd in git go node npm awk; do
    need_cmd "$cmd"
  done

  go_version="$(go version | awk '{print $3}' | sed 's/^go//' | cut -d- -f1)"
  version_ge "$go_version" "1.26.0" || die "Go 1.26+ is required (found $go_version)"

  node_version="$(node -v | sed 's/^v//' | cut -d- -f1)"
  version_ge "$node_version" "20.19.0" || die "Node.js 20.19+ is required (found $node_version)"
}

require_docker_prereqs() {
  need_cmd git
  need_cmd docker
  docker compose version >/dev/null 2>&1 || die "Docker Compose plugin is required (docker compose)"
}

# holds_user_data reports whether a directory contains KeiRouter runtime data
# that must never be deleted by the installer.
holds_user_data() {
  [ -e "$1/keirouter.db" ] || [ -e "$1/master.key" ]
}

handle_existing_dir() {
  if [ -d "$INSTALL_DIR/.git" ]; then
    info "Updating KeiRouter in $INSTALL_DIR"
    git -C "$INSTALL_DIR" fetch origin "$BRANCH" --depth 1 --quiet
    git -C "$INSTALL_DIR" checkout "$BRANCH" --quiet 2>/dev/null || true
    git -C "$INSTALL_DIR" pull --ff-only origin "$BRANCH" --quiet
    cd "$INSTALL_DIR"
    return 0
  fi

  if [ -d "$INSTALL_DIR" ]; then
    if holds_user_data "$INSTALL_DIR"; then
      die "$INSTALL_DIR holds KeiRouter data (database or master key). Set KEIROUTER_DIR to another directory and retry."
    fi
    choice="$FORCE_MODE"
    if [ -z "$choice" ]; then
      echo ""
      warn "Directory $INSTALL_DIR already exists but is not a git checkout."
      echo "  1) Replace  - remove it and fresh clone"
      echo "  2) Sync     - overwrite it with latest $BRANCH"
      echo "  3) Abort"
      echo ""
      read -rp "Choose [1/2/3]: " choice </dev/tty
    fi

    case "$choice" in
      1|replace)
        warn "Removing existing $INSTALL_DIR"
        rm -rf "$INSTALL_DIR"
        info "Cloning KeiRouter into $INSTALL_DIR"
        git clone --depth 1 --branch "$BRANCH" "$REPO" "$INSTALL_DIR"
        ;;
      2|sync|keep)
        warn "Syncing $INSTALL_DIR to origin/$BRANCH. Local files may be overwritten."
        if ! git -C "$INSTALL_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
          git -C "$INSTALL_DIR" init -q
          git -C "$INSTALL_DIR" remote add origin "$REPO" 2>/dev/null || \
            git -C "$INSTALL_DIR" remote set-url origin "$REPO"
        fi
        git -C "$INSTALL_DIR" fetch origin "$BRANCH" --depth 1 --quiet
        git -C "$INSTALL_DIR" reset --hard "origin/$BRANCH" --quiet
        ;;
      3|abort)
        die "Aborted by user."
        ;;
      *)
        die "Invalid choice: $choice"
        ;;
    esac
    cd "$INSTALL_DIR"
    return 0
  fi

  info "Cloning KeiRouter into $INSTALL_DIR"
  mkdir -p "$(dirname "$INSTALL_DIR")"
  git clone --depth 1 --branch "$BRANCH" "$REPO" "$INSTALL_DIR"
  cd "$INSTALL_DIR"
}

copy_with_optional_sudo() {
  src="$1"
  dest="$2"
  mode="${3:-}"
  dest_dir="$(dirname "$dest")"

  # Copy next to the target, then rename: replacing a running binary in place
  # fails with "Text file busy" on Linux, while a rename swaps it atomically.
  if mkdir -p "$dest_dir" 2>/dev/null && [ -w "$dest_dir" ]; then
    cp "$src" "$dest.new"
    if [ -n "$mode" ]; then chmod "$mode" "$dest.new"; fi
    mv -f "$dest.new" "$dest"
    return 0
  fi

  need_cmd sudo
  info "Need sudo to write $dest"
  sudo mkdir -p "$dest_dir"
  sudo cp "$src" "$dest.new"
  if [ -n "$mode" ]; then sudo chmod "$mode" "$dest.new"; fi
  sudo mv -f "$dest.new" "$dest"
}

install_frontend_assets() {
  src="$1"
  dest="$SHARE_DIR/frontend/dist"
  [ -d "$src" ] || die "Dashboard build output not found at $src"

  if mkdir -p "$(dirname "$dest")" 2>/dev/null && [ -w "$(dirname "$dest")" ]; then
    rm -rf "$dest"
    cp -R "$src" "$dest"
    return 0
  fi

  need_cmd sudo
  info "Need sudo to install dashboard assets to $dest"
  sudo mkdir -p "$(dirname "$dest")"
  sudo rm -rf "$dest"
  sudo cp -R "$src" "$dest"
}

# fetch downloads a URL to a file (or stdout with "-") using curl or wget.
fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$1" -O "$2"
  else
    die "curl or wget is required"
  fi
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo ""
  fi
}

detect_platform() {
  case "$(uname -s)" in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    *) die "Unsupported OS: $(uname -s). On Windows use scripts/install.ps1." ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) die "Unsupported CPU: $(uname -m). Prebuilt releases cover amd64 and arm64; try --source." ;;
  esac
}

resolve_version() {
  if [ -n "$VERSION" ]; then
    case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
    return 0
  fi
  info "Resolving latest release"
  VERSION="$(fetch "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" - |
    sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
  [ -n "$VERSION" ] || die "Could not resolve the latest release. Set KEIROUTER_VERSION (e.g. v0.1.29) and retry."
}

run_binary_install() {
  need_cmd tar
  detect_platform
  resolve_version
  ok "Version: $VERSION ($os/$arch)"

  asset="keirouter_${VERSION}_${os}_${arch}.tar.gz"
  base="https://github.com/${GITHUB_REPO}/releases/download/${VERSION}"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  info "Downloading $asset"
  fetch "$base/$asset" "$tmp/$asset" || die "Download failed: $base/$asset"

  if fetch "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
    want="$(awk -v f="$asset" '$2 == f {print $1}' "$tmp/checksums.txt")"
    have="$(sha256_of "$tmp/$asset")"
    if [ -n "$want" ] && [ -n "$have" ]; then
      [ "$want" = "$have" ] || die "Checksum mismatch for $asset"
      ok "Checksum verified"
    else
      warn "Could not verify checksum; continuing"
    fi
  fi

  tar -xzf "$tmp/$asset" -C "$tmp"
  [ -x "$tmp/keirouter/keirouter" ] || die "keirouter binary not found in $asset"

  copy_with_optional_sudo "$tmp/keirouter/keirouter" "$BIN_DIR/keirouter" "0755"
  install_frontend_assets "$tmp/keirouter/frontend/dist"
  print_installed
}

run_source_install() {
  require_source_prereqs
  handle_existing_dir

  info "Installing dashboard dependencies"
  (cd frontend && npm ci --quiet)

  info "Building dashboard"
  (cd frontend && npm run build --silent)

  info "Building keirouter binary"
  GIT_DESCRIBE="$(git -C "$INSTALL_DIR" describe --tags --always --dirty 2>/dev/null || echo dev)"
  (cd backend && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=${GIT_DESCRIBE}" -o "$INSTALL_DIR/keirouter" ./cmd/keirouter)

  copy_with_optional_sudo "$INSTALL_DIR/keirouter" "$BIN_DIR/keirouter" "0755"
  install_frontend_assets "$INSTALL_DIR/frontend/dist"
  print_installed
}

print_installed() {
  ok "KeiRouter installed to $BIN_DIR/keirouter"
  ok "Dashboard assets installed to $SHARE_DIR/frontend/dist"
  case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) warn "$BIN_DIR is not on your PATH; add it or run $BIN_DIR/keirouter" ;;
  esac
  if [ -n "$WITH_SERVICE" ]; then
    install_service
    return 0
  fi
  echo ""
  echo "Quick start:"
  echo "  keirouter start            # start the server on :20180 and open the dashboard"
  echo "  keirouter tray             # or run in the background with a tray icon"
  echo ""
  echo "Dashboard: http://localhost:20180  (default password: keirouter)"
}

# install_service writes a systemd user unit so KeiRouter starts on boot and
# restarts on failure. Linger keeps it running after you log out (VPS/SSH).
install_service() {
  [ "$(uname -s)" = "Linux" ] || die "--service is Linux only. On macOS use: brew services start keirouter"
  need_cmd systemctl
  unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
  mkdir -p "$unit_dir"
  cat >"$unit_dir/keirouter.service" <<EOF
[Unit]
Description=KeiRouter AI gateway
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=$BIN_DIR/keirouter start --no-browser
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
EOF
  systemctl --user daemon-reload
  systemctl --user enable --now keirouter.service
  if command -v loginctl >/dev/null 2>&1; then
    loginctl enable-linger "$USER" 2>/dev/null || sudo loginctl enable-linger "$USER" 2>/dev/null ||
      warn "Could not enable linger; KeiRouter stops when you log out (run: sudo loginctl enable-linger $USER)"
  fi
  ok "systemd user service installed and started"
  echo ""
  echo "  systemctl --user status keirouter     # check it"
  echo "  journalctl --user -u keirouter -f     # follow logs"
  echo ""
  echo "Dashboard: http://localhost:20180  (default password: keirouter)"
}

run_docker_install() {
  require_docker_prereqs
  handle_existing_dir

  if [ ! -f .env ] && [ -f .env.example ]; then
    cp .env.example .env
    warn "Created .env from .env.example. Add KEIROUTER_MASTER_KEY for production/VPS installs."
  fi

  info "Starting KeiRouter with Docker Compose"
  docker compose up -d --build

  ok "KeiRouter is starting in Docker"
  echo ""
  echo "Dashboard: http://localhost:${KEIROUTER_PORT:-20180}"
  echo "Logs:      cd $INSTALL_DIR && docker compose logs -f keirouter"
}

case "$INSTALL_MODE" in
  binary)
    run_binary_install
    ;;
  source)
    run_source_install
    ;;
  docker)
    run_docker_install
    ;;
  *)
    die "Unknown KEIROUTER_INSTALL_MODE: $INSTALL_MODE"
    ;;
esac

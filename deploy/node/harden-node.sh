#!/usr/bin/env bash
# Base setup for a fresh Debian/Ubuntu hosting node.
#
#   sudo bash harden-node.sh                      # user tokenizer, SSH port 1319
#   sudo bash harden-node.sh --pubkey "ssh-ed25519 AAAA... you@laptop"
#
# What it does:
#   - creates a sudo user with a random 32-character password (shown once)
#   - moves SSH to the new port and disables root login
#   - ufw: deny incoming, allow the SSH port, 80/443 only from Cloudflare
#     (Docker-published ports included)
#   - fail2ban jail for sshd
#   - refreshes the Cloudflare ranges weekly
#
#   sudo keirouter-node-harden --refresh-cloudflare   # refresh by hand
#
# A re-run sets a new password. Keep your current SSH session open until you
# have logged in on the new port from a second terminal.
set -euo pipefail

CONF_DIR=/etc/keirouter-node
CONF_FILE=$CONF_DIR/node.env
INSTALL_PATH=/usr/local/sbin/keirouter-node-harden
SSHD_DROPIN=/etc/ssh/sshd_config.d/00-keirouter.conf
F2B_JAIL=/etc/fail2ban/jail.d/keirouter-sshd.local
BEGIN_MARK="# BEGIN keirouter-node"
END_MARK="# END keirouter-node"

NEW_USER=tokenizer
SSH_PORT=1319
PUBKEY=""
EXT_IFACE=""
MODE=setup

log()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//'
  cat <<'EOF'

Flags:
  --user NAME           sudo user to create (default tokenizer)
  --ssh-port PORT       new SSH port, 1024-65535 (default 1319)
  --pubkey KEY          optional public key; when set, SSH accepts keys only
                        and the password is used for sudo
  --iface NAME          public network interface (auto-detected by default)
  --refresh-cloudflare  only refresh Cloudflare ranges and reload ufw
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --user) NEW_USER=${2:-}; shift 2 ;;
    --ssh-port) SSH_PORT=${2:-}; shift 2 ;;
    --pubkey) PUBKEY=${2:-}; shift 2 ;;
    --iface) EXT_IFACE=${2:-}; shift 2 ;;
    --refresh-cloudflare) MODE=refresh; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown flag: $1 (see --help)" ;;
  esac
done

[[ $EUID -eq 0 ]] || die "run as root (sudo bash $0 ...)"
[[ -f $0 ]] || die "save the script to a file first; piping into bash is not supported"
command -v apt-get >/dev/null || die "only Debian/Ubuntu are supported"

# Cloudflare ranges are cached so a failed fetch keeps the last good list
# instead of locking every visitor out.
fetch_cloudflare() {
  local fam tmp min
  mkdir -p "$CONF_DIR"
  for fam in v4 v6; do
    tmp=$(mktemp)
    min=$([[ $fam == v4 ]] && echo 10 || echo 5)
    if curl -fsS --max-time 20 "https://www.cloudflare.com/ips-$fam" -o "$tmp" \
      && grep -cE '^[0-9a-fA-F:.]+/[0-9]+$' "$tmp" | awk -v m="$min" '{exit !($1>=m)}' \
      && ! grep -vqE '^[0-9a-fA-F:.]+/[0-9]+$|^$' "$tmp"; then
      grep -E '/' "$tmp" >"$CONF_DIR/cloudflare-ips-$fam"
    elif [[ -s $CONF_DIR/cloudflare-ips-$fam ]]; then
      warn "could not fetch Cloudflare $fam ranges, keeping the cached list"
    else
      rm -f "$tmp"
      die "could not fetch Cloudflare $fam ranges and no cached list exists"
    fi
    rm -f "$tmp"
  done
}

# Docker-published ports skip ufw's INPUT rules, so the 80/443 filter also has
# to live in DOCKER-USER. --ctorigdstport matches the host port before DNAT.
render_block() {
  local fam=$1 cidr port proto after=ufw-after-input
  [[ $fam == v6 ]] && after=ufw6-after-input
  echo "$BEGIN_MARK"
  echo "*filter"
  echo ":DOCKER-USER - [0:0]"
  echo "-A DOCKER-USER -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN"
  echo "-A DOCKER-USER ! -i $EXT_IFACE -j RETURN"
  while read -r cidr; do
    [[ -n $cidr ]] || continue
    for port in tcp:80 tcp:443 udp:443; do
      proto=${port%%:*}
      echo "-A DOCKER-USER -s $cidr -p $proto -m conntrack --ctorigdstport ${port#*:} --ctdir ORIGINAL -j RETURN"
    done
    # Covers a web server that runs on the host instead of in Docker.
    echo "-A $after -s $cidr -p tcp -m multiport --dports 80,443 -j ACCEPT"
    echo "-A $after -s $cidr -p udp --dport 443 -j ACCEPT"
  done <"$CONF_DIR/cloudflare-ips-$fam"
  echo "-A DOCKER-USER -i $EXT_IFACE -j DROP"
  echo "-A DOCKER-USER -j RETURN"
  echo "COMMIT"
  echo "$END_MARK"
}

write_ufw_rules() {
  local fam file tmp
  for fam in v4 v6; do
    file=/etc/ufw/after.rules
    [[ $fam == v6 ]] && file=/etc/ufw/after6.rules
    [[ -f $file.keirouter.bak ]] || cp "$file" "$file.keirouter.bak"
    tmp=$(mktemp)
    awk -v b="$BEGIN_MARK" -v e="$END_MARK" '$0==b{s=1} !s{print} $0==e{s=0}' "$file" >"$tmp"
    render_block "$fam" >>"$tmp"
    install -m 0640 "$tmp" "$file"
    rm -f "$tmp"
  done
}

if [[ $MODE == refresh ]]; then
  [[ -f $CONF_FILE ]] || die "$CONF_FILE missing; run the full setup first"
  # shellcheck source=/dev/null
  source "$CONF_FILE"
  fetch_cloudflare
  write_ufw_rules
  ufw reload >/dev/null
  log "Cloudflare ranges refreshed ($(wc -l <"$CONF_DIR/cloudflare-ips-v4") v4, $(wc -l <"$CONF_DIR/cloudflare-ips-v6") v6)"
  exit 0
fi

[[ $NEW_USER =~ ^[a-z_][a-z0-9_-]{0,31}$ ]] || die "--user is not a valid Linux username"
[[ $NEW_USER != root ]] || die "--user cannot be root"
if [[ -n $PUBKEY ]]; then
  [[ $PUBKEY =~ ^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp(256|384|521)|sk-ssh-ed25519@openssh.com|sk-ecdsa-sha2-nistp256@openssh.com)\ [A-Za-z0-9+/=]+ ]] \
    || die "--pubkey is not an OpenSSH public key"
fi
if ! [[ $SSH_PORT =~ ^[0-9]+$ ]] || (( SSH_PORT < 1024 || SSH_PORT > 65535 )); then
  die "--ssh-port must be between 1024 and 65535"
fi

[[ -n $EXT_IFACE ]] || EXT_IFACE=$(ip -4 route show default | awk '!f{print $5; f=1}')
if [[ -z $EXT_IFACE ]] || ! ip link show "$EXT_IFACE" >/dev/null 2>&1; then
  die "could not detect the public interface; pass --iface"
fi

mkdir -p /run/sshd
# awk reads to the end on purpose: exiting early would kill sshd with
# SIGPIPE, and pipefail would then stop the script without a message.
CURRENT_SSH_PORT=$(sshd -T 2>/dev/null | awk '$1=="port" && !f{print $2; f=1}') || true
CURRENT_SSH_PORT=${CURRENT_SSH_PORT:-22}

log "Installing packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ufw fail2ban python3-systemd curl sudo openssh-server >/dev/null

log "Creating sudo user $NEW_USER"
USER_PASSWORD=$(LC_ALL=C tr -dc 'A-Za-z0-9' < <(head -c 4096 /dev/urandom))
USER_PASSWORD=${USER_PASSWORD:0:32}
[[ ${#USER_PASSWORD} -eq 32 ]] || die "could not generate a password"
id "$NEW_USER" >/dev/null 2>&1 || useradd -m -s /bin/bash "$NEW_USER"
usermod -aG sudo "$NEW_USER"
# Read from a here-string so the password never shows in the process list.
chpasswd <<<"$NEW_USER:$USER_PASSWORD"
if [[ -n $PUBKEY ]]; then
  HOME_DIR=$(getent passwd "$NEW_USER" | cut -d: -f6)
  install -d -m 0700 -o "$NEW_USER" -g "$(id -gn "$NEW_USER")" "$HOME_DIR/.ssh"
  AK=$HOME_DIR/.ssh/authorized_keys
  touch "$AK"
  grep -qxF "$PUBKEY" "$AK" || echo "$PUBKEY" >>"$AK"
  chown "$NEW_USER:$(id -gn "$NEW_USER")" "$AK"
  chmod 0600 "$AK"
fi

log "Configuring ufw"
fetch_cloudflare
cat >"$CONF_FILE" <<EOF
EXT_IFACE=$EXT_IFACE
SSH_PORT=$SSH_PORT
EOF
write_ufw_rules
ufw default deny incoming >/dev/null
ufw default allow outgoing >/dev/null
ufw allow "$SSH_PORT/tcp" comment 'ssh' >/dev/null
# Old port stays open until sshd is confirmed on the new one.
ufw allow "$CURRENT_SSH_PORT/tcp" comment 'ssh (old)' >/dev/null
ufw --force enable >/dev/null
ufw reload >/dev/null

log "Configuring fail2ban"
cat >"$F2B_JAIL" <<EOF
# Managed by harden-node.sh.
[sshd]
enabled = true
mode = aggressive
port = $SSH_PORT
backend = systemd
bantime = 1h
findtime = 10m
maxretry = 5
EOF
systemctl enable fail2ban >/dev/null 2>&1
systemctl restart fail2ban

log "Configuring sshd on port $SSH_PORT"
grep -qE '^\s*Include\s+/etc/ssh/sshd_config\.d/\*\.conf' /etc/ssh/sshd_config \
  || sed -i '1i Include /etc/ssh/sshd_config.d/*.conf' /etc/ssh/sshd_config
mkdir -p /etc/ssh/sshd_config.d
PASSWORD_LOGIN=$([[ -n $PUBKEY ]] && echo no || echo yes)
# 00- so it wins over cloud-init's 50-cloud-init.conf (sshd keeps the first value).
cat >"$SSHD_DROPIN" <<EOF
Port $SSH_PORT
PermitRootLogin no
PasswordAuthentication $PASSWORD_LOGIN
KbdInteractiveAuthentication no
PermitEmptyPasswords no
X11Forwarding no
MaxAuthTries 3
LoginGraceTime 30
EOF

rollback_sshd() {
  rm -f "$SSHD_DROPIN"
  systemctl restart ssh 2>/dev/null || systemctl restart sshd 2>/dev/null || true
  die "$1 (sshd config rolled back, nothing else undone)"
}

sshd -t || rollback_sshd "sshd rejected the new config"
EFFECTIVE=$(sshd -T) || rollback_sshd "sshd -T failed"
for want in "port $SSH_PORT" "permitrootlogin no" "passwordauthentication $PASSWORD_LOGIN"; do
  grep -qx "$want" <<<"$EFFECTIVE" || rollback_sshd "sshd did not pick up '$want'"
done

# Ubuntu 22.10+ starts sshd from ssh.socket, which ignores Port in sshd_config.
if systemctl is-enabled --quiet ssh.socket 2>/dev/null; then
  systemctl disable --now ssh.socket >/dev/null 2>&1
  systemctl enable ssh.service >/dev/null 2>&1
fi
systemctl daemon-reload
systemctl restart ssh 2>/dev/null || systemctl restart sshd

sleep 2
[[ -n $(ss -ltnH "sport = :$SSH_PORT") ]] || rollback_sshd "sshd is not listening on $SSH_PORT"
if [[ $CURRENT_SSH_PORT != "$SSH_PORT" ]]; then
  ufw delete allow "$CURRENT_SSH_PORT/tcp" >/dev/null || true
  ufw delete allow OpenSSH >/dev/null 2>&1 || true
fi

log "Installing weekly Cloudflare refresh"
install -m 0755 "$0" "$INSTALL_PATH"
cat >/etc/systemd/system/keirouter-cloudflare-refresh.service <<EOF
[Unit]
Description=Refresh Cloudflare ranges in ufw
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=$INSTALL_PATH --refresh-cloudflare
EOF
cat >/etc/systemd/system/keirouter-cloudflare-refresh.timer <<'EOF'
[Unit]
Description=Weekly Cloudflare range refresh

[Timer]
OnCalendar=weekly
RandomizedDelaySec=6h
Persistent=true

[Install]
WantedBy=timers.target
EOF
systemctl daemon-reload
systemctl enable --now keirouter-cloudflare-refresh.timer >/dev/null 2>&1

PUBLIC_IP=$(ip -4 addr show "$EXT_IFACE" | awk '/inet / && !f{sub(/\/.*/,"",$2); print $2; f=1}') || true
cat <<EOF

Done. Do not close this session yet.

  1. From a second terminal:  ssh -p $SSH_PORT $NEW_USER@${PUBLIC_IP:-<node-ip>}
  2. Then:                    sudo -v

  SSH          port $SSH_PORT, root login off, $([[ -n $PUBKEY ]] && echo "key only" || echo "password login on")
  ufw          deny incoming; $SSH_PORT open; 80/443 only from Cloudflare
  Docker       published ports on $EXT_IFACE blocked except 80/443 from Cloudflare
  fail2ban     sshd jail, aggressive mode
  Refresh      keirouter-cloudflare-refresh.timer (weekly)

  Password for $NEW_USER (shown once, save it in your password manager now):

      $USER_PASSWORD

EOF

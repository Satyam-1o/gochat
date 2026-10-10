#!/usr/bin/env bash
#
# install-rendezvous.sh — install the gochat rendezvous/relay node as a systemd
# service on a Linux host (Debian/Ubuntu, RHEL/Fedora/Amazon Linux, ...).
#
# It downloads a release binary from GitHub, creates an unprivileged system
# user, installs a hardened systemd unit, opens the listen port in the local
# firewall (if one is active), starts the service, and prints the
# ready-to-paste client bootstrap address.
#
# Usage:
#   sudo ./install-rendezvous.sh [options]
#
# Options:
#   --repo    OWNER/REPO  GitHub releases repo (default: Satyam-1o/gochat)
#   --version VERSION     release tag, e.g. v0.1.0 (default: latest)
#   --port    PORT        listen port (default: 4001)
#   --public  HOST        public IP/DNS to advertise (default: auto-detect via
#                         cloud metadata, then an external echo service)
#   --data    DIR         data dir holding the identity key (default: /var/lib/gochat)
#   --user    NAME        system user to run as (default: gochat)
#   -h, --help            show this help
#
# Environment overrides (same meaning as the flags): GOCHAT_REPO,
# GOCHAT_VERSION, GOCHAT_PORT, GOCHAT_PUBLIC. GOCHAT_BASE_URL overrides the
# release download base URL (for mirrors / air-gapped installs).

set -euo pipefail

REPO="${GOCHAT_REPO:-Satyam-1o/gochat}"
VERSION="${GOCHAT_VERSION:-latest}"
PORT="${GOCHAT_PORT:-4001}"
PUBLIC="${GOCHAT_PUBLIC:-}"
DATA_DIR="/var/lib/gochat"
SVC_USER="gochat"
BASE_URL="${GOCHAT_BASE_URL:-}"

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

# detect_public prints the host's public IPv4 using, in order: AWS IMDSv2,
# AWS IMDSv1, GCP metadata, then public echo services. Prints nothing on failure.
detect_public() {
  local ip="" token=""
  token="$(curl -fsS --connect-timeout 1 -X PUT "http://169.254.169.254/latest/api/token" \
    -H "X-aws-ec2-metadata-token-ttl-seconds: 60" 2>/dev/null || true)"
  if [ -n "$token" ]; then
    ip="$(curl -fsS --connect-timeout 1 -H "X-aws-ec2-metadata-token: $token" \
      http://169.254.169.254/latest/meta-data/public-ipv4 2>/dev/null || true)"
  fi
  [ -z "$ip" ] && ip="$(curl -fsS --connect-timeout 1 \
    http://169.254.169.254/latest/meta-data/public-ipv4 2>/dev/null || true)"
  [ -z "$ip" ] && ip="$(curl -fsS --connect-timeout 1 -H 'Metadata-Flavor: Google' \
    http://169.254.169.254/computeMetadata/v1/instance/network-interfaces/0/access-configs/0/external-ip 2>/dev/null || true)"
  [ -z "$ip" ] && ip="$(curl -fsS --connect-timeout 3 https://api.ipify.org 2>/dev/null || true)"
  [ -z "$ip" ] && ip="$(curl -fsS --connect-timeout 3 https://ifconfig.me/ip 2>/dev/null || true)"
  printf '%s' "$ip"
}

usage() { sed -n '3,26p' "$0" | sed 's/^# \{0,1\}//'; }

while [ $# -gt 0 ]; do
  case "$1" in
    --repo)    REPO="${2:?--repo needs a value}"; shift 2 ;;
    --version) VERSION="${2:?--version needs a value}"; shift 2 ;;
    --port)    PORT="${2:?--port needs a value}"; shift 2 ;;
    --public)  PUBLIC="${2:?--public needs a value}"; shift 2 ;;
    --data)    DATA_DIR="${2:?--data needs a value}"; shift 2 ;;
    --user)    SVC_USER="${2:?--user needs a value}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1 (try --help)" ;;
  esac
done

# Root or sudo for system changes.
SUDO=""
if [ "$(id -u)" -ne 0 ]; then
  command -v sudo >/dev/null 2>&1 || die "run as root (no sudo found)"
  SUDO="sudo"
fi

for c in curl tar; do
  command -v "$c" >/dev/null 2>&1 || die "missing required command: $c"
done

# --- detect target platform ------------------------------------------------
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
[ "$os" = "linux" ] || die "this installer only supports Linux (got $os)"
case "$(uname -m)" in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

# --- resolve the release tag -----------------------------------------------
if [ "$VERSION" = "latest" ]; then
  log "Resolving latest release of $REPO"
  tag="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
    | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1 || true)"
  [ -n "$tag" ] || die "could not find the latest release of $REPO (is it public? GitHub may be rate-limiting)"
else
  tag="$VERSION"
fi
vernum="${tag#v}"
asset="gochat_${vernum}_${os}_${arch}.tar.gz"
[ -n "$BASE_URL" ] || BASE_URL="https://github.com/$REPO/releases/download"
base="$BASE_URL/$tag"

# --- download and verify ---------------------------------------------------
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

log "Downloading $asset"
curl -fsSL -o "$tmp/$asset" "$base/$asset" || die "download failed: $base/$asset"

if curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" 2>/dev/null; then
  if grep -q "  $asset\$" "$tmp/checksums.txt"; then
    ( cd "$tmp" && grep "  $asset\$" checksums.txt | sha256sum -c - >/dev/null ) \
      || die "checksum verification failed for $asset"
    log "Checksum verified"
  fi
fi

tar -xzf "$tmp/$asset" -C "$tmp"
[ -f "$tmp/gochat-rendezvous" ] || die "archive did not contain gochat-rendezvous"

# --- install binary + service ----------------------------------------------
log "Installing /usr/local/bin/gochat-rendezvous ($tag)"
$SUDO install -m 0755 "$tmp/gochat-rendezvous" /usr/local/bin/gochat-rendezvous

if ! id -u "$SVC_USER" >/dev/null 2>&1; then
  log "Creating system user '$SVC_USER'"
  nologin="$(command -v nologin || echo /usr/sbin/nologin)"
  $SUDO useradd --system --user-group --home-dir "$DATA_DIR" \
    --create-home --shell "$nologin" "$SVC_USER"
fi
$SUDO mkdir -p "$DATA_DIR"
$SUDO chown -R "$SVC_USER:$SVC_USER" "$DATA_DIR"

# --- discover the public address -------------------------------------------
if [ -z "$PUBLIC" ]; then
  log "Detecting public IP"
  PUBLIC="$(detect_public 2>/dev/null || true)"
  [ -n "$PUBLIC" ] && log "Public IP: $PUBLIC" \
    || warn "could not detect a public IP; the node will only print its local listen addresses"
fi

exec_args="-port $PORT -data $DATA_DIR"
[ -n "$PUBLIC" ] && exec_args="$exec_args -public $PUBLIC"

log "Writing /etc/systemd/system/gochat-rendezvous.service"
$SUDO tee /etc/systemd/system/gochat-rendezvous.service >/dev/null <<EOF
# Managed by scripts/install-rendezvous.sh — edits will be overwritten.
[Unit]
Description=gochat rendezvous/relay node (PEX + circuit relay + pubsub bridge)
Documentation=https://github.com/$REPO
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SVC_USER
Group=$SVC_USER
ExecStart=/usr/local/bin/gochat-rendezvous $exec_args
Restart=on-failure
RestartSec=3
LimitNOFILE=65536

# Hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK
ReadWritePaths=$DATA_DIR

[Install]
WantedBy=multi-user.target
EOF

# --- open the port locally --------------------------------------------------
open_port() {
  local p="$1"
  if command -v ufw >/dev/null 2>&1 && $SUDO ufw status 2>/dev/null | grep -qi "Status: active"; then
    $SUDO ufw allow "$p/tcp" >/dev/null && $SUDO ufw allow "$p/udp" >/dev/null
    log "Opened $p/tcp and $p/udp in ufw"
  elif command -v firewall-cmd >/dev/null 2>&1 && $SUDO firewall-cmd --state >/dev/null 2>&1; then
    $SUDO firewall-cmd --permanent --add-port="$p/tcp" >/dev/null
    $SUDO firewall-cmd --permanent --add-port="$p/udp" >/dev/null
    $SUDO firewall-cmd --reload >/dev/null
    log "Opened $p/tcp and $p/udp in firewalld"
  else
    warn "no active local firewall found; make sure your cloud security group allows $p/tcp and $p/udp"
  fi
}
open_port "$PORT"

# --- start ------------------------------------------------------------------
log "Starting gochat-rendezvous"
$SUDO systemctl daemon-reload
$SUDO systemctl enable gochat-rendezvous >/dev/null 2>&1 || true
$SUDO systemctl restart gochat-rendezvous

# --- report the client bootstrap address -----------------------------------
bootstrap=""
for _ in $(seq 1 30); do
  bootstrap="$($SUDO journalctl -u gochat-rendezvous --no-pager -n 100 2>/dev/null \
    | grep 'client: gochat -bootstrap' | tail -n1 | sed 's/.*client: //' || true)"
  [ -n "$bootstrap" ] && break
  sleep 0.5
done

printf '\n'
if [ -n "$bootstrap" ]; then
  log "Installed and running ($tag). Give this to clients:"
  printf '\n    %s\n\n' "$bootstrap"
else
  log "Installed and running ($tag)."
  warn "could not read the bootstrap address automatically; check the log:"
  printf '\n    sudo journalctl -u gochat-rendezvous -n 40 --no-pager\n\n'
fi

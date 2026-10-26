#!/usr/bin/env bash
# Installs or updates Shoebox on Debian or Ubuntu: an LXC container, a VM or
# a small server. Run as root. Running it again updates to the latest release
# and keeps your settings and documents.
#
#   bash install.sh
#
# Settings (environment variables, all optional):
#   SHOEBOX_ARCHIVE  archive folder, e.g. a mounted NAS share   (/srv/shoebox/archive)
#   SHOEBOX_DATA     database and caches, keep on a local disk   (/var/lib/shoebox)
#   SHOEBOX_PORT     port to listen on                           (8080)
#   SHOEBOX_LANGS    Tesseract languages, space separated        (deu eng)
#   SHOEBOX_VERSION  release tag to install, e.g. v0.1.0         (latest)
set -euo pipefail

REPO="lcsfls/shoebox"
ARCHIVE="${SHOEBOX_ARCHIVE:-/srv/shoebox/archive}"
DATA="${SHOEBOX_DATA:-/var/lib/shoebox}"
PORT="${SHOEBOX_PORT:-8080}"
LANGS="${SHOEBOX_LANGS:-deu eng}"
VERSION="${SHOEBOX_VERSION:-latest}"

say() { printf '\033[1m==>\033[0m %s\n' "$*"; }
die() { printf '\033[31mError:\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "Run this as root."
command -v apt-get >/dev/null || die "This installer is for Debian and Ubuntu."
command -v systemctl >/dev/null || die "systemd is required."

case "$(dpkg --print-architecture)" in
  amd64) ARCH=amd64 ;;
  arm64) ARCH=arm64 ;;
  *) die "Unsupported architecture $(dpkg --print-architecture)." ;;
esac

say "Installing Tesseract, Poppler, qpdf and ExifTool"
pkgs="ca-certificates curl tesseract-ocr poppler-utils qpdf libimage-exiftool-perl"
for l in $LANGS; do pkgs="$pkgs tesseract-ocr-$l"; done
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
# shellcheck disable=SC2086
apt-get install -y -qq --no-install-recommends $pkgs >/dev/null

if ! id shoebox >/dev/null 2>&1; then
  say "Creating the user shoebox"
  # uid 1000 when free: in an unprivileged Proxmox container that is 101000 on the host
  if getent passwd 1000 >/dev/null; then
    useradd --system --home-dir "$DATA" --shell /usr/sbin/nologin shoebox
  else
    useradd --uid 1000 --home-dir "$DATA" --shell /usr/sbin/nologin --user-group shoebox
  fi
fi

mkdir -p "$DATA" "$ARCHIVE"
chown shoebox: "$DATA"
chmod 750 "$DATA"
# the archive may be a network share: only fix the top folder, never recurse
if [ "$(stat -c %U "$ARCHIVE")" = "root" ]; then chown shoebox: "$ARCHIVE" 2>/dev/null || true; fi
if ! su -s /bin/sh shoebox -c "touch '$ARCHIVE/.shoebox-test' && rm -f '$ARCHIVE/.shoebox-test'" 2>/dev/null; then
  printf '\033[33mWarning:\033[0m the user shoebox (uid %s) cannot write to %s yet.\n' "$(id -u shoebox)" "$ARCHIVE"
  echo "         Give it write access (on a NAS share: for the host user 101000 in an unprivileged LXC)."
fi

if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/$REPO/releases/latest/download/shoebox-linux-$ARCH"
else
  URL="https://github.com/$REPO/releases/download/$VERSION/shoebox-linux-$ARCH"
fi
say "Downloading Shoebox ($VERSION, $ARCH)"
tmp="$(mktemp)"
curl -fsSL --retry 3 -o "$tmp" "$URL" || die "Download failed: $URL"
install -m 755 "$tmp" /usr/local/bin/shoebox
rm -f "$tmp"

if [ ! -f /etc/shoebox.env ]; then
  cat > /etc/shoebox.env <<EOF
# Shoebox settings. After changes: systemctl restart shoebox
SB_LISTEN=:$PORT
SB_DATA=$DATA
SB_ARCHIVE=$ARCHIVE
# Behind a reverse proxy (Caddy, nginx …):
#SB_TRUST_PROXY=true
# Serve HTTPS directly (the phone camera needs HTTPS; without it, photos still work):
#SB_TLS_CERT=/etc/shoebox/cert.pem
#SB_TLS_KEY=/etc/shoebox/key.pem
EOF
fi

cat > /etc/systemd/system/shoebox.service <<'EOF'
[Unit]
Description=Shoebox document archive
After=network-online.target remote-fs.target
Wants=network-online.target

[Service]
User=shoebox
EnvironmentFile=/etc/shoebox.env
ExecStart=/usr/local/bin/shoebox
Restart=on-failure
RestartSec=5
NoNewPrivileges=yes

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable shoebox >/dev/null 2>&1
started="$(date '+%Y-%m-%d %H:%M:%S')"
systemctl restart shoebox

say "Waiting for Shoebox to start"
for _ in $(seq 1 20); do
  if curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 || die "Shoebox didn't start. See: journalctl -u shoebox"

ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
code="$(journalctl -u shoebox --since "$started" --no-pager 2>/dev/null | grep -o 'setup code: [A-F0-9]*' | tail -n1 | awk '{print $3}' || true)"
echo
say "Shoebox $(/usr/local/bin/shoebox version) is running: http://${ip:-this-machine}:$PORT"
if [ -n "$code" ]; then
  echo "    Setup code for the first account: $code"
fi
echo "    Archive folder: $ARCHIVE"
echo "    Settings: /etc/shoebox.env · Logs: journalctl -u shoebox"

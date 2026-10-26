#!/usr/bin/env bash
# Creates a new Proxmox LXC container with Shoebox. Run on the Proxmox host as root:
#
#   bash -c "$(curl -fsSL https://raw.githubusercontent.com/lcsfls/shoebox/main/deploy/proxmox/shoebox-lxc.sh)"
#
# It asks a few questions (container id, disk, memory, network) and where the
# archive should live:
#   1. a folder on the Proxmox host, e.g. a NAS share you mounted under /mnt/pve
#   2. a new volume on one of your Proxmox storages
#   3. inside the container's own disk
# The container is unprivileged; Shoebox runs as an ordinary user inside it.
set -euo pipefail

RAW="https://raw.githubusercontent.com/lcsfls/shoebox/main"
MP="/srv/shoebox/archive"

say() { printf '\033[1m==>\033[0m %s\n' "$*"; }
die() { printf '\033[31mError:\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "Run this as root on the Proxmox host."
command -v pct >/dev/null && command -v pvesm >/dev/null || die "This script runs on a Proxmox VE host."
command -v whiptail >/dev/null || die "whiptail is missing (apt install whiptail)."

TITLE="Shoebox · new LXC container"
ask() { whiptail --title "$TITLE" --inputbox "$1" 10 72 "$2" 3>&1 1>&2 2>&3 || exit 1; }
menu() {
  local text="$1"; shift
  whiptail --title "$TITLE" --menu "$text" 20 78 10 "$@" 3>&1 1>&2 2>&3 || exit 1
}
yesno() { whiptail --title "$TITLE" --yesno "$1" 12 72; }

# storages that can hold a given content type, as menu items "name" "type, free"
storages() {
  pvesm status -content "$1" 2>/dev/null | awk 'NR>1 && $3=="active" {printf "%s\n%s, %.0f GB free\n", $1, $2, $6/1024/1024}'
}
pick_storage() {
  local content="$1" text="$2" items=()
  mapfile -t items < <(storages "$content")
  [ "${#items[@]}" -gt 0 ] || die "No active storage for $content found."
  if [ "${#items[@]}" -eq 2 ]; then echo "${items[0]}"; return; fi
  menu "$text" "${items[@]}"
}

# --- questions ---
CTID="$(ask "Container ID" "$(pvesh get /cluster/nextid)")"
[[ "$CTID" =~ ^[0-9]+$ ]] || die "The container ID must be a number."
pct status "$CTID" >/dev/null 2>&1 && die "Container $CTID already exists."
HOST="$(ask "Hostname" "shoebox")"
CORES="$(ask "CPU cores (OCR is faster with 2 or more)" "2")"
RAM="$(ask "Memory in MB" "1024")"
DISK="$(ask "Container disk in GB (system, database, caches)" "8")"
ROOTFS_STORAGE="$(pick_storage rootdir "Where should the container disk live?")"
BRIDGE="$(ask "Network bridge" "vmbr0")"
IPCFG="$(ask "IPv4: dhcp, or a static address like 192.168.1.50/24" "dhcp")"
GW=""
if [ "$IPCFG" != "dhcp" ]; then
  GW="$(ask "Gateway" "$(ip route | awk '/default/ {print $3; exit}')")"
fi

ARCHIVE_CHOICE="$(menu "Where should the archive (your PDFs) live?" \
  host "A folder on this host, e.g. a NAS share under /mnt/pve" \
  volume "A new volume on a Proxmox storage" \
  inside "Inside the container disk")"

MP0=""
HOSTDIR=""
case "$ARCHIVE_CHOICE" in
  host)
    guess="$(find /mnt/pve -mindepth 1 -maxdepth 1 -type d 2>/dev/null | head -n1)"
    HOSTDIR="$(ask "Folder on the Proxmox host. NFS: add the share under Datacenter → Storage first; it is mounted at /mnt/pve/<name>. A subfolder is fine." "${guess:-/mnt/pve/nas/documents}")"
    [ -d "$HOSTDIR" ] || die "$HOSTDIR does not exist on this host."
    MP0="$HOSTDIR,mp=$MP"
    ;;
  volume)
    VOL_STORAGE="$(pick_storage rootdir "Storage for the archive volume")"
    VOL_SIZE="$(ask "Size of the archive volume in GB" "32")"
    MP0="$VOL_STORAGE:$VOL_SIZE,mp=$MP,backup=1"
    ;;
  inside) ;;
esac

# --- template ---
say "Looking for a Debian template"
pveam update >/dev/null 2>&1 || true
TEMPLATE_NAME="$(pveam available --section system 2>/dev/null | awk '{print $2}' | grep -E '^debian-1[3]-standard' | sort -V | tail -n1)"
[ -n "$TEMPLATE_NAME" ] || TEMPLATE_NAME="$(pveam available --section system 2>/dev/null | awk '{print $2}' | grep -E '^debian-12-standard' | sort -V | tail -n1)"
[ -n "$TEMPLATE_NAME" ] || die "No Debian template found (pveam available)."
TEMPLATE_STORAGE="$(pick_storage vztmpl "Where should the container template be stored?")"
if ! pveam list "$TEMPLATE_STORAGE" 2>/dev/null | grep -q "$TEMPLATE_NAME"; then
  say "Downloading $TEMPLATE_NAME"
  pveam download "$TEMPLATE_STORAGE" "$TEMPLATE_NAME" >/dev/null
fi

NET="name=eth0,bridge=$BRIDGE,ip=$IPCFG"
[ -n "$GW" ] && NET="$NET,gw=$GW"

SUMMARY="Container $CTID ($HOST): $CORES cores, $RAM MB, ${DISK} GB on $ROOTFS_STORAGE
Network: $BRIDGE, $IPCFG
Archive: $( [ -n "$MP0" ] && echo "$MP0" || echo "inside the container ($MP)")
Template: $TEMPLATE_NAME"
yesno "Create this container?\n\n$SUMMARY" || exit 1

# --- create ---
say "Creating container $CTID"
args=(
  --hostname "$HOST" --cores "$CORES" --memory "$RAM" --swap 512
  --rootfs "$ROOTFS_STORAGE:$DISK" --net0 "$NET"
  --unprivileged 1 --features nesting=1 --onboot 1 --ostype debian --tags shoebox
)
[ -n "$MP0" ] && args+=(--mp0 "$MP0")
pct create "$CTID" "$TEMPLATE_STORAGE:vztmpl/$TEMPLATE_NAME" "${args[@]}" >/dev/null

if [ -n "$HOSTDIR" ]; then
  fstype="$(stat -f -c %T "$HOSTDIR" 2>/dev/null || echo unknown)"
  if [[ "$fstype" != nfs* && "$fstype" != cifs* && "$fstype" != smb* ]]; then
    if yesno "Let Shoebox write to $HOSTDIR?\n\nIn the unprivileged container Shoebox writes as user 101000 on this host. Set that user as the owner of the folder (only the folder itself, not its contents)?"; then
      chown 101000:101000 "$HOSTDIR"
    fi
  else
    whiptail --title "$TITLE" --msgbox "$HOSTDIR is a network share ($fstype).\n\nShoebox writes to it as user 101000 of this host. Make sure the NAS lets that user write, for example by mapping all users of this host to one account (all-squash) for the share." 14 72
  fi
fi

say "Starting the container"
pct start "$CTID"
for _ in $(seq 1 30); do
  if pct exec "$CTID" -- getent hosts github.com >/dev/null 2>&1; then break; fi
  sleep 2
done
pct exec "$CTID" -- getent hosts github.com >/dev/null 2>&1 || die "The container has no internet connection. Check the network settings."

say "Installing Shoebox inside the container"
tmp="$(mktemp)"
curl -fsSL "$RAW/deploy/install.sh" -o "$tmp" || die "Could not download the installer."
pct push "$CTID" "$tmp" /root/shoebox-install.sh
rm -f "$tmp"
pct exec "$CTID" -- env SHOEBOX_ARCHIVE="$MP" bash /root/shoebox-install.sh

echo
say "Done. Container $CTID is running Shoebox."
echo "    Update later with: pct exec $CTID -- bash /root/shoebox-install.sh"

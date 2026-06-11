#!/bin/sh
# One-click installer for lists-tg on OpenWrt 24.10+
#
# Usage:
#   sh <(wget -O - https://raw.githubusercontent.com/bpGusar/signbox-custom-lists-tgbot/main/install.sh)
#   ./install.sh
#
# Environment:
#   LISTS_TG_INSTALL_LUCI=0 — skip LuCI package
#   LISTS_TG_TOKEN          — set bot token in UCI after install
#   LISTS_TG_REPO_URL       — fallback: base URL for dist/ipk (raw GitHub)

set -eu

REPO_API="https://api.github.com/repos/bpGusar/signbox-custom-lists-tgbot/releases/latest"
REPO_OWNER="bpGusar"
REPO_NAME="signbox-custom-lists-tgbot"
REPO_BRANCH="${LISTS_TG_REPO_BRANCH:-main}"
FALLBACK_REPO_URL="https://raw.githubusercontent.com/${REPO_OWNER}/${REPO_NAME}/${REPO_BRANCH}/dist/ipk"
DOWNLOAD_DIR="/tmp/lists-tg-install"
INSTALL_LUCI="${LISTS_TG_INSTALL_LUCI:-1}"
RETRIES=3

log() { printf '\033[32;1m%s\033[0m\n' "$*" >&2; }
die() { printf '\033[31;1mERROR: %s\033[0m\n' "$*" >&2; exit 1; }

need_root() {
	[ "$(id -u)" -eq 0 ] || die "run as root"
}

need_openwrt() {
	command -v opkg >/dev/null 2>&1 || die "opkg not found — this script is for OpenWrt"
	[ -f /etc/openwrt_release ] || die "unsupported system"
}

detect_arch() {
	# shellcheck disable=SC1091
	. /etc/openwrt_release
	[ -n "${DISTRIB_ARCH:-}" ] || die "cannot detect DISTRIB_ARCH"
	printf '%s' "$DISTRIB_ARCH"
}

script_dir() {
	local src="$0"
	case "$src" in
		/*) dirname "$src" ;;
		*)  cd "$(dirname "$src")" && pwd ;;
	esac
}

download_file() {
	local url="$1"
	local dest="$2"
	local attempt=0

	while [ "$attempt" -lt "$RETRIES" ]; do
		log "download $(basename "$dest") (attempt $((attempt + 1))/$RETRIES)"
		if wget -q -O "$dest" "$url" && [ -s "$dest" ]; then
			return 0
		fi
		rm -f "$dest"
		attempt=$((attempt + 1))
	done
	return 1
}

fetch_from_release() {
	local arch="$1"
	local api_response url filename

	if command -v curl >/dev/null 2>&1; then
		api_response="$(curl -fsSL "$REPO_API" 2>/dev/null || true)"
		if printf '%s' "$api_response" | grep -q 'API rate limit'; then
			die "GitHub API rate limit — retry in a few minutes"
		fi
	elif command -v wget >/dev/null 2>&1; then
		api_response="$(wget -qO- "$REPO_API" 2>/dev/null || true)"
	else
		return 1
	fi

	[ -n "$api_response" ] || return 1

	printf '%s' "$api_response" | grep -o 'https://[^"[:space:]]*\.ipk' | while read -r url; do
		filename="$(basename "$url")"
		case "$filename" in
			lists-tg_*_"${arch}".ipk|luci-app-lists-tg_*_all.ipk)
				download_file "$url" "${DOWNLOAD_DIR}/${filename}" || return 1
				;;
		esac
	done

	[ -f "${DOWNLOAD_DIR}/lists-tg_"*_"${arch}.ipk" ] 2>/dev/null || return 1
	return 0
}

fetch_from_local() {
	local arch="$1"
	local base local_dir

	base="$(script_dir)"
	local_dir="${base}/dist/ipk/${arch}"
	[ -d "$local_dir" ] || return 1

	log "using local packages from ${local_dir}"
	cp "${local_dir}"/lists-tg_*.ipk "$DOWNLOAD_DIR/" 2>/dev/null || return 1
	if [ "$INSTALL_LUCI" = "1" ]; then
		cp "${local_dir}"/luci-app-lists-tg_*.ipk "$DOWNLOAD_DIR/" 2>/dev/null || return 1
	fi
	return 0
}

fetch_from_raw() {
	local arch="$1"
	local base_url="${LISTS_TG_REPO_URL:-$FALLBACK_REPO_URL}"
	local lists_glob luci_glob f

	log "fallback: raw packages from ${base_url}/${arch}"
	for f in "${base_url}/${arch}"/lists-tg_*.ipk; do
		[ -f "$f" ] || continue
		wget -q -O "${DOWNLOAD_DIR}/$(basename "$f")" "$f" || return 1
		break
	done
	[ -n "$(ls "${DOWNLOAD_DIR}"/lists-tg_*.ipk 2>/dev/null)" ] || return 1

	if [ "$INSTALL_LUCI" = "1" ]; then
		for f in "${base_url}/${arch}"/luci-app-lists-tg_*.ipk; do
			[ -f "$f" ] || continue
			wget -q -O "${DOWNLOAD_DIR}/$(basename "$f")" "$f" || return 1
			break
		done
	fi
	return 0
}

fetch_packages() {
	local arch="$1"

	rm -rf "$DOWNLOAD_DIR"
	mkdir -p "$DOWNLOAD_DIR"

	if fetch_from_local "$arch"; then
		return 0
	fi
	if fetch_from_release "$arch"; then
		return 0
	fi
	if fetch_from_raw "$arch"; then
		return 0
	fi

	die "no packages found for ${arch} — create a GitHub Release or build with: make ipk"
}

install_packages() {
	local arch="$1"

	log "updating package lists"
	opkg update >/dev/null 2>&1 || true
	opkg install ca-bundle >/dev/null 2>&1 || true

	if [ "$INSTALL_LUCI" = "1" ]; then
		opkg install luci-base luci-compat >/dev/null 2>&1 || true
	fi

	log "installing lists-tg (${arch})"
	# shellcheck disable=SC2046
	opkg install --force-reinstall $(ls "${DOWNLOAD_DIR}"/lists-tg_*.ipk)

	if [ "$INSTALL_LUCI" = "1" ]; then
		log "installing LuCI app"
		# shellcheck disable=SC2046
		opkg install --force-reinstall $(ls "${DOWNLOAD_DIR}"/luci-app-lists-tg_*.ipk)
		rm -f /tmp/luci-indexcache /tmp/luci-modulecache 2>/dev/null || true
	fi
}

configure_token() {
	if [ -n "${LISTS_TG_TOKEN:-}" ]; then
		log "setting bot token in UCI"
		uci set "lists-tg.main.token=${LISTS_TG_TOKEN}"
		uci commit lists-tg
	fi
}

enable_service() {
	log "enabling lists-tg service"
	/etc/init.d/lists-tg enable
	/etc/init.d/lists-tg restart || /etc/init.d/lists-tg start || true
}

print_done() {
	cat <<EOF

lists-tg installed successfully.

Next steps:
  1. LuCI: Services → Lists Telegram Bot
  2. Paste Telegram bot token and apply
  3. Send /start to your bot

EOF
}

main() {
	local arch

	need_root
	need_openwrt
	arch="$(detect_arch)"
	log "router arch: ${arch}"

	fetch_packages "$arch"
	install_packages "$arch"
	configure_token
	enable_service
	print_done

	rm -rf "$DOWNLOAD_DIR"
}

main "$@"

#!/bin/sh
# One-click installer for lst-signbox-lists-tgbot on OpenWrt 24.10+
#
# Usage:
#   sh <(wget -O - https://raw.githubusercontent.com/bpGusar/signbox-custom-lists-tgbot/main/install.sh)
#   ./install.sh
#
# Environment:
#   LST_SIGNBOX_LISTS_TGBOT_INSTALL_LUCI=0 — skip LuCI package
#   LST_SIGNBOX_LISTS_TGBOT_TOKEN          — set bot token in UCI after install
#   LST_SIGNBOX_LISTS_TGBOT_REPO_URL       — fallback: base URL for dist/ipk (raw GitHub)

set -eu

REPO_API="https://api.github.com/repos/bpGusar/signbox-custom-lists-tgbot/releases/latest"
REPO_OWNER="bpGusar"
REPO_NAME="signbox-custom-lists-tgbot"
REPO_BRANCH="${LST_SIGNBOX_LISTS_TGBOT_REPO_BRANCH:-${LISTS_TG_REPO_BRANCH:-main}}"
FALLBACK_REPO_URL="https://raw.githubusercontent.com/${REPO_OWNER}/${REPO_NAME}/${REPO_BRANCH}/dist/ipk"
DOWNLOAD_DIR="/tmp/lst-signbox-lists-tgbot-install"
INSTALL_LUCI="${LST_SIGNBOX_LISTS_TGBOT_INSTALL_LUCI:-${LISTS_TG_INSTALL_LUCI:-1}}"
RETRIES=3
PREV_ENABLED=""
PREV_TOKEN=""
PREV_DOMAIN_LIST=""
PREV_IP_LIST=""
PREV_RESTART_CMD=""
PREV_SERVICE_LABEL=""
PREV_STATE_PATH=""

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

keep_latest_packages() {
	local latest_lists latest_luci f

	latest_lists="$(ls "${DOWNLOAD_DIR}"/lst-signbox-lists-tgbot_*.ipk 2>/dev/null | sort -V | tail -n 1)"
	[ -n "$latest_lists" ] || die "lst-signbox-lists-tgbot package not found in ${DOWNLOAD_DIR}"
	for f in "${DOWNLOAD_DIR}"/lst-signbox-lists-tgbot_*.ipk; do
		[ "$f" = "$latest_lists" ] || rm -f "$f"
	done

	if [ "$INSTALL_LUCI" = "1" ]; then
		latest_luci="$(ls "${DOWNLOAD_DIR}"/luci-app-lst-signbox-lists-tgbot_*.ipk 2>/dev/null | sort -V | tail -n 1)"
		[ -n "$latest_luci" ] || die "luci-app-lst-signbox-lists-tgbot package not found in ${DOWNLOAD_DIR}"
		for f in "${DOWNLOAD_DIR}"/luci-app-lst-signbox-lists-tgbot_*.ipk; do
			[ "$f" = "$latest_luci" ] || rm -f "$f"
		done
	fi
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
			lst-signbox-lists-tgbot_*_"${arch}".ipk|luci-app-lst-signbox-lists-tgbot_*_all.ipk)
				download_file "$url" "${DOWNLOAD_DIR}/${filename}" || return 1
				;;
		esac
	done

	[ -f "${DOWNLOAD_DIR}/lst-signbox-lists-tgbot_"*_"${arch}.ipk" ] 2>/dev/null || return 1
	return 0
}

fetch_from_local() {
	local arch="$1"
	local base local_dir

	base="$(script_dir)"
	local_dir="${base}/dist/ipk/${arch}"
	[ -d "$local_dir" ] || return 1

	log "using local packages from ${local_dir}"
	cp "${local_dir}"/lst-signbox-lists-tgbot_*.ipk "$DOWNLOAD_DIR/" 2>/dev/null || return 1
	if [ "$INSTALL_LUCI" = "1" ]; then
		cp "${local_dir}"/luci-app-lst-signbox-lists-tgbot_*.ipk "$DOWNLOAD_DIR/" 2>/dev/null || return 1
	fi
	return 0
}

fetch_from_raw() {
	local arch="$1"
	local base_url="${LST_SIGNBOX_LISTS_TGBOT_REPO_URL:-${LISTS_TG_REPO_URL:-$FALLBACK_REPO_URL}}"
	local lists_glob luci_glob f

	log "fallback: raw packages from ${base_url}/${arch}"
	for f in "${base_url}/${arch}"/lst-signbox-lists-tgbot_*.ipk; do
		[ -f "$f" ] || continue
		wget -q -O "${DOWNLOAD_DIR}/$(basename "$f")" "$f" || return 1
		break
	done
	[ -n "$(ls "${DOWNLOAD_DIR}"/lst-signbox-lists-tgbot_*.ipk 2>/dev/null)" ] || return 1

	if [ "$INSTALL_LUCI" = "1" ]; then
		for f in "${base_url}/${arch}"/luci-app-lst-signbox-lists-tgbot_*.ipk; do
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
		keep_latest_packages
		return 0
	fi

	die "no packages found for ${arch} — create a GitHub Release or build with: make ipk"
}

backup_current_config() {
	PREV_ENABLED="$(uci -q get lst-signbox-lists-tgbot.main.enabled || uci -q get lists-tg.main.enabled || true)"
	PREV_TOKEN="$(uci -q get lst-signbox-lists-tgbot.main.token || uci -q get lists-tg.main.token || true)"
	PREV_DOMAIN_LIST="$(uci -q get lst-signbox-lists-tgbot.main.domain_list || uci -q get lists-tg.main.domain_list || true)"
	PREV_IP_LIST="$(uci -q get lst-signbox-lists-tgbot.main.ip_list || uci -q get lists-tg.main.ip_list || true)"
	PREV_RESTART_CMD="$(uci -q get lst-signbox-lists-tgbot.main.restart_cmd || uci -q get lists-tg.main.restart_cmd || true)"
	PREV_SERVICE_LABEL="$(uci -q get lst-signbox-lists-tgbot.main.service_label || uci -q get lists-tg.main.service_label || true)"
	PREV_STATE_PATH="$(uci -q get lst-signbox-lists-tgbot.main.state_path || uci -q get lists-tg.main.state_path || true)"
}

install_packages() {
	local arch="$1" lists_ipk luci_ipk

	log "updating package lists"
	opkg update >/dev/null 2>&1 || true
	opkg install ca-bundle >/dev/null 2>&1 || true

	if [ "$INSTALL_LUCI" = "1" ]; then
		opkg install luci-base luci-compat >/dev/null 2>&1 || true
	fi

	log "installing lst-signbox-lists-tgbot (${arch})"
	lists_ipk="$(ls "${DOWNLOAD_DIR}"/lst-signbox-lists-tgbot_*.ipk 2>/dev/null | sort -V | tail -n 1)"
	[ -n "$lists_ipk" ] || die "lst-signbox-lists-tgbot package not found"
	opkg install --force-reinstall --force-downgrade "$lists_ipk"

	if [ "$INSTALL_LUCI" = "1" ]; then
		log "installing LuCI app"
		luci_ipk="$(ls "${DOWNLOAD_DIR}"/luci-app-lst-signbox-lists-tgbot_*.ipk 2>/dev/null | sort -V | tail -n 1)"
		[ -n "$luci_ipk" ] || die "luci-app-lst-signbox-lists-tgbot package not found"
		opkg install --force-reinstall --force-downgrade "$luci_ipk"
		rm -f /tmp/luci-indexcache /tmp/luci-modulecache 2>/dev/null || true
	fi
}

configure_token() {
	local changed=0

	if [ -n "$PREV_ENABLED" ]; then
		uci set "lst-signbox-lists-tgbot.main.enabled=${PREV_ENABLED}"
		changed=1
	fi

	if [ -n "${LST_SIGNBOX_LISTS_TGBOT_TOKEN:-${LISTS_TG_TOKEN:-}}" ]; then
		log "setting bot token in UCI from env"
		uci set "lst-signbox-lists-tgbot.main.token=${LST_SIGNBOX_LISTS_TGBOT_TOKEN:-${LISTS_TG_TOKEN:-}}"
		changed=1
	elif [ -n "$PREV_TOKEN" ]; then
		log "restoring bot token from previous config"
		uci set "lst-signbox-lists-tgbot.main.token=${PREV_TOKEN}"
		changed=1
	fi

	if [ -n "$PREV_DOMAIN_LIST" ]; then
		uci set "lst-signbox-lists-tgbot.main.domain_list=${PREV_DOMAIN_LIST}"
		changed=1
	fi
	if [ -n "$PREV_IP_LIST" ]; then
		uci set "lst-signbox-lists-tgbot.main.ip_list=${PREV_IP_LIST}"
		changed=1
	fi
	if [ -n "$PREV_RESTART_CMD" ]; then
		uci set "lst-signbox-lists-tgbot.main.restart_cmd=${PREV_RESTART_CMD}"
		changed=1
	fi
	if [ -n "$PREV_SERVICE_LABEL" ]; then
		uci set "lst-signbox-lists-tgbot.main.service_label=${PREV_SERVICE_LABEL}"
		changed=1
	fi
	if [ -n "$PREV_STATE_PATH" ]; then
		uci set "lst-signbox-lists-tgbot.main.state_path=${PREV_STATE_PATH}"
		changed=1
	fi

	if [ "$changed" -eq 1 ]; then
		uci commit lst-signbox-lists-tgbot
	fi
}

enable_service() {
	log "enabling lst-signbox-lists-tgbot service"
	/etc/init.d/lst-signbox-lists-tgbot enable
	/etc/init.d/lst-signbox-lists-tgbot restart || /etc/init.d/lst-signbox-lists-tgbot start || true
}

print_done() {
	cat <<EOF

lst-signbox-lists-tgbot installed successfully.

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

	backup_current_config
	fetch_packages "$arch"
	keep_latest_packages
	install_packages "$arch"
	configure_token
	enable_service
	print_done

	rm -rf "$DOWNLOAD_DIR"
}

main "$@"

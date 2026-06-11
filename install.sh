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
DIST_RAW_URL="https://raw.githubusercontent.com/${REPO_OWNER}/${REPO_NAME}/${REPO_BRANCH}/dist"
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

manifest_package_name() {
	local manifest="$1"
	local package="$2"
	local arch="$3"
	local template

	template="$(grep -F "\"${package}\"" "$manifest" | sed -n 's/.*: *"\([^"]*\)".*/\1/p' | head -n 1)"
	[ -n "$template" ] || return 1
	printf '%s' "$template" | sed "s/\${ARCH}/${arch}/g"
}

resolve_package_names() {
	local arch="$1"
	local manifest="${DOWNLOAD_DIR}/manifest.json"
	local lists_pkg luci_pkg

	lists_pkg="$(manifest_package_name "$manifest" "lst-signbox-lists-tgbot" "$arch")"
	[ -n "$lists_pkg" ] || return 1

	if [ "$INSTALL_LUCI" = "1" ]; then
		luci_pkg="$(manifest_package_name "$manifest" "luci-app-lst-signbox-lists-tgbot" "$arch")"
		[ -n "$luci_pkg" ] || return 1
	fi

	PACKAGE_LISTS="$lists_pkg"
	PACKAGE_LUCI="$luci_pkg"
	return 0
}

fetch_manifest() {
	local dest="${DOWNLOAD_DIR}/manifest.json"
	local base_url="${LST_SIGNBOX_LISTS_TGBOT_REPO_URL:-${LISTS_TG_REPO_URL:-$DIST_RAW_URL}}"
	local manifest_url

	case "$base_url" in
		*/dist/ipk) manifest_url="${base_url%/ipk}/manifest.json" ;;
		*/dist)     manifest_url="${base_url}/manifest.json" ;;
		*)          manifest_url="${DIST_RAW_URL}/manifest.json" ;;
	esac

	download_file "$manifest_url" "$dest" || return 1
	resolve_package_names "$1" || return 1
	return 0
}

release_asset_url() {
	local api_response="$1"
	local filename="$2"

	printf '%s' "$api_response" | grep -F "$filename" | grep -o 'https://[^"[:space:]]*' | head -n 1
}

release_package_names() {
	local arch="$1"
	local api_response="$2"
	local release_ver

	release_ver="$(printf '%s' "$api_response" | grep -o '"tag_name"[[:space:]]*:[[:space:]]*"v[^"]*"' | head -n 1 | sed 's/.*"v\([^"]*\)".*/\1/')"
	[ -n "$release_ver" ] || return 1

	PACKAGE_LISTS="lst-signbox-lists-tgbot_${release_ver}-r1_${arch}.ipk"
	if [ "$INSTALL_LUCI" = "1" ]; then
		PACKAGE_LUCI="luci-app-lst-signbox-lists-tgbot_${release_ver}-r1_all.ipk"
	fi
	return 0
}

fetch_from_release() {
	local arch="$1"
	local api_response url

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
	release_package_names "$arch" "$api_response" || return 1

	url="$(release_asset_url "$api_response" "$PACKAGE_LISTS")"
	[ -n "$url" ] || return 1
	download_file "$url" "${DOWNLOAD_DIR}/${PACKAGE_LISTS}" || return 1

	if [ "$INSTALL_LUCI" = "1" ]; then
		url="$(release_asset_url "$api_response" "$PACKAGE_LUCI")"
		[ -n "$url" ] || return 1
		download_file "$url" "${DOWNLOAD_DIR}/${PACKAGE_LUCI}" || return 1
	fi

	return 0
}

fetch_from_local() {
	local arch="$1"
	local base local_dir manifest_src

	base="$(script_dir)"
	local_dir="${base}/dist/ipk/${arch}"
	manifest_src="${base}/dist/manifest.json"
	[ -d "$local_dir" ] || return 1
	[ -f "$manifest_src" ] || return 1

	log "using local packages from ${local_dir}"
	cp "$manifest_src" "${DOWNLOAD_DIR}/manifest.json"
	resolve_package_names "$arch" || return 1

	cp "${local_dir}/${PACKAGE_LISTS}" "${DOWNLOAD_DIR}/${PACKAGE_LISTS}" 2>/dev/null || return 1
	if [ "$INSTALL_LUCI" = "1" ]; then
		cp "${local_dir}/${PACKAGE_LUCI}" "${DOWNLOAD_DIR}/${PACKAGE_LUCI}" 2>/dev/null || return 1
	fi
	return 0
}

fetch_from_raw() {
	local arch="$1"
	local base_url="${LST_SIGNBOX_LISTS_TGBOT_REPO_URL:-${LISTS_TG_REPO_URL:-$FALLBACK_REPO_URL}}"

	log "fallback: raw packages from ${base_url}/${arch}"
	fetch_manifest "$arch" || return 1

	download_file "${base_url}/${arch}/${PACKAGE_LISTS}" "${DOWNLOAD_DIR}/${PACKAGE_LISTS}" || return 1
	if [ "$INSTALL_LUCI" = "1" ]; then
		download_file "${base_url}/${arch}/${PACKAGE_LUCI}" "${DOWNLOAD_DIR}/${PACKAGE_LUCI}" || return 1
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
	lists_ipk="${DOWNLOAD_DIR}/${PACKAGE_LISTS}"
	[ -f "$lists_ipk" ] || die "lst-signbox-lists-tgbot package not found"
	opkg install --force-reinstall --force-downgrade "$lists_ipk"

	if [ "$INSTALL_LUCI" = "1" ]; then
		log "installing LuCI app"
		luci_ipk="${DOWNLOAD_DIR}/${PACKAGE_LUCI}"
		[ -f "$luci_ipk" ] || die "luci-app-lst-signbox-lists-tgbot package not found"
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
	install_packages "$arch"
	configure_token
	enable_service
	print_done

	rm -rf "$DOWNLOAD_DIR"
}

main "$@"

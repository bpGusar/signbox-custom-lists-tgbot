#!/usr/bin/env bash
# Build packages via Dockerfile-ipk. The SDK decides the format: OpenWrt 24.10
# makes .ipk, 25.12 makes .apk.
# Result: dist/ipk/<arch>/*.ipk or dist/apk/<arch>/*.apk
#
# Usage:
#   ./scripts/build-ipk.sh
#   OPENWRT_VERSION=25.12.3 ./scripts/build-ipk.sh
#   TARGETS="aarch64_cortex-a53" ./scripts/build-ipk.sh

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OPENWRT_VERSION="${OPENWRT_VERSION:-24.10.5}"
TARGETS="${TARGETS:-aarch64_cortex-a53 arm_cortex-a7_neon-vfpv4 mipsel_24kc x86_64}"
DOCKER_PLATFORM="${DOCKER_PLATFORM:-linux/amd64}"
LISTS_TG_VERSION="${LISTS_TG_VERSION:-$("${ROOT}/scripts/version.sh")}"
LISTS_TG_RELEASE="${LISTS_TG_RELEASE:-1}"

log() { printf '==> %s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

command -v docker >/dev/null 2>&1 || die "docker is required"

update_manifest() {
	local manifest="${ROOT}/dist/manifest.json"
	[ -f "$manifest" ] || return 0
	python3 "${ROOT}/scripts/update-manifest.py" "$LISTS_TG_VERSION" "$LISTS_TG_RELEASE" "$manifest" 2>/dev/null || true
}

build_arch() {
	local arch="$1"
	local image="lst-signbox-lists-tgbot-ipk:${arch}"
	local full="${LISTS_TG_VERSION}-r${LISTS_TG_RELEASE}"
	local container tmp out

	log "building for ${arch} (version ${full})"
	tmp="$(mktemp -d)"

	docker build --platform "$DOCKER_PLATFORM" -f "${ROOT}/Dockerfile-ipk" \
		--build-arg "ARCH=${arch}" \
		--build-arg "OPENWRT_VERSION=${OPENWRT_VERSION}" \
		--build-arg "LISTS_TG_VERSION=${LISTS_TG_VERSION}" \
		--build-arg "LISTS_TG_RELEASE=${LISTS_TG_RELEASE}" \
		-t "$image" \
		"$ROOT"

	container="$(docker create "$image")"
	cleanup() {
		[ -n "${container:-}" ] && docker rm -f "$container" >/dev/null 2>&1 || true
		[ -n "${tmp:-}" ] && rm -rf "$tmp"
	}
	trap cleanup EXIT

	docker cp "${container}:/builder/bin/packages/." "$tmp/"
	docker rm -f "$container" >/dev/null
	container=""

	if [ -n "$(find "$tmp" -name 'lst-signbox-lists-tgbot_*.ipk' -print -quit)" ]; then
		out="${ROOT}/dist/ipk/${arch}"
		mkdir -p "$out"
		find "$tmp" -name 'lst-signbox-lists-tgbot_*.ipk' -exec cp -f {} "$out/" \;
		find "$tmp" -name 'luci-app-lst-signbox-lists-tgbot_*.ipk' -exec cp -f {} "$out/" \;
	else
		# apk names carry no architecture, so they are renamed the ipk way to
		# stay unique among release assets.
		out="${ROOT}/dist/apk/${arch}"
		mkdir -p "$out"
		cp -f "$(find "$tmp" -name "lst-signbox-lists-tgbot-${full}.apk" -print -quit)" \
			"${out}/lst-signbox-lists-tgbot_${full}_${arch}.apk" \
			|| die "lst-signbox-lists-tgbot apk not produced for ${arch}"
		cp -f "$(find "$tmp" -name "luci-app-lst-signbox-lists-tgbot-${full}.apk" -print -quit)" \
			"${out}/luci-app-lst-signbox-lists-tgbot_${full}_all.apk" \
			|| die "luci-app-lst-signbox-lists-tgbot apk not produced for ${arch}"
	fi

	[ -n "$(find "$out" -maxdepth 1 -name 'lst-signbox-lists-tgbot_*' -print -quit)" ] \
		|| die "lst-signbox-lists-tgbot package not produced for ${arch}"

	log "done: ${out}"
	ls -la "$out/"
}

main() {
	log "package version: ${LISTS_TG_VERSION}-r${LISTS_TG_RELEASE}"
	log "OpenWrt SDK version: ${OPENWRT_VERSION}"
	log "targets: ${TARGETS}"

	for arch in $TARGETS; do
		build_arch "$arch"
	done

	update_manifest

	log "built packages:"
	find "${ROOT}/dist" \( -name '*.ipk' -o -name '*.apk' \) | sort
}

main "$@"

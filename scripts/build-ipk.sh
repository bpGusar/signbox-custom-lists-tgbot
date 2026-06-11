#!/usr/bin/env bash
# Build .ipk packages via Dockerfile-ipk.
# Result: dist/ipk/<arch>/*.ipk
#
# Usage:
#   ./scripts/build-ipk.sh
#   TARGETS="aarch64_cortex-a53" ./scripts/build-ipk.sh

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OPENWRT_VERSION="${OPENWRT_VERSION:-24.10.5}"
TARGETS="${TARGETS:-aarch64_cortex-a53 arm_cortex-a7_neon-vfpv4 mipsel_24kc x86_64}"
DOCKER_PLATFORM="${DOCKER_PLATFORM:-linux/amd64}"

log() { printf '==> %s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

command -v docker >/dev/null 2>&1 || die "docker is required"

build_arch() {
	local arch="$1"
	local image="lists-tg-ipk:${arch}"
	local out="${ROOT}/dist/ipk/${arch}"
	local container tmp

	log "building for ${arch} (OpenWrt ${OPENWRT_VERSION})"
	mkdir -p "$out"
	tmp="$(mktemp -d)"

	docker build --platform "$DOCKER_PLATFORM" -f "${ROOT}/Dockerfile-ipk" \
		--build-arg "ARCH=${arch}" \
		--build-arg "OPENWRT_VERSION=${OPENWRT_VERSION}" \
		-t "$image" \
		"$ROOT"

	container="$(docker create "$image")"
	cleanup() {
		docker rm -f "$container" >/dev/null 2>&1 || true
		[ -n "${tmp:-}" ] && rm -rf "$tmp"
	}
	trap cleanup EXIT

	docker cp "${container}:/builder/bin/packages/." "$tmp/"
	docker rm -f "$container" >/dev/null
	container=""

	find "$tmp" -name 'lists-tg_*.ipk' -exec cp -f {} "$out/" \;
	find "$tmp" -name 'luci-app-lists-tg_*.ipk' -exec cp -f {} "$out/" \;

	[ -n "$(find "$out" -maxdepth 1 -name 'lists-tg_*.ipk' -print -quit)" ] \
		|| die "lists-tg ipk not produced for ${arch}"

	log "done: ${out}"
	ls -la "$out/"
}

main() {
	log "OpenWrt SDK version: ${OPENWRT_VERSION}"
	log "targets: ${TARGETS}"
	log "docker platform: ${DOCKER_PLATFORM}"

	for arch in $TARGETS; do
		build_arch "$arch"
	done

	log "built packages:"
	find "${ROOT}/dist/ipk" -name '*.ipk' | sort
	log "push a tag to publish GitHub Release, or commit dist/ipk/ for raw install"
}

main "$@"

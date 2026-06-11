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
LISTS_TG_VERSION="${LISTS_TG_VERSION:-$("${ROOT}/scripts/version.sh")}"
LISTS_TG_RELEASE="${LISTS_TG_RELEASE:-1}"

log() { printf '==> %s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

command -v docker >/dev/null 2>&1 || die "docker is required"

update_manifest() {
	local manifest="${ROOT}/dist/manifest.json"
	[ -f "$manifest" ] || return 0
	# shellcheck disable=SC2016
	python3 - "$LISTS_TG_VERSION" "$LISTS_TG_RELEASE" "$manifest" <<'PY' 2>/dev/null || true
import json, sys
version, release, path = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path) as f:
    data = json.load(f)
data["version"] = f"{version}-r{release}"
data["packages"]["lists-tg"] = f"lists-tg_{version}-r{release}_${{ARCH}}.ipk"
data["packages"]["luci-app-lists-tg"] = f"luci-app-lists-tg_{version}-r{release}_all.ipk"
with open(path, "w") as f:
    json.dump(data, f, indent=2)
    f.write("\n")
PY
}

build_arch() {
	local arch="$1"
	local image="lists-tg-ipk:${arch}"
	local out="${ROOT}/dist/ipk/${arch}"
	local container tmp

	log "building for ${arch} (version ${LISTS_TG_VERSION}-r${LISTS_TG_RELEASE})"
	mkdir -p "$out"
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
	log "package version: ${LISTS_TG_VERSION}-r${LISTS_TG_RELEASE}"
	log "OpenWrt SDK version: ${OPENWRT_VERSION}"
	log "targets: ${TARGETS}"

	for arch in $TARGETS; do
		build_arch "$arch"
	done

	update_manifest

	log "built packages:"
	find "${ROOT}/dist/ipk" -name '*.ipk' | sort
}

main "$@"

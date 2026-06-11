#!/usr/bin/env bash
# Print package version for IPK builds (stdout only).
# - exact git tag on HEAD  -> tag without "v" (e.g. 1.2.3)
# - otherwise             -> 0.YYYYMMDD.<build> (e.g. 0.20260611.42)
set -euo pipefail

if tag="$(git describe --tags --exact-match HEAD 2>/dev/null)"; then
	printf '%s' "${tag#v}"
	exit 0
fi

build_id="${GITHUB_RUN_NUMBER:-$(git rev-list --count HEAD 2>/dev/null || echo 0)}"
printf '0.%s.%s' "$(date -u +%Y%m%d)" "$build_id"

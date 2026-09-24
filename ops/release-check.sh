#!/usr/bin/env bash
# release-check validates that a version is ready to tag: semver-shaped,
# documented in CHANGELOG.md, and matching the AUR PKGBUILD's pkgver. It
# builds nothing and publishes nothing (see RELEASING.md). Invoked as
# `make release-check VERSION=vX.Y.Z`; CHANGELOG_FILE and PKGBUILD_FILE are
# overridable for tests.
set -euo pipefail

VERSION="${VERSION:-}"
CHANGELOG_FILE="${CHANGELOG_FILE:-CHANGELOG.md}"
PKGBUILD_FILE="${PKGBUILD_FILE:-ops/aur/PKGBUILD}"

if [[ -z "$VERSION" ]]; then
	echo "release-check: VERSION is required, e.g. make release-check VERSION=v1.2.3" >&2
	exit 1
fi

if [[ ! "$VERSION" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
	echo "release-check: VERSION '$VERSION' is not semver (want vMAJOR.MINOR.PATCH)" >&2
	exit 1
fi
BARE_VERSION="${VERSION#v}"

if [[ ! -f "$CHANGELOG_FILE" ]]; then
	echo "release-check: CHANGELOG file not found: $CHANGELOG_FILE" >&2
	exit 1
fi
if ! grep -qE "^## \[${BARE_VERSION//./\\.}\]" "$CHANGELOG_FILE"; then
	echo "release-check: $CHANGELOG_FILE has no '## [$BARE_VERSION]' section" >&2
	exit 1
fi

if [[ ! -f "$PKGBUILD_FILE" ]]; then
	echo "release-check: PKGBUILD not found: $PKGBUILD_FILE" >&2
	exit 1
fi
PKGVER="$(grep -E '^pkgver=' "$PKGBUILD_FILE" | head -n1 | cut -d= -f2)"
if [[ "$PKGVER" != "$BARE_VERSION" ]]; then
	echo "release-check: $PKGBUILD_FILE pkgver='$PKGVER' does not match VERSION '$BARE_VERSION'" >&2
	exit 1
fi

echo "release-check: $VERSION OK (CHANGELOG section present, pkgver matches)"

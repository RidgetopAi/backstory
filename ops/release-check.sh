#!/usr/bin/env bash
# release-check validates that a version is ready to tag: semver-shaped,
# documented in CHANGELOG.md, and matching the AUR PKGBUILD's pkgver, with a real sha256sums (never SKIP) that
the committed .SRCINFO agrees with. It
# builds nothing and publishes nothing (see RELEASING.md). Invoked as
# `make release-check VERSION=vX.Y.Z`; CHANGELOG_FILE and PKGBUILD_FILE are
# overridable for tests.
set -euo pipefail

VERSION="${VERSION:-}"
CHANGELOG_FILE="${CHANGELOG_FILE:-CHANGELOG.md}"
PKGBUILD_FILE="${PKGBUILD_FILE:-ops/aur/PKGBUILD}"
SRCINFO_FILE="${SRCINFO_FILE:-$(dirname "$PKGBUILD_FILE")/.SRCINFO}"

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

# sha256sums must be real 64-lowercase-hex digests: SKIP disables download
# verification, which AUR users read as a red flag on a tagged tarball.
SHA_LINE="$(grep -E '^sha256sums=' "$PKGBUILD_FILE" | head -n1 || true)"
if [[ -z "$SHA_LINE" ]]; then
	echo "release-check: $PKGBUILD_FILE has no sha256sums=(...) line" >&2
	exit 1
fi
mapfile -t PKG_SHAS < <(grep -oE "'[^']*'|\"[^\"]*\"" <<<"$SHA_LINE" | tr -d "'\"")
if [[ ${#PKG_SHAS[@]} -eq 0 ]]; then
	echo "release-check: $PKGBUILD_FILE sha256sums has no entries" >&2
	exit 1
fi
for sha in "${PKG_SHAS[@]}"; do
	if [[ ! "$sha" =~ ^[0-9a-f]{64}$ ]]; then
		echo "release-check: $PKGBUILD_FILE sha256sums entry '$sha' is not 64 lowercase hex (SKIP is refused)" >&2
		exit 1
	fi
done

if [[ ! -f "$SRCINFO_FILE" ]]; then
	echo "release-check: .SRCINFO not found: $SRCINFO_FILE (regenerate with makepkg --printsrcinfo)" >&2
	exit 1
fi
SRCINFO_PKGVER="$(sed -nE 's/^[[:space:]]*pkgver = (.*)$/\1/p' "$SRCINFO_FILE" | head -n1)"
if [[ "$SRCINFO_PKGVER" != "$PKGVER" ]]; then
	echo "release-check: $SRCINFO_FILE pkgver='$SRCINFO_PKGVER' disagrees with PKGBUILD pkgver '$PKGVER'" >&2
	exit 1
fi
mapfile -t SRC_SHAS < <(sed -nE 's/^[[:space:]]*sha256sums = (.*)$/\1/p' "$SRCINFO_FILE")
if [[ "${SRC_SHAS[*]}" != "${PKG_SHAS[*]}" ]]; then
	echo "release-check: $SRCINFO_FILE sha256sums (${SRC_SHAS[*]}) disagree with PKGBUILD sha256sums (${PKG_SHAS[*]})" >&2
	exit 1
fi

echo "release-check: $VERSION OK (CHANGELOG section present, pkgver matches, sha256sums verified and agree with .SRCINFO)"

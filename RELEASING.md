# Releasing Backstory

Decision `262cf929`: v1 is tagged only once `PLAN.md` §Phase 4's done-when
holds on a box we do not own: a machine that is not Brian's. This document is the release
branch flow and its tag. It does not authorize anything past a local tag —
**AUR push and marketplace submission are CN3 and require Brian's explicit
approval**, decided separately from this document. Nothing in this repo, its
Makefile, or its CI pushes a package, uploads an artifact, or submits
anything on your behalf.

## Flow

1. Branch `release` from `main` at the commit that satisfies the Phase 4
   done-when (Q4 targets measured on a machine that is not Brian's: panel
   content under two minutes, a warm session under an hour).
2. Add a section to `CHANGELOG.md` for the version, moving the relevant
   `[Unreleased]` entries under it: `## [X.Y.Z] - YYYY-MM-DD`.
3. Bump `pkgver` in `ops/aur/PKGBUILD` to `X.Y.Z` (no leading `v`, per Arch
   packaging convention) and reset `pkgrel` to `1`.
4. Run `make release-check VERSION=vX.Y.Z`. It must exit 0: VERSION is
   semver, `CHANGELOG.md` has the `## [X.Y.Z]` section, and the PKGBUILD's
   `pkgver` matches. This step builds nothing and publishes nothing — it
   only checks that the three sources of truth (tag, changelog, package
   metadata) agree.
5. Run `make check` on the `release` branch head.
6. Commit the CHANGELOG and PKGBUILD bump, then tag: `git tag -a vX.Y.Z -m
   "vX.Y.Z"`.

Everything above happens locally or on the canonical clone. Nothing here
pushes the tag, pushes the branch, uploads a tarball, or opens a PR against
the AUR or the marketplace repo.

## What needs Brian's approval

The tag is not the release. Two steps are gated separately and are **not**
covered by this document or by `make release-check`:

- **AUR push** — publishing `ops/aur/PKGBUILD` (and its generated
  `.SRCINFO`) to the `aur.archlinux.org` `backstory` package.
- **Marketplace submission** — filing the Omarchy Install menu submission
  (ladder rung 1, decision `6caaac1d` Lock 10) against the marketplace repo.

Both require Brian's explicit sign-off before anyone runs them. Neither has
credentials or automation in this repo.

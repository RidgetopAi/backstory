# Backstory

Backstory is the OS memory daemon for [Omarchy](https://omarchy.org) (Arch + Hyprland): a `systemd --user` service, one static Go binary, SQLite/FTS5 store, unix socket + MCP stdio shim.
Status: **pre-alpha** (Phase 0 skeleton; no schema, no daemon yet).
Build and verify with `make check` (fmt + vet + lint + race tests); `make build` writes `bin/backstory`.
License: MIT.

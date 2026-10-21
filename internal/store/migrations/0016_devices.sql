-- 0016_devices.sql — device identity and the sync schema (PLAN.md Phase 8,
-- decision 2ac4c54f, task 4f00e324). No sync behaviour yet.
--   devices          one row per known device; Open mints the single is_self
--                    row (ensureSelfDevice), so the UUID is minted in Go.
--   origin_device    on records and sessions: NULL means this device. Existing
--                    rows stay NULL, nothing is rewritten. Set at INSERT time
--                    only; records_no_update (recreated by this migration's
--                    data hook) refuses any UPDATE of it.
--   sync_peers       unused until sync 4/8; created now so later punches add
--                    no migration of their own.
CREATE TABLE devices (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  is_self     INTEGER NOT NULL CHECK (is_self IN (0,1)),
  first_seen  INTEGER                   -- unix nanoseconds, UTC
);
CREATE UNIQUE INDEX devices_one_self ON devices(is_self) WHERE is_self = 1;

CREATE TABLE sync_peers (
  device_id  TEXT PRIMARY KEY REFERENCES devices(id),
  ssh_host   TEXT,
  enabled    INTEGER NOT NULL DEFAULT 1,
  paired_at  INTEGER                    -- unix nanoseconds, UTC
);

ALTER TABLE records  ADD COLUMN origin_device TEXT;
ALTER TABLE sessions ADD COLUMN origin_device TEXT;
DROP TRIGGER records_no_update;

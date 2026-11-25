package store

import (
	"errors"
	"fmt"
	"strconv"
)

// settingDismissedGitStatePrefix + a session.git_state event id is the
// settings key `backstory dismiss` writes; its value is that event's
// session id. Settings, not a record: the evidence is a timeline event,
// which never leaves this device (sync serves records, edges and session
// stubs only), so the item it backs exists only here and its dismissal
// does too. A record would sync to peers that have no such event. The
// session id is part of the key's meaning because an event id is a rowid
// that a purge of the newest events can hand out again; a reused id
// belongs to a different session, so it is never hidden by an old
// dismissal.
const settingDismissedGitStatePrefix = "attention_dismissed_git_state."

// ErrDismissHumanOnly is returned when a non-human identity tries to dismiss
// a Needs-you item: settings are a human-only power (AGENT-CONTRACT.md §The
// never-list, item 5) and no tool or socket method reaches this.
var ErrDismissHumanOnly = errors.New("store: only a human can dismiss a Needs-you item")

// DismissGitState hides the uncommitted-at-session-end item whose evidence
// is ev until a newer session.git_state event in the same tree replaces it
// (internal/week reads GitStateDismissed). The caller validates that ev is
// the item's current evidence; this only refuses a non-human identity and
// an event with no session.
func (s *Store) DismissGitState(identity Identity, ev TimelineEvent) error {
	if identity.Kind != IdentityHuman {
		return ErrDismissHumanOnly
	}
	if ev.SessionID == "" {
		return fmt.Errorf("store: dismiss event %d: it has no session", ev.ID)
	}
	return s.SetSetting(settingDismissedGitStatePrefix+strconv.FormatInt(ev.ID, 10), ev.SessionID)
}

// GitStateDismissed reports whether `backstory dismiss` hid ev: a dismissal
// keyed to ev's id AND written for ev's own session.
func (s *Store) GitStateDismissed(ev TimelineEvent) (bool, error) {
	sessionID, ok, err := s.GetSetting(settingDismissedGitStatePrefix + strconv.FormatInt(ev.ID, 10))
	if err != nil {
		return false, err
	}
	return ok && sessionID != "" && sessionID == ev.SessionID, nil
}

// GetEvent reads back one timeline event by id; found is false when no
// such event exists (never written, or purged).
func (s *Store) GetEvent(id int64) (ev TimelineEvent, found bool, err error) {
	rows, err := s.db.Query(`SELECT id, ts, kind, session_id, source, payload, workspace, window
		FROM timeline_events WHERE id = ?`, id)
	if err != nil {
		return TimelineEvent{}, false, fmt.Errorf("store: get event %d: %w", id, err)
	}
	events, err := s.scanTimelineEvents(rows)
	if err != nil {
		return TimelineEvent{}, false, fmt.Errorf("store: get event %d: %w", id, err)
	}
	if len(events) == 0 {
		return TimelineEvent{}, false, nil
	}
	return events[0], true, nil
}

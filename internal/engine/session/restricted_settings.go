package session

import "time"

// RefreshInvocationSettingsLocked updates only operation-local policy for
// subsequent nested work. It never changes the active statement context,
// deadline or real session lifetime timers. Caller holds the effective store
// or exclusively owns a freshly detached/checkpoint image.
func (op *Operation) RefreshInvocationSettingsLocked(store *SettingsStore) {
	op.LockTimeout = time.Duration(store.TypedValue("lock_timeout").Number) * time.Millisecond
	op.NoticeThreshold, _ = messageLevel(store.Value("client_min_messages"))
	op.NonstandardStrings = !store.TypedValue("standard_conforming_strings").Boolean
}

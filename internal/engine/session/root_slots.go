package session

// The slots below let the session-layer structs carry state that the root
// engine owns (execution runtime, feature state) without naming its types.
// They follow the journal slot rules: an exported marker method, one checked
// root helper per slot that recovers the concrete type, and never a typed
// nil, so a nil slot means "none" (or all zero).

// PayloadSlot holds a session's root payload: SQL cursors, the prepared SQL
// registry, retained SQL routine analyses, sequence session memory and the
// notification listener. The root allocates it with the session and sets it
// before the session is shared; it never changes afterwards.
type PayloadSlot interface {
	PayloadSlot()
}

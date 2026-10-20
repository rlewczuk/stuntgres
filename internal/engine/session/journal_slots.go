package session

// The slots below carry feature state in the catalog journal without naming
// the feature types. Each owner stores and recovers its value with one checked
// helper and never stores a nil pointer, so a nil slot means "none".
//
// Clone copies a slot for a new journal. The journal is copy-on-write and
// savepoint marks keep older journals, so Clone copies whatever the owner
// writes in place; it never returns nil for a non-nil slot.

// EnumSlot holds enum provenance (typedef).
type EnumSlot interface {
	EnumSlot()
	Clone() EnumSlot
}

// DomainSlot holds the committed-predecessor fences of ALTER DOMAIN
// (typedef).
type DomainSlot interface {
	DomainSlot()
	Clone() DomainSlot
}

// IndexSlot holds private unique roots, produced index candidates and
// REINDEX intents (root index code).
type IndexSlot interface {
	IndexSlot()
	Clone() IndexSlot
}

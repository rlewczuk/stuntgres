package index

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// WriteHost is the engine side of write-time unique and exclusion
// enforcement: the Engine.mu windows and the lock waits around the pure
// decisions of this package. Each method either takes Engine.mu itself for one
// window and never waits (the caller must not hold the lock), or waits with
// the caller holding nothing. No method takes a callback or an evaluation
// context. The ExclusionRegistry primitives stay caller-locked; only the
// engine calls them.
type WriteHost interface {
	// UniqueParent returns the statement's private view of ref's parent.
	// Locks, never waits; ids.ErrCatalogReplaced after an epoch change.
	UniqueParent(ref ids.ObjectRef) (*catalog.Relation, catalog.Read, error)
	// BindingAuthority captures b's write authority and type names against
	// view, then readmits the prepared programs unlocked. stale reports that
	// the private catalog moved past view; the caller rebinds. Locks, never
	// waits.
	BindingAuthority(b *UniqueWriteBinding, view catalog.Read) (stale bool, err error)
	// ClaimUniqueKey pins key on ref's index and acquires its unique claim
	// with the operation's lock policy, readmitting programs after a wait.
	// Waits; the caller holds nothing.
	ClaimUniqueKey(ref ids.ObjectRef, bound *UniqueBoundKey, key []sqltype.Value) error
	// WaitTransaction waits for owner's completion. Waits; the caller holds
	// nothing.
	WaitTransaction(owner *lock.Ticket, purpose lock.CompletionPurpose) error
	// CaptureUniqueProbe captures one coherent probe of idx for ref, with
	// the statement's write authority recaptured on the probe's view. heads
	// asks for the parent's head versions too. Locks, never waits.
	CaptureUniqueProbe(ref ids.ObjectRef, b *UniqueWriteBinding, idx *catalog.Relation, heads bool) (UniqueProbe, error)
	// ExistingIndexRoot returns idx's committed or private root, failing with
	// ids.ErrCatalogReplaced when the key semantics changed. Locks, never
	// waits.
	ExistingIndexRoot(idx *catalog.Relation) (*storage.IndexRoot, error)
	// CaptureExclusionScan captures one coherent exclusion scan of idx for
	// ref, with the statement's write authority (acting as a mutating FK
	// action's owner) recaptured on the scan's view. Locks, never waits.
	CaptureExclusionScan(ref ids.ObjectRef, b *UniqueWriteBinding, idx *catalog.Relation) (ExclusionScan, error)
	// ExclusionScanCurrent reports whether nothing scan captured for idx
	// changed since. Locks, never waits.
	ExclusionScanCurrent(scan ExclusionScan, idx ids.ObjectAddress) bool
	// PublishExclusion checks the epoch and state, gives proposal its row
	// identity, applies edit to the statement's projection and installs it,
	// all in one window. Locks, never waits.
	PublishExclusion(ref ids.ObjectRef, state *catalog.State, proposal *WriteCandidate, edit ExclusionEdit) (storage.RowVersion, error)
	// DiscardExclusion drops proposal's projected entries. Locks, never
	// waits.
	DiscardExclusion(proposal *WriteCandidate)
	// OwnExclusionKeys returns the keys the statement's published projection
	// holds for version on idx, or nil. Locks, never waits.
	OwnExclusionKeys(idx ids.ObjectAddress, version ids.VersionID) []sqltype.Value
	// AdmitExclusion marks version's entries on idx admitted. Locks, never
	// waits; like today it checks no epoch.
	AdmitExclusion(idx ids.ObjectAddress, version ids.VersionID)
	// EndExclusionRow projects the end of version of row on ref's exclusion
	// constraints, for a deletion staged outside the write stage. Locks,
	// never waits.
	EndExclusionRow(ref ids.ObjectRef, row ids.RowID, version ids.VersionID)
}

// UniqueProbe is one coherent capture of a unique index for a probe. Stale
// means the private catalog moved past the binding and the caller rebinds;
// the other fields are then zero. Root may be nil: the caller reports a
// missing ready root after its own checks.
type UniqueProbe struct {
	Stale         bool
	Parent, Index *catalog.Relation
	View          catalog.Read
	Schema        string
	Root          *storage.IndexRoot
	Versions      []storage.RowVersion
	Authority     access.View // for the ON CONFLICT check and key disclosure
}

// ExclusionScan is one coherent capture of an exclusion index, the private
// parent storage and every owner's pending projection.
type ExclusionScan struct {
	Generation      uint64
	Root            *storage.IndexRoot
	Pending         map[*lock.Ticket]*ExclusionProjection
	View            catalog.Read
	Schema          string
	Parent          ids.ObjectAddress
	Store           *storage.Relation
	StoreGeneration uint64
	Versions        []storage.RowVersion
	Ends            *storage.DeletionRoot
	Authority       access.View // for the conflict's key disclosure
}

// ExclusionEdit is the projection change of one exclusion admission: the
// candidate's entry on Index (when Member), whether it is admitted already,
// whether it carries the precheck outputs, and the row versions it ends.
type ExclusionEdit struct {
	Index                      ids.ObjectAddress
	Keys                       []sqltype.Value
	Member, Admitted, Precheck bool
	Excluded                   []storage.RowVersion
}

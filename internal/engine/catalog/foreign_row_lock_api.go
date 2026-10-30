package catalog

import "context"

// ForeignRowMarkType selects provider semantics, independently of SQL strength.
// COPY is the default: a captured tuple is not a claim of a remote lock.
type ForeignRowMarkType uint8

const (
	ForeignRowMarkCopy ForeignRowMarkType = iota
	ForeignRowMarkReference
	ForeignRowMarkKeyShare
	ForeignRowMarkShare
	ForeignRowMarkNoKeyUpdate
	ForeignRowMarkUpdate
)

type ForeignLockStrength uint8

const (
	ForeignLockNone ForeignLockStrength = iota
	ForeignLockKeyShare
	ForeignLockShare
	ForeignLockNoKeyUpdate
	ForeignLockUpdate
)

type ForeignLockWaitPolicy uint8

const (
	ForeignLockBlock ForeignLockWaitPolicy = iota
	ForeignLockNoWait
	ForeignLockSkipLocked
)

// Characteristics are captured from the sole SQL owner at execution, never at
// planning. They describe local requested modes; remote policy is the provider's.
type ForeignTransactionCharacteristics struct {
	Isolation            string
	ReadOnly, Deferrable bool
}

type ForeignRowMarkRequest struct {
	ForeignRequest
	Strength   ForeignLockStrength
	WaitPolicy ForeignLockWaitPolicy
}

// Identity must re-identify the original logical row, not a replacement which
// recycled its visible key. Private is immutable descriptor data, not a handle.
type ForeignRowMarkPlan struct {
	Type     ForeignRowMarkType
	Identity ForeignUpdateTargets
}

type ForeignRowMarkProvider interface {
	GetForeignRowMarkType(context.Context, ForeignRowMarkRequest) (ForeignRowMarkPlan, error)
}

// Present distinguishes missing/SKIP from an empty or all-NULL tuple. Errors
// veto all fields. Row is the full declared tuple; buffers survive until the
// next callback. Uncertain version comparisons must conservatively set Updated.
type ForeignRefetchResult struct {
	Row              ForeignRow
	Present, Updated bool
}

type ForeignRowRefetcher interface {
	RefetchForeignRow(context.Context, ForeignRowMarkRequest, ForeignRowIdentity) (ForeignRefetchResult, error)
}

// Recheck receives the actual replacement tuple. It supplements, never replaces,
// the local predicate/join/RLS recheck and may not substitute another tuple.
type ForeignScanRechecker interface {
	RecheckForeignScan(context.Context, ForeignRowMarkRequest, ForeignRow) (bool, error)
}

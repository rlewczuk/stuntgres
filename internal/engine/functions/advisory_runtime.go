package functions

import "stuntgres/internal/sqltype"

// AdvisoryAction, AdvisoryMode and AdvisoryLifetime describe an immutable call,
// not a lock-manager request or a captured execution owner.
type AdvisoryAction uint8

const (
	AdvisoryAcquire AdvisoryAction = iota
	AdvisoryTry
	AdvisoryUnlock
	AdvisoryUnlockAll
)

type AdvisoryMode uint8

const (
	AdvisoryExclusive AdvisoryMode = iota
	AdvisoryShare
)

type AdvisoryLifetime uint8

const (
	AdvisorySession AdvisoryLifetime = iota
	AdvisoryTransaction
)

// AdvisoryRuntime leaves resource construction, admission, ownership, waits and
// notices with the engine. The values have already been evaluated once.
type AdvisoryRuntime interface {
	Advisory(AdvisoryAction, AdvisoryMode, AdvisoryLifetime, int64, int64, bool, *sqltype.Descriptor) (sqltype.Value, error)
}

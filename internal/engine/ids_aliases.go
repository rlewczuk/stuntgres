package engine

import "stuntgres/internal/engine/ids"

// Temporary aliases for identity values moved to internal/engine/ids
// (docs/ENGINE-SPLIT-ids.md step 1d removes them).
type (
	TxID          = ids.TxID
	TxRef         = ids.TxRef
	TxOwner       = ids.TxOwner
	TxOwnerMarker = ids.TxOwnerMarker
	CommandID     = ids.CommandID
	CommitSeq     = ids.CommitSeq
	RowID         = ids.RowID
	VersionID     = ids.VersionID

	transactionIdentityState = ids.TxCompletion
)

const (
	transactionInProgress = ids.TxInProgress
	transactionCommitted  = ids.TxCommitted
	transactionAborted    = ids.TxAborted
)

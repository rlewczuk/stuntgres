package engine

import "stuntgres/internal/engine/ids"

// Temporary aliases for identity values moved to internal/engine/ids
// (docs/ENGINE-SPLIT-ids.md step 1d removes them).
type (
	CommandID = ids.CommandID
	CommitSeq = ids.CommitSeq
	RowID     = ids.RowID
	VersionID = ids.VersionID
)

const ()

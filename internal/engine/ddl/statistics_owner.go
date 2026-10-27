package ddl

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// RestoredStatisticsOwner captures exact authority for one immediate private
// Restore transfer. The replay driver owns its statement-scoped lifetime.
type RestoredStatisticsOwner struct {
	Ref                ids.ObjectRef
	Definition         *catalog.Statistics
	Namespace          *catalog.Namespace
	Creator, Recipient authorization.PrincipalRef
}

const StatisticsOwnerReplayBoundary = "restore accepts only an immediately paired statistics owner transfer"

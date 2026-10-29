package pgcatalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// IndexMetadataFact transports one detached index-owner projection. The runtime
// owns derivation; the producer only serializes it in catalog attribute order.
type IndexMetadataFact struct {
	IndexRelID, IndRelID                     ids.OID
	IndNatts, IndNKeyAtts                    int16
	IndIsUnique, IndNullsNotDistinct         bool
	IndIsPrimary, IndIsExclusion, IndImmediate bool
	IndIsClustered, IndIsValid, IndCheckXmin bool
	IndIsReady, IndIsLive, IndIsReplIdent    bool
	IndKey, IndCollation, IndClass, IndOption sqltype.Value
	IndexExpressions, Predicate              sqltype.Value
}

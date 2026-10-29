package pgcatalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const PartitionedTableOID ids.OID = 3350

// PartitionMetadataFact transports one captured descriptor projection. Derivation
// belongs to the runtime; the source serializes these facts in attribute order.
type PartitionMetadataFact struct {
	PartRelID, PartDefID                           ids.OID
	PartStrat                                      string
	PartNatts                                      int16
	PartAttrs, PartClass, PartCollation, PartExprs sqltype.Value
}

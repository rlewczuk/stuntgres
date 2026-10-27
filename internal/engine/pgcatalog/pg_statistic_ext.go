package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// StatisticExtOID identifies the extended-statistics definitions catalog.
const StatisticExtOID ids.OID = 3381

func capturePGStatisticExt(in Input) ([][]sqltype.Value, error) {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: StatisticExtOID}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner ||
		!RegisteredSource(in.Catalog, in.Source) || in.Source.Address() != address ||
		in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != in.Catalog.Epoch() ||
		in.Ref.Address != address || !in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid statistics definition source")
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	// CREATE STATISTICS owns definition population. Ordinary relations and
	// collected ANALYZE metrics are not statistics definition objects.
	return [][]sqltype.Value{}, nil
}

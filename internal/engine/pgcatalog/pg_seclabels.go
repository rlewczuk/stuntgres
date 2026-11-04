package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// SeclabelsOID is the initialized native view identity, not a portable PostgreSQL OID.
const SeclabelsOID ids.OID = 12099

// There is no native label provider or label store. The released view therefore
// has no rows; its SELECT authority is still checked by the ordinary binder.
func capturePGSeclabels(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	view, source := in.Catalog, in.Source
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: SeclabelsOID}
	if in.Owner == nil || view.Owner() != in.Owner || view.State() == nil || source == nil || source.Address() != address || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != view.Epoch() || in.Ref.Address != address || !RegisteredSource(view, source) {
		return nil, fmt.Errorf("stuntgres: foreign security labels view authority")
	}
	if len(source.ColumnMeta.CatalogAttributes) != 8 {
		return nil, fmt.Errorf("stuntgres: invalid security labels view attribute facts")
	}
	for i, column := range source.Columns {
		fact := source.ColumnMeta.CatalogAttributes[i]
		usage := column.Usage.Type.Usage()
		if i >= 5 {
			usage.Collation.ID = 950
		}
		if !column.Nullable || !column.NullabilityKnown || column.Usage != usage || column.Usage.Typmod != -1 || column.DeclaredDimensions != 0 || fact.NotNull || fact.Dimensions != 0 || fact.Collation != usage.Collation.ID {
			return nil, fmt.Errorf("stuntgres: invalid security labels view shape")
		}
	}
	database, ok := view.Database(in.Owner.DatabaseAddress())
	if !ok || database == nil || !in.Roles.BelongsTo(in.Owner.Principals) || in.Roles.DatabaseOwner() != database.Owner {
		return nil, fmt.Errorf("stuntgres: foreign security labels view owner authority")
	}
	return nil, in.Context.Err()
}

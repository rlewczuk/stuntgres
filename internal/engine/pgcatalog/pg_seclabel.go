package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// No security label provider is installed in the embedded engine. This is a
// real empty catalog, not successful assignment storage. Ordinary relation and
// column ACL admission remains the binder's responsibility, even at EOF.
func capturePGSeclabel(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	view, source := in.Catalog, in.Source
	if in.Owner == nil || view.Owner() != in.Owner || view.State() == nil || source == nil || source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3596}) || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != view.Epoch() || in.Ref.Address != source.Address() || !RegisteredSource(view, source) {
		return nil, fmt.Errorf("stuntgres: foreign security label source authority")
	}
	if len(source.ColumnMeta.CatalogAttributes) != 5 {
		return nil, fmt.Errorf("stuntgres: invalid security label attribute facts")
	}
	for i, column := range source.Columns {
		fact := source.ColumnMeta.CatalogAttributes[i]
		// InitializeAttributes gives textual bootstrap attributes C collation;
		// the result carrier separately retains its ordinary TEXT usage.
		collation := uint32(0)
		if i >= 3 {
			collation = 950
		}
		if column.Nullable || !column.NullabilityKnown || column.Usage != column.Usage.Type.Usage() || column.Usage.Typmod != -1 || column.DeclaredDimensions != 0 || !fact.NotNull || fact.Dimensions != 0 || fact.Collation != collation {
			return nil, fmt.Errorf("stuntgres: invalid security label source shape")
		}
	}
	database, ok := view.Database(in.Owner.DatabaseAddress())
	if !ok || database == nil || !in.Roles.BelongsTo(in.Owner.Principals) || in.Roles.DatabaseOwner() != database.Owner {
		return nil, fmt.Errorf("stuntgres: foreign security label owner authority")
	}
	return nil, in.Context.Err()
}

package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const ShdependOID ids.OID = 1214

// capturePGShdepend encodes immutable core-classified dependency facts. It owns
// typed rows only; classification and cluster capture remain core responsibilities.
func capturePGShdepend(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	view, source := in.Catalog, in.Source
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ShdependOID}
	if in.Owner == nil || view.Owner() != in.Owner || view.State() == nil || source == nil ||
		source.Address() != address || source.RelationKind != catalog.CatalogRelation ||
		in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != view.Epoch() ||
		in.Ref.Address != address || view.Get(address) != source || !in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: foreign shared dependency source authority")
	}
	names := [...]string{"dbid", "classid", "objid", "objsubid", "refclassid", "refobjid", "deptype"}
	if len(source.Columns) != len(names) {
		return nil, fmt.Errorf("stuntgres: invalid shared dependency source shape")
	}
	for i, name := range names {
		id := sqltype.Oid
		if i == 3 {
			id = sqltype.Int4
		} else if i == 6 {
			id = sqltype.Char
		}
		typ, ok := view.LookupType(id)
		column := source.Columns[i]
		if !ok || column.Name != name || column.Usage.Type != typ || column.Nullable || !column.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: foreign shared dependency source type")
		}
	}
	rows := make([][]sqltype.Value, 0, len(in.SharedDependencies))
	for _, edge := range in.SharedDependencies {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if edge.Address.ClassID == 0 || edge.Address.ObjectID == 0 || edge.Address.SubID < 0 || edge.ReferenceClass != ids.RoleClassID || edge.Reference == 0 || (edge.Kind != 'o' && edge.Kind != 'a' && edge.Kind != 'i' && edge.Kind != 'r') {
			return nil, fmt.Errorf("stuntgres: invalid shared dependency fact")
		}
		row := make([]sqltype.Value, 7)
		for i, n := range [...]uint32{uint32(edge.Database), uint32(edge.Address.ClassID), uint32(edge.Address.ObjectID), 0, uint32(edge.ReferenceClass), uint32(edge.Reference)} {
			var err error
			if i == 3 {
				row[i], err = sqltype.Integer(source.Columns[i].Usage.Type, int64(edge.Address.SubID))
			} else {
				row[i], err = sqltype.OIDValue(source.Columns[i].Usage.Type, n)
			}
			if err != nil {
				return nil, err
			}
		}
		row[6] = sqltype.String(source.Columns[6].Usage.Type, string(edge.Kind))
		rows = append(rows, row)
	}
	return rows, in.Context.Err()
}

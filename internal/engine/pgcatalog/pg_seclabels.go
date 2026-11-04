package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// SeclabelsOID is the initialized native view identity, not a portable PostgreSQL OID.
const SeclabelsOID ids.OID = 12099

const SeclabelsRuleOID ids.OID = 12361

func capturePGSeclabels(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	view, source := in.Catalog, in.Source
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: SeclabelsOID}
	if in.Owner == nil || view.Owner() != in.Owner || view.State() == nil || source == nil || source.Address() != address || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != view.Epoch() || in.Ref.Address != address || !RegisteredSource(view, source) || view.Get(address) != source {
		return nil, fmt.Errorf("stuntgres: foreign security labels view authority")
	}
	if len(source.Columns) != 8 || len(source.ColumnMeta.CatalogAttributes) != 8 {
		return nil, fmt.Errorf("stuntgres: invalid security labels view attribute facts")
	}
	var spec *Spec
	for i := range Specs {
		if Specs[i].OID == SeclabelsOID {
			spec = &Specs[i]
			break
		}
	}
	if spec == nil || len(spec.Columns) != 8 {
		return nil, fmt.Errorf("stuntgres: invalid security labels view shape")
	}
	for i, expected := range spec.Columns {
		owned, ok := view.LookupType(expected.Type)
		column := source.Columns[i]
		if !ok || owned == nil || column.Name != expected.Name || column.Usage.Type != owned {
			return nil, fmt.Errorf("stuntgres: invalid security labels view shape")
		}
		usage := owned.Usage()
		if expected.Collation != 0 {
			usage.Collation.ID = expected.Collation
		}
		fact := source.ColumnMeta.CatalogAttributes[i]
		if !column.Nullable || !column.NullabilityKnown || column.Usage != usage || column.Usage.Typmod != -1 || column.DeclaredDimensions != 0 || fact.NotNull || fact.Dimensions != 0 || fact.Collation != usage.Collation.ID {
			return nil, fmt.Errorf("stuntgres: invalid security labels view shape")
		}
	}
	database, ok := view.Database(in.Owner.DatabaseAddress())
	if !ok || database == nil || !in.Roles.BelongsTo(in.Owner.Principals) || in.Roles.DatabaseOwner() != database.Owner {
		return nil, fmt.Errorf("stuntgres: foreign security labels view owner authority")
	}
	return securityLabelRows(in, false, true)
}

func securityLabelRows(in Input, shared, presentation bool) ([][]sqltype.Value, error) {
	if err := catalog.ValidateSecurityLabels(in.Catalog); err != nil {
		return nil, err
	}
	var rows [][]sqltype.Value
	source := in.Source
	for key, record := range in.Catalog.State().SecurityLabels() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		address := key.Object
		if !presentation && (address.ClassID == ids.DatabaseClassID) != shared {
			continue
		}
		row := make([]sqltype.Value, len(source.Columns))
		var err error
		row[0], err = sqltype.OIDValue(source.Columns[0].Usage.Type, uint32(address.ObjectID))
		if err != nil {
			return nil, err
		}
		row[1], err = sqltype.OIDValue(source.Columns[1].Usage.Type, uint32(address.ClassID))
		if err != nil {
			return nil, err
		}
		at := 2
		if !shared || presentation {
			row[2], err = sqltype.Integer(source.Columns[2].Usage.Type, int64(address.SubID))
			if err != nil {
				return nil, err
			}
			at = 3
		}
		if presentation {
			object := address
			object.SubID = 0
			d := in.Catalog.Get(object)
			kind, name, namespace, err := securityLabelName(in, d, address.SubID)
			if err != nil {
				return nil, err
			}
			row[3] = sqltype.String(source.Columns[3].Usage.Type, kind)
			row[4] = sqltype.Null(source.Columns[4].Usage.Type)
			if namespace.ObjectID != 0 {
				row[4], err = sqltype.OIDValue(source.Columns[4].Usage.Type, uint32(namespace.ObjectID))
				if err != nil {
					return nil, err
				}
			}
			row[5] = sqltype.String(source.Columns[5].Usage.Type, name)
			at = 6
		}
		row[at] = sqltype.String(source.Columns[at].Usage.Type, key.Provider)
		row[at+1] = sqltype.String(source.Columns[at+1].Usage.Type, record.Label)
		rows = append(rows, row)
	}
	return rows, in.Context.Err()
}
func securityLabelName(in Input, d catalog.Descriptor, sub int32) (kind, name string, namespace ids.ObjectAddress, err error) {
	quote := catalog.DependencyIdentifier
	name = quote(d.Name())
	namespace = d.Namespace()
	if namespace.ObjectID != 0 {
		visible := false
		for _, schema := range in.TypeNamePath {
			candidate := in.Catalog.NameAddress(ids.NameKey{ClassID: d.Address().ClassID, Namespace: schema, Name: d.Name(), Signature: catalog.DescriptorNameKey(d).Signature})
			if candidate.ObjectID != 0 {
				visible = candidate == d.Address()
				break
			}
		}
		if !visible {
			name = quote(in.Catalog.Get(namespace).Name()) + "." + name
		}
	}
	switch d := d.(type) {
	case *catalog.Database:
		kind = "database"
	case *catalog.Namespace:
		kind = "schema"
		namespace = d.Address()
	case *catalog.Relation:
		kind = "table"
		if d.RelationKind == catalog.SequenceRelation {
			kind = "sequence"
		}
		if d.RelationKind == catalog.ViewRelation || d.CatalogView {
			kind = "view"
		}
		if sub != 0 {
			kind = "column"
			name += "." + catalog.SepgsqlAttributeName(d, sub)
		}
	case *catalog.Procedure:
		kind = "function"
		if d.RoutineKind == catalog.ProcedureRoutine {
			kind = "procedure"
		}
		if d.RoutineKind == catalog.AggregateRoutine {
			kind = "aggregate"
		}
		if d.RoutineKind == catalog.WindowRoutine {
			kind = "window"
		}
		if in.Runtime == nil {
			return "", "", namespace, fmt.Errorf("stuntgres: missing security label argument presentation authority")
		}
		arguments, err := in.Runtime.RoutineArguments(in.Context, in.Catalog, d, true)
		if err != nil {
			return "", "", namespace, err
		}
		name += "(" + arguments + ")"
	}
	return
}

package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoViewTableUsageOID is the native virtual relation identity, not a
// PostgreSQL allocator-dependent information-schema view OID.
const InfoViewTableUsageOID ids.OID = 16209

// captureInfoViewTableUsage projects the direct SELECT-rule relation edges.
// Visibility depends only on enabled ownership of each referenced relation.
func captureInfoViewTableUsage(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoViewTableUsageOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 6 {
		return nil, fmt.Errorf("stuntgres: invalid view_table_usage source")
	}
	for _, column := range in.Source.Columns {
		typ := column.Usage.Type
		if typ == nil || !typ.IsDomain() || typ.ID() != 13248 || typ.BaseType().ID() != sqltype.Name {
			return nil, fmt.Errorf("stuntgres: invalid view_table_usage identifier domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		view, ok := descriptor.(*catalog.Relation)
		if !ok || view.RelationKind != catalog.ViewRelation || view.View == nil {
			continue
		}
		references, err := in.Runtime.ViewReferences(in.Context, in.Catalog, view)
		if err != nil {
			return nil, err
		}
		var seen map[ids.OID]struct{}
		var prefix [4]sqltype.Value
		initialized := false
		for _, reference := range references {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			if reference.ClassID != ids.RelationClassID {
				continue
			}
			address := reference
			address.SubID = 0
			target, ok := in.Catalog.Relation(address)
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing view_table_usage dependency")
			}
			// Validate before pair deduplication, including a later attribute
			// of an already emitted relation. tableoid is a real system attr.
			if reference.SubID != 0 && reference.SubID != -6 && !target.HasAttribute(reference.SubID) {
				return nil, fmt.Errorf("stuntgres: invalid view_table_usage dependency attribute")
			}
			if address == view.Address() {
				continue
			}
			switch ClassKind(in.Catalog, target) {
			case "r", "v", "f", "p":
			default:
				continue
			}
			if _, duplicate := seen[address.ObjectID]; duplicate {
				continue
			}
			visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), target.Owner)
			if err != nil {
				return nil, err
			}
			if !visible {
				continue
			}
			if !initialized {
				namespace, ok := in.Catalog.Namespace(view.Namespace())
				if !ok {
					return nil, fmt.Errorf("stuntgres: missing view_table_usage view namespace")
				}
				database := in.Runtime.DatabaseName()
				for i, text := range [4]string{database, namespace.Name(), view.Name(), database} {
					typ := in.Source.Columns[i].Usage.Type
					prefix[i], err = typ.Wrap(sqltype.String(typ.BaseType(), text))
					if err != nil {
						return nil, err
					}
				}
				initialized = true
			}
			namespace, ok := in.Catalog.Namespace(target.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing view_table_usage dependency namespace")
			}
			row := make([]sqltype.Value, 6)
			copy(row, prefix[:])
			for i, text := range [2]string{namespace.Name(), target.Name()} {
				typ := in.Source.Columns[i+4].Usage.Type
				row[i+4], err = typ.Wrap(sqltype.String(typ.BaseType(), text))
				if err != nil {
					return nil, err
				}
			}
			rows = append(rows, row)
			if seen == nil {
				seen = make(map[ids.OID]struct{})
			}
			seen[address.ObjectID] = struct{}{}
		}
	}
	return rows, nil
}

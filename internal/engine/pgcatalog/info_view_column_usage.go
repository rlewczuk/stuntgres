package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoViewColumnUsageOID is the native virtual relation identity, separate from
// PostgreSQL's allocated view OID and the predefined row/array identities.
const InfoViewColumnUsageOID ids.OID = 16254

// captureInfoViewColumnUsage projects direct SELECT-rule column dependencies.
// The stored collector owns address deduplication and pinned-object exclusion.
func captureInfoViewColumnUsage(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoViewColumnUsageOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 7 {
		return nil, fmt.Errorf("stuntgres: invalid view_column_usage source")
	}
	domain, ok := in.Catalog.LookupType(13248)
	base, baseOK := in.Catalog.LookupType(sqltype.Name)
	if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base {
		return nil, fmt.Errorf("stuntgres: missing view_column_usage identifier domain")
	}
	for _, column := range in.Source.Columns {
		if column.Usage.Type != domain {
			return nil, fmt.Errorf("stuntgres: invalid view_column_usage identifier domain")
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
		var prefix [4]sqltype.Value
		initialized := false
		for _, reference := range references {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			if reference.ClassID != ids.RelationClassID || reference.SubID == 0 {
				continue
			}
			address := reference
			address.SubID = 0
			target, ok := in.Catalog.Relation(address)
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing view_column_usage dependency")
			}
			columnName := "tableoid"
			if reference.SubID != -6 {
				slot := target.IndexForAttribute(reference.SubID)
				if slot < 0 {
					return nil, fmt.Errorf("stuntgres: invalid view_column_usage dependency attribute")
				}
				columnName = target.Columns[slot].Name
			}
			if address == view.Address() {
				continue
			}
			switch ClassKind(in.Catalog, target) {
			case "r", "v", "f", "p":
			default:
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
					return nil, fmt.Errorf("stuntgres: missing view_column_usage view namespace")
				}
				database := in.Runtime.DatabaseName()
				for i, text := range [4]string{database, namespace.Name(), view.Name(), database} {
					prefix[i], err = domain.Wrap(sqltype.String(base, text))
					if err != nil {
						return nil, err
					}
				}
				initialized = true
			}
			namespace, ok := in.Catalog.Namespace(target.Namespace())
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing view_column_usage dependency namespace")
			}
			row := make([]sqltype.Value, 7)
			copy(row, prefix[:])
			for i, text := range [3]string{namespace.Name(), target.Name(), columnName} {
				row[i+4], err = domain.Wrap(sqltype.String(base, text))
				if err != nil {
					return nil, err
				}
			}
			rows = append(rows, row)
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

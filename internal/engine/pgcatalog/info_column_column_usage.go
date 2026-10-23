package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoColumnColumnUsageOID is a native relation identity, separate from the
// predefined row/array types and PostgreSQL's allocated relation identity.
const InfoColumnColumnUsageOID ids.OID = 16210

func captureInfoColumnColumnUsage(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoColumnColumnUsageOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 5 {
		return nil, fmt.Errorf("stuntgres: invalid column_column_usage source")
	}
	domain, ok := in.Catalog.LookupType(13248)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing column_column_usage identifier domain")
	}
	for _, column := range in.Source.Columns {
		typ := column.Usage.Type
		if typ == nil || typ != domain || !typ.IsDomain() || typ.BaseType().ID() != sqltype.Name {
			return nil, fmt.Errorf("stuntgres: invalid column_column_usage identifier domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		table, ok := descriptor.(*catalog.Relation)
		if !ok || len(table.ColumnMeta.Generations) == 0 {
			continue
		}
		visible, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), table.Owner)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		namespace, ok := in.Catalog.Namespace(table.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing column_column_usage table namespace")
		}
		for attribute, generation := range table.ColumnMeta.Generations {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			dependent := table.IndexForAttribute(attribute)
			if dependent < 0 {
				continue
			}
			// Dependencies belong to the retained analyzed definition, not its folded
			// execution plan. In particular, dead CASE arms still record references.
			refs, err := in.Runtime.ExpressionReferences(in.Context, in.Catalog, generation.Expression)
			if err != nil {
				return nil, err
			}
			for _, ref := range refs {
				if err := in.Context.Err(); err != nil {
					return nil, err
				}
				whole := ref
				whole.SubID = 0
				if whole != table.Address() || ref.SubID == 0 || ref.SubID == attribute {
					continue
				}
				base := "tableoid"
				if ref.SubID != -6 {
					slot := table.IndexForAttribute(ref.SubID)
					if slot < 0 {
						return nil, fmt.Errorf("stuntgres: missing column_column_usage dependency attribute")
					}
					base = table.Columns[slot].Name
				}
				row := make([]sqltype.Value, 5)
				for i, name := range [5]string{database, namespace.Name(), table.Name(), base, table.Columns[dependent].Name} {
					typ := in.Source.Columns[i].Usage.Type
					row[i], err = typ.Wrap(sqltype.String(typ.BaseType(), name))
					if err != nil {
						return nil, err
					}
				}
				rows = append(rows, row)
			}
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

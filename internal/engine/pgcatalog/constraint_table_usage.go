package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ConstraintTableUsageOID is a reserved native relation identity, not a
// PostgreSQL allocator identity or the view's row-type identity.
const ConstraintTableUsageOID ids.OID = 16252

// Capture preserves one occurrence per actual PK, UNIQUE or FK descriptor.
// FK projection and visibility belong to the target table, while the constraint
// namespace and name belong to the child. No column or index expansion is needed.
func captureConstraintTableUsage(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, ConstraintTableUsageOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 6 {
		return nil, fmt.Errorf("stuntgres: invalid constraint_table_usage source")
	}
	domain, ok := in.Catalog.LookupType(13248)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing constraint_table_usage identifier domain")
	}
	for _, column := range in.Source.Columns {
		typ := column.Usage.Type
		if typ == nil || typ != domain || !typ.IsDomain() || typ.BaseType().ID() != sqltype.Name {
			return nil, fmt.Errorf("stuntgres: invalid constraint_table_usage identifier domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	type projectedTable struct {
		schema  string
		visible bool
	}
	// Resolve namespaces and inherited ownership once per projected table,
	// including targets shared by multiple foreign keys.
	projected := make(map[ids.ObjectAddress]projectedTable)
	emit := func(table *catalog.Relation, constraintSchema, name string) error {
		if err := in.Context.Err(); err != nil {
			return err
		}
		p, cached := projected[table.Address()]
		if !cached {
			kind := ClassKind(in.Catalog, table)
			if kind == "r" || kind == "p" {
				namespace, ok := in.Catalog.Namespace(table.Namespace())
				if !ok {
					return fmt.Errorf("stuntgres: missing constraint_table_usage table namespace")
				}
				p.schema = namespace.Name()
				var err error
				p.visible, err = in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), table.Owner)
				if err != nil {
					return err
				}
			}
			projected[table.Address()] = p
		}
		if !p.visible {
			return nil
		}
		row := make([]sqltype.Value, 6)
		for i, value := range [6]string{database, p.schema, table.Name(), database, constraintSchema, name} {
			var err error
			row[i], err = domain.Wrap(sqltype.String(domain.BaseType(), value))
			if err != nil {
				return err
			}
		}
		rows = append(rows, row)
		return nil
	}
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		table, ok := descriptor.(*catalog.Relation)
		if !ok || len(table.Constraints.Unique) == 0 && len(table.Constraints.ForeignKeys) == 0 {
			continue
		}
		namespace, ok := in.Catalog.Namespace(table.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing constraint_table_usage constraint namespace")
		}
		for _, constraint := range table.Constraints.Unique {
			if err := emit(table, namespace.Name(), constraint.Name); err != nil {
				return nil, err
			}
		}
		for _, constraint := range table.Constraints.ForeignKeys {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			target, ok := in.Catalog.Relation(constraint.Target)
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing constraint_table_usage foreign key target")
			}
			if err := emit(target, namespace.Name(), constraint.Name); err != nil {
				return nil, err
			}
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

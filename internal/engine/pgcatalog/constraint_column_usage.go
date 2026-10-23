package pgcatalog

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ConstraintColumnUsageOID is a reserved native relation identity, not a
// PostgreSQL allocator identity or the view's row-type identity.
const ConstraintColumnUsageOID ids.OID = 16207

// Capture uses actual constraint descriptors and CHECK dependency authority from
// one paired catalog/role generation. FK rows describe the target's columns,
// but retain the child constraint's namespace and name.
func captureConstraintColumnUsage(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, ConstraintColumnUsageOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 7 {
		return nil, fmt.Errorf("stuntgres: invalid constraint_column_usage source")
	}
	domain, ok := in.Catalog.LookupType(13248)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing constraint_column_usage identifier domain")
	}
	for _, column := range in.Source.Columns {
		typ := column.Usage.Type
		if typ == nil || typ != domain || !typ.IsDomain() || typ.BaseType().ID() != sqltype.Name {
			return nil, fmt.Errorf("stuntgres: invalid constraint_column_usage identifier domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	// This capture-local memo avoids repeating owner-role traversal for composite
	// constraints and for multiple children referencing the same target.
	type projectedTable struct {
		schema  string
		visible bool
	}
	projected := make(map[ids.ObjectAddress]projectedTable)
	projection := func(table *catalog.Relation) (projectedTable, error) {
		if p, ok := projected[table.Address()]; ok {
			return p, nil
		}
		var p projectedTable
		kind := ClassKind(in.Catalog, table)
		if kind == "r" || kind == "p" {
			namespace, ok := in.Catalog.Namespace(table.Namespace())
			if !ok {
				return p, fmt.Errorf("stuntgres: missing constraint_column_usage table namespace")
			}
			p.schema = namespace.Name()
			var err error
			p.visible, err = in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), table.Owner)
			if err != nil {
				return p, err
			}
		}
		projected[table.Address()] = p
		return p, nil
	}
	// Released CHECK and NOT NULL branches each have their own DISTINCT. The
	// key branch is UNION ALL: identical tuples from separate FKs must survive.
	type distinctKey struct {
		tableSchema, table, column, constraintSchema, constraint string
		owner                                                    authorization.PrincipalRef
	}
	var checks, notNulls map[distinctKey]struct{}
	emit := func(table *catalog.Relation, attribute int32, constraintSchema, name string, distinct *map[distinctKey]struct{}) error {
		if err := in.Context.Err(); err != nil {
			return err
		}
		slot := table.IndexForAttribute(attribute)
		if slot < 0 {
			return nil
		}
		p, err := projection(table)
		if err != nil {
			return err
		}
		if !p.visible {
			return nil
		}
		column := table.Columns[slot].Name
		if distinct != nil {
			key := distinctKey{p.schema, table.Name(), column, constraintSchema, name, table.Owner}
			if _, exists := (*distinct)[key]; exists {
				return nil
			}
			if *distinct == nil {
				*distinct = make(map[distinctKey]struct{})
			}
			(*distinct)[key] = struct{}{}
		}
		row := make([]sqltype.Value, 7)
		for i, name := range [7]string{database, p.schema, table.Name(), column, database, constraintSchema, name} {
			typ := in.Source.Columns[i].Usage.Type
			row[i], err = typ.Wrap(sqltype.String(typ.BaseType(), name))
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
		if !ok {
			continue
		}
		constraints := &table.Constraints
		if len(constraints.Check) == 0 && len(constraints.NotNull) == 0 && len(constraints.Unique) == 0 && len(constraints.ForeignKeys) == 0 {
			continue
		}
		namespace, ok := in.Catalog.Namespace(table.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing constraint_column_usage constraint namespace")
		}
		schema := namespace.Name()
		for _, check := range constraints.Check {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			refs, err := in.Runtime.ExpressionReferences(in.Context, in.Catalog, check.Expression)
			if err != nil {
				return nil, err
			}
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			for _, ref := range refs {
				if err := in.Context.Err(); err != nil {
					return nil, err
				}
				if ref.ClassID != ids.RelationClassID || ref.SubID == 0 {
					continue
				}
				address := ref
				address.SubID = 0
				target, ok := in.Catalog.Relation(address)
				if !ok {
					return nil, fmt.Errorf("stuntgres: missing constraint_column_usage check dependency")
				}
				if err := emit(target, ref.SubID, schema, check.Name, &checks); err != nil {
					return nil, err
				}
			}
		}
		for _, constraint := range constraints.NotNull {
			if err := emit(table, constraint.Attribute, schema, constraint.Name, &notNulls); err != nil {
				return nil, err
			}
		}
		for _, constraint := range constraints.Unique {
			for i, attribute := range constraint.Attributes {
				if err := in.Context.Err(); err != nil {
					return nil, err
				}
				if slices.Contains(constraint.Attributes[:i], attribute) {
					continue
				}
				if err := emit(table, attribute, schema, constraint.Name, nil); err != nil {
					return nil, err
				}
			}
		}
		for _, constraint := range constraints.ForeignKeys {
			if err := in.Context.Err(); err != nil {
				return nil, err
			}
			target, ok := in.Catalog.Relation(constraint.Target)
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing constraint_column_usage foreign key target")
			}
			for i, attribute := range constraint.Referenced {
				if err := in.Context.Err(); err != nil {
					return nil, err
				}
				if slices.Contains(constraint.Referenced[:i], attribute) {
					continue
				}
				if err := emit(target, attribute, schema, constraint.Name, nil); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

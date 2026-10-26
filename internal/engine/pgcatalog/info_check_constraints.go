package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoCheckConstraintsOID is a native source identity, not a PostgreSQL allocator OID.
const InfoCheckConstraintsOID ids.OID = 16241

func captureInfoCheckConstraints(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoCheckConstraintsOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 4 {
		return nil, fmt.Errorf("stuntgres: invalid check_constraints source")
	}
	for i, column := range in.Source.Columns {
		domain, base := sqltype.ID(13248), sqltype.Name
		if i == 3 {
			domain, base = 13246, sqltype.VarChar
		}
		typ, ok := in.Catalog.LookupType(domain)
		physical, present := in.Catalog.LookupType(base)
		if !ok || !present || !typ.IsDomain() || typ.BaseType() != physical || column.Usage != typ.Usage() {
			return nil, fmt.Errorf("stuntgres: invalid check_constraints source domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	owners := make(map[authorization.PrincipalRef]bool)
	visible := func(owner authorization.PrincipalRef) (bool, error) {
		if allowed, ok := owners[owner]; ok {
			return allowed, nil
		}
		allowed, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), owner)
		if err == nil {
			owners[owner] = allowed
		}
		return allowed, err
	}
	emit := func(namespace ids.ObjectAddress, oid ids.OID, name, clause string) error {
		if err := in.Context.Err(); err != nil {
			return err
		}
		if oid == 0 || name == "" {
			return fmt.Errorf("stuntgres: invalid check_constraints definition")
		}
		schema, ok := in.Catalog.Namespace(namespace)
		if !ok {
			return fmt.Errorf("stuntgres: missing check_constraints namespace")
		}
		row := make([]sqltype.Value, 4)
		for i, text := range [4]string{database, schema.Name(), name, clause} {
			typ := in.Source.Columns[i].Usage.Type
			value, err := typ.Wrap(sqltype.String(typ.BaseType(), text))
			if err != nil {
				return err
			}
			row[i] = value
		}
		// UNION ALL: distinct definitions can have identical projected tuples.
		rows = append(rows, row)
		return nil
	}
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		switch d := descriptor.(type) {
		case *catalog.Relation:
			if len(d.Constraints.Check) == 0 && len(d.Constraints.NotNull) == 0 {
				continue
			}
			allowed, err := visible(d.Owner)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
			for _, check := range d.Constraints.Check {
				if err := in.Context.Err(); err != nil {
					return nil, err
				}
				clause, err := in.Runtime.ColumnExpression(in.Context, in.Catalog, d, check.Expression)
				if err != nil {
					return nil, err
				}
				if err := emit(d.Namespace(), check.OID, check.Name, clause); err != nil {
					return nil, err
				}
			}
			for _, nn := range d.Constraints.NotNull {
				slot := d.IndexForAttribute(nn.Attribute)
				if slot < 0 || slot >= len(d.Columns) {
					return nil, fmt.Errorf("stuntgres: missing check_constraints attribute")
				}
				if err := emit(d.Namespace(), nn.OID, nn.Name, d.Columns[slot].Name+" IS NOT NULL"); err != nil {
					return nil, err
				}
			}
		case *catalog.Type:
			if !d.SQLType.IsDomain() || len(d.DomainChecks) == 0 && d.DomainNotNull == nil {
				continue
			}
			allowed, err := visible(d.Owner)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
			for _, check := range d.DomainChecks {
				if err := in.Context.Err(); err != nil {
					return nil, err
				}
				clause, err := in.Runtime.DomainExpression(in.Context, in.Catalog, d, check.Expression)
				if err != nil {
					return nil, err
				}
				if err := emit(d.Namespace(), check.OID, check.Name, clause); err != nil {
					return nil, err
				}
			}
			if nn := d.DomainNotNull; nn != nil {
				if err := emit(d.Namespace(), nn.OID, nn.Name, "VALUE IS NOT NULL"); err != nil {
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

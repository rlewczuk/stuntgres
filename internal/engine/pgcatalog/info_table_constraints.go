package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoTableConstraintsOID is a native source identity, not a PostgreSQL allocation.
const InfoTableConstraintsOID ids.OID = 16240

func captureInfoTableConstraints(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoTableConstraintsOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 11 {
		return nil, fmt.Errorf("stuntgres: invalid table_constraints source")
	}
	for i, column := range in.Source.Columns {
		domain, base := sqltype.ID(13248), sqltype.Name
		if i == 6 {
			domain, base = 13246, sqltype.VarChar
		} else if i > 6 {
			domain, base = 13256, sqltype.VarChar
		}
		typ, ok := in.Catalog.LookupType(domain)
		physical, present := in.Catalog.LookupType(base)
		if !ok || !present || !typ.IsDomain() || typ.BaseType() != physical || column.Usage != typ.Usage() {
			return nil, fmt.Errorf("stuntgres: invalid table_constraints source domain")
		}
	}
	rows := make([][]sqltype.Value, 0)
	database := in.Runtime.DatabaseName()
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		table, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		kind := ClassKind(in.Catalog, table)
		if kind != "r" && kind != "p" {
			continue
		}
		if len(table.Constraints.NotNull)+len(table.Constraints.Check)+len(table.Constraints.Unique)+len(table.Constraints.ForeignKeys) == 0 {
			continue
		}
		namespace, ok := in.Catalog.Namespace(table.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing table_constraints namespace")
		}
		if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != in.Runtime.TemporaryOwner() {
			continue
		}
		visible, err := infoTableConstraintsVisible(in, table)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		// Validity, enforcement and persistent timing are independent facts.
		emit := func(oid ids.OID, name, kind, distinct string, negative, deferrable, deferred bool) error {
			if err := in.Context.Err(); err != nil {
				return err
			}
			if oid == 0 || name == "" {
				return fmt.Errorf("stuntgres: invalid table_constraints definition")
			}
			row := make([]sqltype.Value, 11)
			enforced := "YES"
			if negative {
				enforced = "NO"
			}
			timing, initial := "NO", "NO"
			if deferrable {
				timing = "YES"
			}
			if deferred {
				initial = "YES"
			}
			for i, text := range [11]string{database, namespace.Name(), name, database, namespace.Name(), table.Name(), kind, timing, initial, enforced, distinct} {
				typ := in.Source.Columns[i].Usage.Type
				if i == 10 && distinct == "" {
					row[i] = sqltype.Null(typ)
					continue
				}
				value, err := typ.Wrap(sqltype.String(typ.BaseType(), text))
				if err != nil {
					return err
				}
				row[i] = value
			}
			rows = append(rows, row)
			return nil
		}
		for i := range table.Constraints.NotNull {
			constraint := &table.Constraints.NotNull[i]
			if err := emit(constraint.OID, constraint.Name, "CHECK", "", false, false, false); err != nil {
				return nil, err
			}
		}
		for i := range table.Constraints.Check {
			constraint := &table.Constraints.Check[i]
			if err := emit(constraint.OID, constraint.Name, "CHECK", "", constraint.NotEnforced, false, false); err != nil {
				return nil, err
			}
		}
		for i := range table.Constraints.Unique {
			constraint := &table.Constraints.Unique[i]
			kind, distinct := "UNIQUE", "YES"
			if constraint.NullsNotDistinct {
				distinct = "NO"
			}
			if constraint.Primary {
				kind, distinct = "PRIMARY KEY", ""
			}
			if err := emit(constraint.OID, constraint.Name, kind, distinct, false, constraint.Deferrable, constraint.InitiallyDeferred); err != nil {
				return nil, err
			}
		}
		for i := range table.Constraints.ForeignKeys {
			constraint := &table.Constraints.ForeignKeys[i]
			if err := emit(constraint.OID, constraint.Name, "FOREIGN KEY", "", constraint.NotEnforced, constraint.Deferrable, constraint.InitiallyDeferred); err != nil {
				return nil, err
			}
		}
	}
	return rows, nil
}

// Namespace USAGE and SELECT/MAINTAIN privileges do not disclose constraints.
// A qualifying grant on any live column discloses every constraint of the table.
func infoTableConstraintsVisible(in Input, table *catalog.Relation) (bool, error) {
	owner, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), table.Owner)
	if err != nil || owner {
		return owner, err
	}
	ref := in.Ref.Domain().Ref(in.Ref.Epoch, table.Address())
	rights, err := in.Runtime.Effective(ref, false)
	if err != nil {
		return false, err
	}
	if rights&(authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.DeletePrivilege|authorization.TruncatePrivilege|authorization.ReferencesPrivilege|authorization.TriggerPrivilege) != 0 {
		return true, nil
	}
	for _, attribute := range table.Attributes {
		if err := in.Context.Err(); err != nil {
			return false, err
		}
		if attribute <= 0 {
			continue
		}
		if _, dropped := table.ColumnMeta.Dropped[attribute]; dropped {
			continue
		}
		column := ref
		column.Address.SubID = attribute
		rights, err := in.Runtime.Effective(column, false)
		if err != nil {
			return false, err
		}
		if rights&(authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.ReferencesPrivilege) != 0 {
			return true, nil
		}
	}
	return false, nil
}

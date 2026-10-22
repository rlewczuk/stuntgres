package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// KeyColumnUsageOID is a reserved native identity, not a PostgreSQL allocation.
const KeyColumnUsageOID ids.OID = 16204

// Capture reads the paired immutable catalog/role roots under Engine.mu. Key
// positions follow the actual constraint vectors and supporting index identity;
// visibility never renumbers a partially visible composite key.
func captureKeyColumnUsage(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, KeyColumnUsageOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 9 {
		return nil, fmt.Errorf("stuntgres: invalid key_column_usage source")
	}
	for i, column := range in.Source.Columns {
		domain, base := sqltype.ID(13248), sqltype.Name
		if i >= 7 {
			domain, base = 13243, sqltype.Int4
		}
		typ := column.Usage.Type
		if typ == nil || !typ.IsDomain() || typ.ID() != domain || typ.BaseType().ID() != base {
			return nil, fmt.Errorf("stuntgres: invalid key_column_usage domain")
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
		if len(table.Constraints.Unique) == 0 && len(table.Constraints.ForeignKeys) == 0 {
			continue
		}
		ns, ok := in.Catalog.Namespace(table.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing key_column_usage namespace")
		}
		if ns.Temporary != (ids.SessionToken{}) && ns.Temporary != in.Runtime.TemporaryOwner() {
			continue
		}
		owner, err := in.Roles.InheritsRole(in.Runtime.CurrentPrincipal(), table.Owner)
		if err != nil {
			return nil, err
		}
		emit := func(name string, attributes []int32, fk *catalog.ForeignKeyConstraint) error {
			if err := in.Context.Err(); err != nil {
				return err
			}
			var target, index *catalog.Relation
			if fk != nil {
				if len(fk.Referenced) != len(attributes) {
					return fmt.Errorf("stuntgres: invalid key_column_usage foreign key vector")
				}
				var ok bool
				target, ok = in.Catalog.Relation(fk.Target)
				if !ok {
					return fmt.Errorf("stuntgres: missing key_column_usage target")
				}
				index, ok = in.Catalog.Relation(fk.Index)
				if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != target.Address() {
					return fmt.Errorf("stuntgres: invalid key_column_usage supporting index")
				}
			}
			for i, attribute := range attributes {
				if err := in.Context.Err(); err != nil {
					return err
				}
				slot := table.IndexForAttribute(attribute)
				if slot < 0 {
					continue
				}
				if !owner {
					address := table.Address()
					address.SubID = attribute
					rights, err := in.Runtime.Effective(in.Ref.Domain().Ref(in.Ref.Epoch, address), false)
					if err != nil {
						return err
					}
					if rights&(authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.ReferencesPrivilege) == 0 {
						continue
					}
				}
				position := int64(0)
				if fk != nil {
					referenced := fk.Referenced[i]
					if target.IndexForAttribute(referenced) < 0 {
						return fmt.Errorf("stuntgres: invalid key_column_usage referenced attribute")
					}
					for j, key := range index.SQLIndex.Keys {
						if err := in.Context.Err(); err != nil {
							return err
						}
						if attr, ok := in.Runtime.IndexSimpleAttribute(target.Address(), key); ok && attr == referenced {
							position = int64(j + 1)
							break
						}
					}
					if position == 0 {
						return fmt.Errorf("stuntgres: missing key_column_usage index position")
					}
				}
				row := make([]sqltype.Value, 9)
				names := [7]string{database, ns.Name(), name, database, ns.Name(), table.Name(), table.Columns[slot].Name}
				for j, name := range names {
					typ := in.Source.Columns[j].Usage.Type
					value, err := typ.Wrap(sqltype.String(typ.BaseType(), name))
					if err != nil {
						return err
					}
					row[j] = value
				}
				for j, n := range [2]int64{int64(i + 1), position} {
					typ := in.Source.Columns[j+7].Usage.Type
					value := sqltype.Null(typ.BaseType())
					if j == 0 || fk != nil {
						var err error
						value, err = sqltype.Integer(typ.BaseType(), n)
						if err != nil {
							return err
						}
					}
					wrapped, err := typ.Wrap(value)
					if err != nil {
						return err
					}
					row[j+7] = wrapped
				}
				rows = append(rows, row)
			}
			return nil
		}
		for _, key := range table.Constraints.Unique {
			if err := emit(key.Name, key.Attributes, nil); err != nil {
				return nil, err
			}
		}
		for i := range table.Constraints.ForeignKeys {
			key := &table.Constraints.ForeignKeys[i]
			if err := emit(key.Name, key.Attributes, key); err != nil {
				return nil, err
			}
		}
	}
	return rows, nil
}

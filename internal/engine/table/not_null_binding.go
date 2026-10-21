package table

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

func NameCreateNotNull(d *catalog.Relation, read catalog.Read) error {
	if len(d.Constraints.NotNull) == 0 {
		return nil
	}
	used := catalog.ConstraintNames(read, d.Namespace(), nil)
	// A local false entry was generated; true entries were explicitly named.
	local := make(map[string]bool, len(d.Constraints.NotNull))
	for i := range d.Constraints.NotNull {
		constraint := &d.Constraints.NotNull[i]
		if constraint.Name == "" {
			constraint.Name = catalog.GeneratedConstraintName(d.Name(), d.Columns[constraint.Attribute-1].Name, "not_null", used)
			local[constraint.Name] = false
		} else {
			if explicit, exists := local[constraint.Name]; exists {
				if explicit {
					return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, constraint.Name, d.Name()))
				}
				const index = "pg_constraint_conrelid_contypid_conname_index"
				diagnostic := sqlError("23505", `duplicate key value violates unique constraint "`+index+`"`).(*sqlstate.Error)
				diagnostic.Detail = fmt.Sprintf("Key (conrelid, contypid, conname)=(%d, 0, %s) already exists.", d.Address().ObjectID, constraint.Name)
				diagnostic.Schema, diagnostic.Table, diagnostic.Constraint = "pg_catalog", "pg_constraint", index
				return diagnostic
			}
			local[constraint.Name] = true
		}
		used[constraint.Name] = true
	}
	return nil
}

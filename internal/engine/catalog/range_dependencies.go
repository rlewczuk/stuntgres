package catalog

import "stuntgres/internal/engine/ids"

// RangeDependencies projects the descriptor's owned definitions onto the common
// dependency graph. A generated companion is internally owned by its range.
func RangeDependencies(t *Type) []ids.ObjectAddress {
	if t.SQLType.IsMultirange() {
		if _, ok := t.SQLType.RangeType().RangeDefinition(); ok {
			return []ids.ObjectAddress{{ClassID: ids.TypeClassID, ObjectID: ids.OID(t.SQLType.RangeType().ID())}}
		}
		return nil
	}
	d, ok := t.SQLType.RangeDefinition()
	if !ok {
		return nil
	}
	dependencies := []ids.ObjectAddress{{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Subtype.Type.ID())}, {ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(d.Class.OID())}}
	if d.Subtype.Collation.ID != 0 {
		dependencies = append(dependencies, ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(d.Subtype.Collation.ID)})
	}
	if d.Diff != 0 {
		dependencies = append(dependencies, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(d.Diff)})
	}
	return dependencies
}

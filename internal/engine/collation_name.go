package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func CollationName(view catalog.Read, id uint32) string {
	if view.State() != nil {
		if d, ok := view.Collation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(id)}); ok {
			return d.Name()
		}
	}
	for i := range catalog.BuiltinCollations {
		if uint32(catalog.BuiltinCollations[i].OID) == id {
			return catalog.BuiltinCollations[i].Name()
		}
	}
	return ""
}

func CollationSQLName(view catalog.Read, id uint32) string {
	if view.State() != nil {
		if d, ok := view.Collation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(id)}); ok {
			return catalog.QuoteIdentifier(view.Get(d.Namespace()).Name()) + "." + catalog.QuoteIdentifier(d.Name())
		}
	}
	if name := CollationName(view, id); name != "" {
		return catalog.QuoteIdentifier("pg_catalog") + "." + catalog.QuoteIdentifier(name)
	}
	return ""
}

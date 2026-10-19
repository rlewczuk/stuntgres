package access

import (
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// The diagnostic name helpers below copy the engine's dependencyIdentifier,
// typeDiagnosticNameAt and quoteIdentifier; keep the copies identical.

// PostgreSQL 18 quote_identifier quotes every keyword except UNRESERVED.
// Keep this independent of the intentionally much smaller supported grammar.
func dependencyIdentifier(name string) string {
	safe := name != ""
	for i := range len(name) {
		c := name[i]
		if !(c == '_' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			safe = false
			break
		}
	}
	if !safe {
		return quoteIdentifier(name)
	}
	switch name {
	case "all", "analyse", "analyze", "and", "any", "array", "as", "asc", "asymmetric",
		"authorization", "between", "bigint", "binary", "bit", "boolean", "both",
		"case", "cast", "char", "character", "check", "coalesce", "collate", "collation",
		"column", "concurrently", "constraint", "create", "cross", "current_catalog",
		"current_date", "current_role", "current_schema", "current_time",
		"current_timestamp", "current_user", "dec", "decimal", "default", "deferrable",
		"desc", "distinct", "do", "else", "end", "except", "exists", "extract", "false",
		"fetch", "float", "for", "foreign", "freeze", "from", "full", "grant", "greatest",
		"group", "grouping", "having", "ilike", "in", "initially", "inner", "inout",
		"int", "integer", "intersect", "interval", "into", "is", "isnull", "join",
		"json", "json_array", "json_arrayagg", "json_exists", "json_object",
		"json_objectagg", "json_query", "json_scalar", "json_serialize", "json_table",
		"json_value", "lateral", "leading", "least", "left", "like", "limit",
		"localtime", "localtimestamp", "merge_action", "national", "natural", "nchar",
		"none", "normalize", "not", "notnull", "null", "nullif", "numeric", "offset",
		"on", "only", "or", "order", "out", "outer", "overlaps", "overlay", "placing",
		"position", "precision", "primary", "real", "references", "returning", "right",
		"row", "select", "session_user", "setof", "similar", "smallint", "some",
		"substring", "symmetric", "system_user", "table", "tablesample", "then", "time",
		"timestamp", "to", "trailing", "treat", "trim", "true", "union", "unique",
		"user", "using", "values", "varchar", "variadic", "verbose", "when", "where",
		"window", "with", "xmlattributes", "xmlconcat", "xmlelement", "xmlexists",
		"xmlforest", "xmlnamespaces", "xmlparse", "xmlpi", "xmlroot", "xmlserialize", "xmltable":
		return quoteIdentifier(name)
	default:
		return name
	}
}

func typeDiagnosticNameAt(view catalog.Read, d *catalog.Type, path []ids.ObjectAddress) string {
	if d.SQLType.IsArray() {
		element, ok := view.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.Element().ID())})
		if ok {
			return typeDiagnosticNameAt(view, element.(*catalog.Type), path) + "[]"
		}
	}
	name := dependencyIdentifier(d.Name())
	if path == nil {
		return name
	}
	for _, ns := range path {
		if address, ok := view.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ns, Name: d.Name()}); ok {
			if address == d.Address() {
				return name
			}
			break
		}
	}
	return dependencyIdentifier(view.Get(d.Namespace()).Name()) + "." + name
}

func quoteIdentifier(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

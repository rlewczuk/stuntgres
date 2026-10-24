package catalog

import (
	"strings"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Diagnostic name helpers shared by the engine and access.

// DependencyIdentifier quotes like PostgreSQL 18 quote_identifier: every
// keyword except UNRESERVED is quoted. Keep this independent of the
// intentionally much smaller supported grammar.
func DependencyIdentifier(name string) string {
	safe := name != ""
	for i := range len(name) {
		c := name[i]
		if !(c == '_' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			safe = false
			break
		}
	}
	if !safe {
		return QuoteIdentifier(name)
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
		return QuoteIdentifier(name)
	default:
		return name
	}
}

// TypeDiagnosticNameAt names a type for error messages: bare when path finds
// it first (or path is nil), schema-qualified otherwise; arrays get "[]".
func TypeDiagnosticNameAt(view Read, reader DefinitionReader, d *Type, path []ids.ObjectAddress) string {
	if d.SQLType.IsArray() {
		element, ok := DescriptorAt(view, reader, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.Element().ID())})
		if ok {
			return TypeDiagnosticNameAt(view, reader, element.(*Type), path) + "[]"
		}
	}
	// PostgreSQL format_type uses SQL spellings for these builtin types,
	// independently of namespace visibility, rather than quoting catalog names
	// such as int4. Other types still follow the ordinary visible-name rule.
	switch d.SQLType.ID() {
	case sqltype.Bool, sqltype.Int2, sqltype.Int4, sqltype.Int8,
		sqltype.Float4, sqltype.Float8, sqltype.Numeric,
		sqltype.BPChar, sqltype.VarChar, sqltype.Bit, sqltype.VarBit,
		sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz,
		sqltype.Interval:
		return d.SQLType.DiagnosticName()
	}
	name := DependencyIdentifier(d.Name())
	if path == nil {
		return name
	}
	for _, ns := range path {
		if address := DefinitionLookupAt(view, reader, ids.NameKey{ClassID: ids.TypeClassID, Namespace: ns, Name: d.Name()}); address != (ids.ObjectAddress{}) {
			if address == d.Address() {
				return name
			}
			break
		}
	}
	namespace, _ := DefinitionNamespaceAt(view, reader, d.Namespace())
	return DependencyIdentifier(namespace.Name()) + "." + name
}

// Identifier quotes name only when the supported grammar would not read it
// back unquoted (parser.IdentifierNeedsQuotes).
func Identifier(name string) string {
	if parser.IdentifierNeedsQuotes(name) {
		return QuoteIdentifier(name)
	}
	return name
}

// QuoteIdentifier always double-quotes s, doubling embedded quotes.
func QuoteIdentifier(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// TypeDiagnosticNamer freezes input and demanded-comparison type naming
// against view and the reference path used for binding; providers consult it
// on failure. Unknown types name as "".
func TypeDiagnosticNamer(view Read, path []ids.ObjectAddress) func(sqltype.ID) string {
	return func(id sqltype.ID) string {
		descriptor, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)})
		if !ok {
			return ""
		}
		if descriptor.SQLType.NamespaceName() == "pg_catalog" {
			// Released diagnostics use SQL aliases, not catalog spellings such
			// as int4, and do not identifier-quote builtin names such as json.
			return descriptor.SQLType.DiagnosticName()
		}
		return TypeDiagnosticNameAt(view, nil, descriptor, path)
	}
}

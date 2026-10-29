package pgcatalog

// REL_18_6 system_views.sql: presentation fragments only. The registered native
// producer owns effective membership and current attribute/filter projection.
func publicationTablesViewParts() []builtinViewPart {
	return []builtinViewPart{
		viewText(" SELECT "), viewIdentifier("p"), viewText("."), viewIdentifier("pubname"), viewText(",\n"),
		viewText("    "), viewIdentifier("n"), viewText("."), viewIdentifier("nspname"), viewText(" AS "), viewIdentifier("schemaname"), viewText(",\n"),
		viewText("    "), viewIdentifier("c"), viewText("."), viewIdentifier("relname"), viewText(" AS "), viewIdentifier("tablename"), viewText(",\n"),
		viewText("    ( SELECT "), viewRoutine("array_agg", "2776"), viewText("("), viewIdentifier("a"), viewText("."), viewIdentifier("attname"), viewText(" ORDER BY "), viewIdentifier("a"), viewText("."), viewIdentifier("attnum"), viewText(") AS "), viewIdentifier("array_agg"), viewText("\n"),
		viewText("           FROM "), viewClass("pg_attribute"), viewText(" "), viewIdentifier("a"), viewText("\n"),
		viewText("          WHERE (("), viewIdentifier("a"), viewText("."), viewIdentifier("attrelid"), viewText(" = "), viewIdentifier("gpt"), viewText("."), viewIdentifier("relid"), viewText(") AND ("), viewIdentifier("a"), viewText("."), viewIdentifier("attnum"), viewText(" = ANY ("), viewIdentifier("gpt"), viewText("."), viewIdentifier("attrs"), viewText(")))) AS "), viewIdentifier("attnames"), viewText(",\n"),
		viewText("    "), viewRoutine("pg_get_expr", "194,26"), viewText("("), viewIdentifier("gpt"), viewText("."), viewIdentifier("qual"), viewText(", "), viewIdentifier("gpt"), viewText("."), viewIdentifier("relid"), viewText(") AS "), viewIdentifier("rowfilter"), viewText("\n"),
		viewText("   FROM "), viewClass("pg_publication"), viewText(" "), viewIdentifier("p"), viewText(",\n"),
		viewText("    LATERAL "), viewRoutine("pg_get_publication_tables", "1009"), viewText("(VARIADIC ARRAY[("), viewIdentifier("p"), viewText("."), viewIdentifier("pubname"), viewText(")::"), viewType("text"), viewText("]) "), viewIdentifier("gpt"), viewText("("), viewIdentifier("pubid"), viewText(", "), viewIdentifier("relid"), viewText(", "), viewIdentifier("attrs"), viewText(", "), viewIdentifier("qual"), viewText("),\n"),
		viewText("    ("), viewClass("pg_class"), viewText(" "), viewIdentifier("c"), viewText("\n"),
		viewText("     JOIN "), viewClass("pg_namespace"), viewText(" "), viewIdentifier("n"), viewText(" ON (("), viewIdentifier("n"), viewText("."), viewIdentifier("oid"), viewText(" = "), viewIdentifier("c"), viewText("."), viewIdentifier("relnamespace"), viewText(")))\n"),
		viewText("  WHERE ("), viewIdentifier("c"), viewText("."), viewIdentifier("oid"), viewText(" = "), viewIdentifier("gpt"), viewText("."), viewIdentifier("relid"), viewText(");"),
	}
}

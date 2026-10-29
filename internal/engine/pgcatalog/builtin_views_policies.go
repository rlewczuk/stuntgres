package pgcatalog

// REL_18_6 system_views.sql, with the released default/pretty ruleutils layout.
// Every object and identifier reference uses the common display inventory.
func policiesViewParts(pretty bool) []builtinViewPart {
	parts := []builtinViewPart{
		viewText(" SELECT "), viewIdentifier("n"), viewText("."), viewIdentifier("nspname"), viewText(" AS "), viewIdentifier("schemaname"), viewText(",\n    "),
		viewIdentifier("c"), viewText("."), viewIdentifier("relname"), viewText(" AS "), viewIdentifier("tablename"), viewText(",\n    "),
		viewIdentifier("pol"), viewText("."), viewIdentifier("polname"), viewText(" AS "), viewIdentifier("policyname"), viewText(",\n        CASE\n            WHEN "),
		viewIdentifier("pol"), viewText("."), viewIdentifier("polpermissive"), viewText(" THEN 'PERMISSIVE'::"), viewType("text"), viewText("\n            ELSE 'RESTRICTIVE'::"), viewType("text"),
		viewText("\n        END AS "), viewIdentifier("permissive"), viewText(",\n        CASE\n            WHEN "),
	}
	if !pretty {
		parts = append(parts, viewText("("))
	}
	parts = append(parts, viewIdentifier("pol"), viewText("."), viewIdentifier("polroles"), viewText(" = '{0}'::"), viewType("oid"), viewText("[]"))
	if !pretty {
		parts = append(parts, viewText(")"))
	}
	parts = append(parts, viewText(" THEN "))
	if !pretty {
		parts = append(parts, viewText("("))
	}
	parts = append(parts, viewRoutine("string_to_array", "25,25"), viewText("('public'::"), viewType("text"), viewText(", ''::"), viewType("text"), viewText(")"))
	if !pretty {
		parts = append(parts, viewText(")"))
	}
	parts = append(parts, viewText("::"), viewType("name"), viewText("[]\n            ELSE ARRAY( SELECT "), viewIdentifier("pg_authid"), viewText("."), viewIdentifier("rolname"), viewText("\n               FROM "), viewClass("pg_authid"), viewText("\n              WHERE "))
	if !pretty {
		parts = append(parts, viewText("("))
	}
	parts = append(parts, viewIdentifier("pg_authid"), viewText("."), viewIdentifier("oid"), viewText(" = ANY ("), viewIdentifier("pol"), viewText("."), viewIdentifier("polroles"), viewText(")"))
	if !pretty {
		parts = append(parts, viewText(")"))
	}
	parts = append(parts, viewText("\n              ORDER BY "), viewIdentifier("pg_authid"), viewText("."), viewIdentifier("rolname"), viewText(")\n        END AS "), viewIdentifier("roles"), viewText(",\n        CASE "), viewIdentifier("pol"), viewText("."), viewIdentifier("polcmd"))
	for _, command := range [...]struct{ code, label string }{{"r", "SELECT"}, {"a", "INSERT"}, {"w", "UPDATE"}, {"d", "DELETE"}, {"*", "ALL"}} {
		parts = append(parts, viewText("\n            WHEN '"+command.code+"'::"), viewType("\"char\""), viewText(" THEN '"+command.label+"'::"), viewType("text"))
	}
	parts = append(parts, viewText("\n            ELSE NULL::"), viewType("text"), viewText("\n        END AS "), viewIdentifier("cmd"), viewText(",\n    "),
		viewRoutine("pg_get_expr", "194,26"), viewText("("), viewIdentifier("pol"), viewText("."), viewIdentifier("polqual"), viewText(", "), viewIdentifier("pol"), viewText("."), viewIdentifier("polrelid"), viewText(") AS "), viewIdentifier("qual"), viewText(",\n    "),
		viewRoutine("pg_get_expr", "194,26"), viewText("("), viewIdentifier("pol"), viewText("."), viewIdentifier("polwithcheck"), viewText(", "), viewIdentifier("pol"), viewText("."), viewIdentifier("polrelid"), viewText(") AS "), viewIdentifier("with_check"), viewText("\n   FROM "))
	if !pretty {
		parts = append(parts, viewText("(("))
	}
	parts = append(parts, viewClass("pg_policy"), viewText(" "), viewIdentifier("pol"), viewText("\n     JOIN "), viewClass("pg_class"), viewText(" "), viewIdentifier("c"), viewText(" ON "))
	if !pretty {
		parts = append(parts, viewText("(("))
	}
	parts = append(parts, viewIdentifier("c"), viewText("."), viewIdentifier("oid"), viewText(" = "), viewIdentifier("pol"), viewText("."), viewIdentifier("polrelid"))
	if !pretty {
		parts = append(parts, viewText(")))"))
	}
	parts = append(parts, viewText("\n     LEFT JOIN "), viewClass("pg_namespace"), viewText(" "), viewIdentifier("n"), viewText(" ON "))
	if !pretty {
		parts = append(parts, viewText("(("))
	}
	parts = append(parts, viewIdentifier("n"), viewText("."), viewIdentifier("oid"), viewText(" = "), viewIdentifier("c"), viewText("."), viewIdentifier("relnamespace"))
	if !pretty {
		parts = append(parts, viewText(")))"))
	}
	return append(parts, viewText(";"))
}

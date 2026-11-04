package pgcatalog

// Released LEFT JOIN definition; display does not expose callable getters.
func functionStatisticsViewParts(pretty bool) []builtinViewPart {
	parts := []builtinViewPart{
		viewText(" SELECT "), viewIdentifier("p"), viewText("."), viewIdentifier("oid"), viewText(" AS "), viewIdentifier("funcid"), viewText(",\n"),
		viewText("    "), viewIdentifier("n"), viewText("."), viewIdentifier("nspname"), viewText(" AS "), viewIdentifier("schemaname"), viewText(",\n"),
		viewText("    "), viewIdentifier("p"), viewText("."), viewIdentifier("proname"), viewText(" AS "), viewIdentifier("funcname"), viewText(",\n"),
	}
	for i, field := range []struct{ routine, column string }{
		{"pg_stat_get_function_calls", "calls"},
		{"pg_stat_get_function_total_time", "total_time"},
		{"pg_stat_get_function_self_time", "self_time"},
	} {
		parts = append(parts, viewText("    "), viewRoutine(field.routine, "26"), viewText("("), viewIdentifier("p"), viewText("."), viewIdentifier("oid"), viewText(") AS "), viewIdentifier(field.column))
		if i < 2 {
			parts = append(parts, viewText(",\n"))
		} else {
			parts = append(parts, viewText("\n"))
		}
	}
	if pretty {
		return append(parts,
			viewText("   FROM "), viewClass("pg_proc"), viewText(" "), viewIdentifier("p"), viewText("\n"),
			viewText("     LEFT JOIN "), viewClass("pg_namespace"), viewText(" "), viewIdentifier("n"), viewText(" ON "), viewIdentifier("n"), viewText("."), viewIdentifier("oid"), viewText(" = "), viewIdentifier("p"), viewText("."), viewIdentifier("pronamespace"), viewText("\n"),
			viewText("  WHERE "), viewIdentifier("p"), viewText("."), viewIdentifier("prolang"), viewText(" <> 12::"), viewType("oid"), viewText(" AND "), viewRoutine("pg_stat_get_function_calls", "26"), viewText("("), viewIdentifier("p"), viewText("."), viewIdentifier("oid"), viewText(") IS NOT NULL;"),
		)
	}
	parts = append(parts,
		viewText("   FROM ("), viewClass("pg_proc"), viewText(" "), viewIdentifier("p"), viewText("\n"),
		viewText("     LEFT JOIN "), viewClass("pg_namespace"), viewText(" "), viewIdentifier("n"), viewText(" ON (("), viewIdentifier("n"), viewText("."), viewIdentifier("oid"), viewText(" = "), viewIdentifier("p"), viewText("."), viewIdentifier("pronamespace"), viewText(")))\n"),
	)
	return append(parts, viewText("  WHERE (("), viewIdentifier("p"), viewText("."), viewIdentifier("prolang"), viewText(" <> (12)::"), viewType("oid"), viewText(") AND ("), viewRoutine("pg_stat_get_function_calls", "26"), viewText("("), viewIdentifier("p"), viewText("."), viewIdentifier("oid"), viewText(") IS NOT NULL));"))
}
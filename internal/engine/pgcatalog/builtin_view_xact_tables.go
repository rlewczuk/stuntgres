package pgcatalog

// REL_18_6 system_views.sql: the grouped display has the same dependency-aware
// identifier/routine rendering as other builtin views. It does not add getters.
func xactTableStatisticsViewParts() []builtinViewPart {
	parts := []builtinViewPart{
		viewText(" SELECT "), viewIdentifier("c"), viewText("."), viewIdentifier("oid"), viewText(" AS "), viewIdentifier("relid"), viewText(",\n"),
		viewText("    "), viewIdentifier("n"), viewText("."), viewIdentifier("nspname"), viewText(" AS "), viewIdentifier("schemaname"), viewText(",\n"),
		viewText("    "), viewIdentifier("c"), viewText("."), viewIdentifier("relname"), viewText(",\n"),
		viewText("    "), viewRoutine("pg_stat_get_xact_numscans", "26"), viewText("("), viewIdentifier("c"), viewText("."), viewIdentifier("oid"), viewText(") AS "), viewIdentifier("seq_scan"), viewText(",\n"),
		viewText("    "), viewRoutine("pg_stat_get_xact_tuples_returned", "26"), viewText("("), viewIdentifier("c"), viewText("."), viewIdentifier("oid"), viewText(") AS "), viewIdentifier("seq_tup_read"), viewText(",\n"),
		viewText("    ("), viewRoutine("sum", "20"), viewText("("), viewRoutine("pg_stat_get_xact_numscans", "26"), viewText("("), viewIdentifier("i"), viewText("."), viewIdentifier("indexrelid"), viewText(")))::bigint AS "), viewIdentifier("idx_scan"), viewText(",\n"),
		viewText("    (("), viewRoutine("sum", "20"), viewText("("), viewRoutine("pg_stat_get_xact_tuples_fetched", "26"), viewText("("), viewIdentifier("i"), viewText("."), viewIdentifier("indexrelid"), viewText(")))::bigint + "), viewRoutine("pg_stat_get_xact_tuples_fetched", "26"), viewText("("), viewIdentifier("c"), viewText("."), viewIdentifier("oid"), viewText(")) AS "), viewIdentifier("idx_tup_fetch"), viewText(",\n"),
	}
	for i, field := range []struct{ routine, column string }{
		{"pg_stat_get_xact_tuples_inserted", "n_tup_ins"},
		{"pg_stat_get_xact_tuples_updated", "n_tup_upd"},
		{"pg_stat_get_xact_tuples_deleted", "n_tup_del"},
		{"pg_stat_get_xact_tuples_hot_updated", "n_tup_hot_upd"},
		{"pg_stat_get_xact_tuples_newpage_updated", "n_tup_newpage_upd"},
	} {
		parts = append(parts, viewText("    "), viewRoutine(field.routine, "26"), viewText("("), viewIdentifier("c"), viewText("."), viewIdentifier("oid"), viewText(") AS "), viewIdentifier(field.column))
		if i < 4 {
			parts = append(parts, viewText(",\n"))
		} else {
			parts = append(parts, viewText("\n"))
		}
	}
	return append(parts,
		viewText("   FROM (("), viewClass("pg_class"), viewText(" "), viewIdentifier("c"), viewText("\n"),
		viewText("     LEFT JOIN "), viewClass("pg_index"), viewText(" "), viewIdentifier("i"), viewText(" ON (("), viewIdentifier("c"), viewText("."), viewIdentifier("oid"), viewText(" = "), viewIdentifier("i"), viewText("."), viewIdentifier("indrelid"), viewText(")))\n"),
		viewText("     LEFT JOIN "), viewClass("pg_namespace"), viewText(" "), viewIdentifier("n"), viewText(" ON (("), viewIdentifier("n"), viewText("."), viewIdentifier("oid"), viewText(" = "), viewIdentifier("c"), viewText("."), viewIdentifier("relnamespace"), viewText(")))\n"),
		viewText("  WHERE ("), viewIdentifier("c"), viewText("."), viewIdentifier("relkind"), viewText(" = ANY (ARRAY['r'::"), viewType("\"char\""), viewText(", 't'::"), viewType("\"char\""), viewText(", 'm'::"), viewType("\"char\""), viewText(", 'p'::"), viewType("\"char\""), viewText("]))\n"),
		viewText("  GROUP BY "), viewIdentifier("c"), viewText("."), viewIdentifier("oid"), viewText(", "), viewIdentifier("n"), viewText("."), viewIdentifier("nspname"), viewText(", "), viewIdentifier("c"), viewText("."), viewIdentifier("relname"), viewText(";"),
	)
}

func sysTableStatisticsViewParts(columns []ColumnSpec, source string, pretty bool) []builtinViewPart {
	parts := make([]builtinViewPart, 1, 3*len(columns)+14)
	parts[0] = viewText(" SELECT ")
	for i, column := range columns {
		if i != 0 {
			parts = append(parts, viewText("    "))
		}
		parts = append(parts, viewIdentifier(column.Name))
		if i != len(columns)-1 {
			parts = append(parts, viewText(",\n"))
		} else {
			parts = append(parts, viewText("\n"))
		}
	}
	if pretty {
		return append(parts,
			viewText("   FROM "), viewClass(source), viewText("\n"),
			viewText("  WHERE ("), viewIdentifier("schemaname"), viewText(" = ANY (ARRAY['pg_catalog'::"), viewType("name"), viewText(", 'information_schema'::"), viewType("name"), viewText("])) OR "), viewIdentifier("schemaname"), viewText(" ~ '^pg_toast'::"), viewType("text"), viewText(";"),
		)
	}
	return append(parts,
		viewText("   FROM "), viewClass(source), viewText("\n"),
		viewText("  WHERE (("), viewIdentifier("schemaname"), viewText(" = ANY (ARRAY['pg_catalog'::"), viewType("name"), viewText(", 'information_schema'::"), viewType("name"), viewText("])) OR ("), viewIdentifier("schemaname"), viewText(" ~ '^pg_toast'::"), viewType("text"), viewText("));"),
	)
}

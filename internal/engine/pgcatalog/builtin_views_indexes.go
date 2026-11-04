package pgcatalog

// Both forms use the shared identity-aware display inventory. These are the
// released nine-column view, not executable getter declarations or extra views.
func indexStatisticsViewParts(pretty bool) []builtinViewPart {
	parts := []builtinViewPart{
		viewText(" SELECT "), viewIdentifier("c"), viewText("."), viewIdentifier("oid"), viewText(" AS "), viewIdentifier("relid"), viewText(",\n"),
		viewText("    "), viewIdentifier("i"), viewText("."), viewIdentifier("oid"), viewText(" AS "), viewIdentifier("indexrelid"), viewText(",\n"),
		viewText("    "), viewIdentifier("n"), viewText("."), viewIdentifier("nspname"), viewText(" AS "), viewIdentifier("schemaname"), viewText(",\n"),
		viewText("    "), viewIdentifier("c"), viewText("."), viewIdentifier("relname"), viewText(",\n"),
		viewText("    "), viewIdentifier("i"), viewText("."), viewIdentifier("relname"), viewText(" AS "), viewIdentifier("indexrelname"), viewText(",\n"),
		viewText("    "), viewRoutine("pg_stat_get_numscans", "26"), viewText("("), viewIdentifier("i"), viewText("."), viewIdentifier("oid"), viewText(") AS "), viewIdentifier("idx_scan"), viewText(",\n"),
		viewText("    "), viewRoutine("pg_stat_get_lastscan", "26"), viewText("("), viewIdentifier("i"), viewText("."), viewIdentifier("oid"), viewText(") AS "), viewIdentifier("last_idx_scan"), viewText(",\n"),
		viewText("    "), viewRoutine("pg_stat_get_tuples_returned", "26"), viewText("("), viewIdentifier("i"), viewText("."), viewIdentifier("oid"), viewText(") AS "), viewIdentifier("idx_tup_read"), viewText(",\n"),
		viewText("    "), viewRoutine("pg_stat_get_tuples_fetched", "26"), viewText("("), viewIdentifier("i"), viewText("."), viewIdentifier("oid"), viewText(") AS "), viewIdentifier("idx_tup_fetch"), viewText("\n"),
	}
	from, on, end, where, tail := "   FROM (((", " ON ((", ")))\n", "  WHERE (", "]));"
	if pretty {
		from, on, end, where, tail = "   FROM ", " ON ", "\n", "  WHERE ", "]);"
	}
	return append(parts,
		viewText(from), viewClass("pg_class"), viewText(" "), viewIdentifier("c"), viewText("\n"),
		viewText("     JOIN "), viewClass("pg_index"), viewText(" "), viewIdentifier("x"), viewText(on), viewIdentifier("c"), viewText("."), viewIdentifier("oid"), viewText(" = "), viewIdentifier("x"), viewText("."), viewIdentifier("indrelid"), viewText(end),
		viewText("     JOIN "), viewClass("pg_class"), viewText(" "), viewIdentifier("i"), viewText(on), viewIdentifier("i"), viewText("."), viewIdentifier("oid"), viewText(" = "), viewIdentifier("x"), viewText("."), viewIdentifier("indexrelid"), viewText(end),
		viewText("     LEFT JOIN "), viewClass("pg_namespace"), viewText(" "), viewIdentifier("n"), viewText(on), viewIdentifier("n"), viewText("."), viewIdentifier("oid"), viewText(" = "), viewIdentifier("c"), viewText("."), viewIdentifier("relnamespace"), viewText(end),
		viewText(where), viewIdentifier("c"), viewText("."), viewIdentifier("relkind"), viewText(" = ANY (ARRAY['r'::"), viewType("\"char\""), viewText(", 't'::"), viewType("\"char\""), viewText(", 'm'::"), viewType("\"char\""), viewText(tail),
	)
}

func sysIndexStatisticsViewParts(pretty bool) []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range indexStatisticsColumns {
		if i != 0 {
			parts = append(parts, viewText("    "))
		}
		parts = append(parts, viewIdentifier(column.Name))
		if i != len(indexStatisticsColumns)-1 {
			parts = append(parts, viewText(",\n"))
		} else {
			parts = append(parts, viewText("\n"))
		}
	}
	if pretty {
		return append(parts,
			viewText("   FROM "), viewClass("pg_stat_all_indexes"), viewText("\n"),
			viewText("  WHERE ("), viewIdentifier("schemaname"), viewText(" = ANY (ARRAY['pg_catalog'::"), viewType("name"), viewText(", 'information_schema'::"), viewType("name"), viewText("])) OR "), viewIdentifier("schemaname"), viewText(" ~ '^pg_toast'::"), viewType("text"), viewText(";"),
		)
	}
	return append(parts,
		viewText("   FROM "), viewClass("pg_stat_all_indexes"), viewText("\n"),
		viewText("  WHERE (("), viewIdentifier("schemaname"), viewText(" = ANY (ARRAY['pg_catalog'::"), viewType("name"), viewText(", 'information_schema'::"), viewType("name"), viewText("])) OR ("), viewIdentifier("schemaname"), viewText(" ~ '^pg_toast'::"), viewType("text"), viewText("));"),
	)
}

func userIndexStatisticsViewParts(pretty bool) []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range indexStatisticsColumns {
		if i != 0 {
			parts = append(parts, viewText("    "))
		}
		parts = append(parts, viewIdentifier(column.Name))
		if i != len(indexStatisticsColumns)-1 {
			parts = append(parts, viewText(",\n"))
		} else {
			parts = append(parts, viewText("\n"))
		}
	}
	if pretty {
		return append(parts,
			viewText("   FROM "), viewClass("pg_stat_all_indexes"), viewText("\n"),
			viewText("  WHERE ("), viewIdentifier("schemaname"), viewText(" <> ALL (ARRAY['pg_catalog'::"), viewType("name"), viewText(", 'information_schema'::"), viewType("name"), viewText("])) AND "), viewIdentifier("schemaname"), viewText(" !~ '^pg_toast'::"), viewType("text"), viewText(";"),
		)
	}
	return append(parts,
		viewText("   FROM "), viewClass("pg_stat_all_indexes"), viewText("\n"),
		viewText("  WHERE (("), viewIdentifier("schemaname"), viewText(" <> ALL (ARRAY['pg_catalog'::"), viewType("name"), viewText(", 'information_schema'::"), viewType("name"), viewText("])) AND ("), viewIdentifier("schemaname"), viewText(" !~ '^pg_toast'::"), viewType("text"), viewText("));"),
	)
}

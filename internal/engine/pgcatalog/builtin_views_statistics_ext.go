package pgcatalog

// REL_18_6 system_views.sql. Only explicit inventory fragments carry names;
// these definitions are display metadata, not executable backing SQL.
func extendedStatisticsViewParts(pretty bool) []builtinViewPart {
	parts := statisticsViewIdentityParts()
	parts = append(parts,
		viewText("    ( SELECT "), viewRoutine("array_agg", "2776"), viewText("("), viewIdentifier("a"), viewText("."), viewIdentifier("attname"), viewText(" ORDER BY "), viewIdentifier("a"), viewText("."), viewIdentifier("attnum"), viewText(") AS "), viewIdentifier("array_agg"), viewText("\n"))
	if pretty {
		parts = append(parts, viewText("           FROM "), viewRoutine("unnest", "2277"), viewText("("), viewIdentifier("s"), viewText("."), viewIdentifier("stxkeys"), viewText(") "), viewIdentifier("k"), viewText("("), viewIdentifier("k"), viewText(")\n"),
			viewText("             JOIN "), viewClass("pg_attribute"), viewText(" "), viewIdentifier("a"), viewText(" ON "), viewIdentifier("a"), viewText("."), viewIdentifier("attrelid"), viewText(" = "), viewIdentifier("s"), viewText("."), viewIdentifier("stxrelid"), viewText(" AND "), viewIdentifier("a"), viewText("."), viewIdentifier("attnum"), viewText(" = "), viewIdentifier("k"), viewText("."), viewIdentifier("k"), viewText(") AS "))
	} else {
		parts = append(parts, viewText("           FROM ("), viewRoutine("unnest", "2277"), viewText("("), viewIdentifier("s"), viewText("."), viewIdentifier("stxkeys"), viewText(") "), viewIdentifier("k"), viewText("("), viewIdentifier("k"), viewText(")\n"),
			viewText("             JOIN "), viewClass("pg_attribute"), viewText(" "), viewIdentifier("a"), viewText(" ON ((("), viewIdentifier("a"), viewText("."), viewIdentifier("attrelid"), viewText(" = "), viewIdentifier("s"), viewText("."), viewIdentifier("stxrelid"), viewText(") AND ("), viewIdentifier("a"), viewText("."), viewIdentifier("attnum"), viewText(" = "), viewIdentifier("k"), viewText("."), viewIdentifier("k"), viewText("))))) AS "))
	}
	parts = append(parts, viewIdentifier("attnames"), viewText(",\n"),
		viewText("    "), viewRoutine("pg_get_statisticsobjdef_expressions", "26"), viewText("("), viewIdentifier("s"), viewText("."), viewIdentifier("oid"), viewText(") AS "), viewIdentifier("exprs"), viewText(",\n"),
		viewText("    "), viewIdentifier("s"), viewText("."), viewIdentifier("stxkind"), viewText(" AS "), viewIdentifier("kinds"), viewText(",\n"))
	for _, field := range [][2]string{{"stxdinherit", "inherited"}, {"stxdndistinct", "n_distinct"}, {"stxddependencies", "dependencies"}} {
		parts = append(parts, viewText("    "), viewIdentifier("sd"), viewText("."), viewIdentifier(field[0]), viewText(" AS "), viewIdentifier(field[1]), viewText(",\n"))
	}
	for i, name := range []string{"most_common_vals", "most_common_val_nulls", "most_common_freqs", "most_common_base_freqs"} {
		parts = append(parts, viewText("    "), viewIdentifier("m"), viewText("."), viewIdentifier(name))
		if i != 3 {
			parts = append(parts, viewText(","))
		}
		parts = append(parts, viewText("\n"))
	}
	parts = append(parts, statisticsViewJoinParts(pretty, false)...)
	parts = append(parts, viewText("     LEFT JOIN LATERAL ( SELECT "))
	for i, field := range [][2]string{{"values", "most_common_vals"}, {"nulls", "most_common_val_nulls"}, {"frequency", "most_common_freqs"}, {"base_frequency", "most_common_base_freqs"}} {
		if i != 0 {
			parts = append(parts, viewText("            "))
		}
		identifier := viewIdentifier(field[0])
		if i == 0 {
			identifier.text = `"values"`
		}
		aggregate := viewRoutine("array_agg", "2277")
		if i >= 2 {
			aggregate = viewRoutine("array_agg", "2776")
		}
		parts = append(parts, aggregate, viewText("("), viewIdentifier("pg_mcv_list_items"), viewText("."), identifier, viewText(") AS "), viewIdentifier(field[1]))
		if i != 3 {
			parts = append(parts, viewText(","))
		}
		parts = append(parts, viewText("\n"))
	}
	quotedValues := viewIdentifier("values")
	quotedValues.text = `"values"`
	parts = append(parts, viewText("           FROM "), viewRoutine("pg_mcv_list_items", "5017"), viewText("("), viewIdentifier("sd"), viewText("."), viewIdentifier("stxdmcv"), viewText(") "), viewIdentifier("pg_mcv_list_items"), viewText("("), viewIdentifier("index"), viewText(", "), quotedValues, viewText(", "), viewIdentifier("nulls"), viewText(", "), viewIdentifier("frequency"), viewText(", "), viewIdentifier("base_frequency"), viewText(")) "), viewIdentifier("m"), viewText(" ON "))
	if !pretty {
		parts = append(parts, viewText("(("))
	}
	parts = append(parts, viewIdentifier("sd"), viewText("."), viewIdentifier("stxdmcv"), viewText(" IS NOT NULL"))
	if !pretty {
		parts = append(parts, viewText(")))"))
	}
	parts = append(parts, viewText("\n"))
	return append(parts, statisticsViewPredicateParts(pretty)...)
}

func expressionStatisticsViewParts(pretty bool) []builtinViewPart {
	parts := statisticsViewIdentityParts()
	parts = append(parts, viewText("    "), viewIdentifier("stat"), viewText("."), viewIdentifier("expr"), viewText(",\n"),
		viewText("    "), viewIdentifier("sd"), viewText("."), viewIdentifier("stxdinherit"), viewText(" AS "), viewIdentifier("inherited"), viewText(",\n"))
	for _, field := range [][2]string{{"stanullfrac", "null_frac"}, {"stawidth", "avg_width"}, {"stadistinct", "n_distinct"}} {
		parts = append(parts, viewText("    ("), viewIdentifier("stat"), viewText("."), viewIdentifier("a"), viewText(")."), viewIdentifier(field[0]), viewText(" AS "), viewIdentifier(field[1]), viewText(",\n"))
	}
	for i, field := range []struct {
		kind, source, name, fallback string
		scalar                       bool
	}{
		{"1", "stavalues", "most_common_vals", "anyarray", false},
		{"1", "stanumbers", "most_common_freqs", "real[]", false},
		{"2", "stavalues", "histogram_bounds", "anyarray", false},
		{"3", "stanumbers", "correlation", "real", true},
		{"4", "stavalues", "most_common_elems", "anyarray", false},
		{"4", "stanumbers", "most_common_elem_freqs", "real[]", false},
		{"5", "stanumbers", "elem_count_histogram", "real[]", false},
	} {
		parts = append(parts, viewText("        CASE\n"))
		for _, slot := range []string{"1", "2", "3", "4", "5"} {
			parts = append(parts, viewText("            WHEN "))
			if !pretty {
				parts = append(parts, viewText("("))
			}
			parts = append(parts, viewText("("), viewIdentifier("stat"), viewText("."), viewIdentifier("a"), viewText(")."), viewIdentifier("stakind"+slot), viewText(" = "+field.kind))
			if !pretty {
				parts = append(parts, viewText(")"))
			}
			parts = append(parts, viewText(" THEN ("), viewIdentifier("stat"), viewText("."), viewIdentifier("a"), viewText(")."), viewIdentifier(field.source+slot))
			if field.scalar {
				parts = append(parts, viewText("[1]"))
			}
			parts = append(parts, viewText("\n"))
		}
		parts = append(parts, viewText("            ELSE NULL::"))
		if field.fallback == "anyarray" {
			parts = append(parts, viewType("anyarray"))
		} else {
			parts = append(parts, viewText(field.fallback))
		}
		parts = append(parts, viewText("\n        END AS "), viewIdentifier(field.name))
		if i != 6 {
			parts = append(parts, viewText(","))
		}
		parts = append(parts, viewText("\n"))
	}
	parts = append(parts, statisticsViewJoinParts(pretty, true)...)
	parts = append(parts, viewText("     JOIN LATERAL ( SELECT "), viewRoutine("unnest", "2277"), viewText("("), viewRoutine("pg_get_statisticsobjdef_expressions", "26"), viewText("("), viewIdentifier("s"), viewText("."), viewIdentifier("oid"), viewText(")) AS "), viewIdentifier("expr"), viewText(",\n            "), viewRoutine("unnest", "2277"), viewText("("), viewIdentifier("sd"), viewText("."), viewIdentifier("stxdexpr"), viewText(") AS "), viewIdentifier("a"), viewText(") "), viewIdentifier("stat"), viewText(" ON "))
	if !pretty {
		parts = append(parts, viewText("(("))
	}
	parts = append(parts, viewIdentifier("stat"), viewText("."), viewIdentifier("expr"), viewText(" IS NOT NULL"))
	if !pretty {
		parts = append(parts, viewText(")))"))
	}
	parts = append(parts, viewText("\n"))
	return append(parts, statisticsViewPredicateParts(pretty)...)
}

func statisticsViewIdentityParts() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT "), viewIdentifier("cn"), viewText("."), viewIdentifier("nspname"), viewText(" AS "), viewIdentifier("schemaname"), viewText(",\n"),
		viewText("    "), viewIdentifier("c"), viewText("."), viewIdentifier("relname"), viewText(" AS "), viewIdentifier("tablename"), viewText(",\n"),
		viewText("    "), viewIdentifier("sn"), viewText("."), viewIdentifier("nspname"), viewText(" AS "), viewIdentifier("statistics_schemaname"), viewText(",\n"),
		viewText("    "), viewIdentifier("s"), viewText("."), viewIdentifier("stxname"), viewText(" AS "), viewIdentifier("statistics_name"), viewText(",\n"),
		viewText("    "), viewRoutine("pg_get_userbyid", "26"), viewText("("), viewIdentifier("s"), viewText("."), viewIdentifier("stxowner"), viewText(") AS "), viewIdentifier("statistics_owner"), viewText(",\n")}
	return parts
}

func statisticsViewJoinParts(pretty, expressions bool) []builtinViewPart {
	parts := []builtinViewPart{viewText("   FROM ")}
	if !pretty {
		parts = append(parts, viewText("((((("))
	}
	parts = append(parts, viewClass("pg_statistic_ext"), viewText(" "), viewIdentifier("s"), viewText("\n"))
	dataJoin := "     JOIN "
	if expressions {
		dataJoin = "     LEFT JOIN "
	}
	for _, join := range []struct{ prefix, relation, alias, left, leftField, right, rightField string }{
		{"     JOIN ", "pg_class", "c", "c", "oid", "s", "stxrelid"},
		{dataJoin, "pg_statistic_ext_data", "sd", "s", "oid", "sd", "stxoid"},
		{"     LEFT JOIN ", "pg_namespace", "cn", "cn", "oid", "c", "relnamespace"},
		{"     LEFT JOIN ", "pg_namespace", "sn", "sn", "oid", "s", "stxnamespace"},
	} {
		parts = append(parts, viewText(join.prefix), viewClass(join.relation), viewText(" "), viewIdentifier(join.alias), viewText(" ON "))
		if !pretty {
			parts = append(parts, viewText("(("))
		}
		parts = append(parts, viewIdentifier(join.left), viewText("."), viewIdentifier(join.leftField), viewText(" = "), viewIdentifier(join.right), viewText("."), viewIdentifier(join.rightField))
		if !pretty {
			parts = append(parts, viewText(")))"))
		}
		parts = append(parts, viewText("\n"))
	}
	return parts
}

func statisticsViewPredicateParts(pretty bool) []builtinViewPart {
	parts := []builtinViewPart{viewText("  WHERE ")}
	if !pretty {
		parts = append(parts, viewText("("))
	}
	parts = append(parts, viewRoutine("pg_has_role", "26,25"), viewText("("), viewIdentifier("c"), viewText("."), viewIdentifier("relowner"), viewText(", 'USAGE'::"), viewType("text"), viewText(") AND ("))
	if !pretty {
		parts = append(parts, viewText("("))
	}
	parts = append(parts, viewIdentifier("c"), viewText("."), viewIdentifier("relrowsecurity"), viewText(" = false"))
	if !pretty {
		parts = append(parts, viewText(")"))
	}
	parts = append(parts, viewText(" OR "))
	if !pretty {
		parts = append(parts, viewText("("))
	}
	parts = append(parts, viewText("NOT "), viewRoutine("row_security_active", "26"), viewText("("), viewIdentifier("c"), viewText("."), viewIdentifier("oid"), viewText("))"))
	if !pretty {
		parts = append(parts, viewText("))"))
	}
	return append(parts, viewText(";"))
}

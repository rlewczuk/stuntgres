package pgcatalog

import "strconv"

// REL_18_6 system_views.sql. Every identifier, routine, class and cast retains
// its structural identity for normal view/rule deparsing and search-path rules.
func analyzeProgressViewParts(pretty bool) []builtinViewPart {
	var p []builtinViewPart
	text := func(s string) { p = append(p, viewText(s)) }
	ident := func(s string) { p = append(p, viewIdentifier(s)) }
	field := func(alias, name string) { ident(alias); text("."); ident(name) }
	separator := func() { p = append(p, viewTargetSeparator()) }
	p = append(p, viewTargetsStart())
	field("s", "pid")
	separator()
	field("s", "datid")
	separator()
	field("d", "datname")
	separator()
	field("s", "relid")
	separator()
	text("    CASE ")
	field("s", "param1")
	text("\n")
	for i, phase := range []string{"initializing", "acquiring sample rows", "acquiring inherited sample rows", "computing statistics", "computing extended statistics", "finalizing analyze"} {
		text("            WHEN " + strconv.Itoa(i) + " THEN '" + phase + "'::")
		p = append(p, viewType("text"))
		text("\n")
	}
	text("            ELSE NULL::")
	p = append(p, viewType("text"))
	text("\n        END AS ")
	ident("phase")
	for i, name := range []string{"sample_blks_total", "sample_blks_scanned", "ext_stats_total", "ext_stats_computed", "child_tables_total", "child_tables_done"} {
		separator()
		field("s", "param"+strconv.Itoa(i+2))
		text(" AS ")
		ident(name)
	}
	separator()
	if !pretty {
		text("(")
	}
	field("s", "param8")
	if !pretty {
		text(")")
	}
	text("::")
	p = append(p, viewType("oid"))
	text(" AS ")
	ident("current_child_table_relid")
	separator()
	if !pretty {
		text("((")
	}
	field("s", "param9")
	if !pretty {
		text(")")
	}
	text("::")
	p = append(p, viewType("float8"))
	text(" / ")
	if !pretty {
		text("(")
	}
	text("1000000")
	if !pretty {
		text(")")
	}
	text("::")
	p = append(p, viewType("float8"))
	if !pretty {
		text(")")
	}
	text(" AS ")
	ident("delay_time")
	p = append(p, viewTargetsEnd())
	text("\n   FROM ")
	if !pretty {
		text("(")
	}
	p = append(p, viewRoutine("pg_stat_get_progress_info", "25"))
	text("('ANALYZE'::")
	p = append(p, viewType("text"))
	text(") ")
	ident("s")
	text("(")
	for i, name := range append([]string{"pid", "datid", "relid"}, progressParameterNames()...) {
		if i != 0 {
			text(", ")
		}
		ident(name)
	}
	text(")\n     LEFT JOIN ")
	p = append(p, viewClass("pg_database"))
	text(" ")
	ident("d")
	text(" ON ")
	if !pretty {
		text("((")
	}
	field("s", "datid")
	text(" = ")
	field("d", "oid")
	if !pretty {
		text(")))")
	}
	text(";")
	return p
}

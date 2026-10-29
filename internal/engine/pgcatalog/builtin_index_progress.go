package pgcatalog

import "strconv"

// Released REL_18_0 system_views.sql projection. Names remain structural so
// pg_get_viewdef resolves exact built-in object identities, like COPY progress.
func createIndexProgressViewParts(pretty bool) []builtinViewPart {
	var p []builtinViewPart
	text := func(s string) { p = append(p, viewText(s)) }
	ident := func(s string) { p = append(p, viewIdentifier(s)) }
	field := func(alias, name string) { ident(alias); text("."); ident(name) }
	text(" SELECT ")
	field("s", "pid")
	text(",\n    ")
	field("s", "datid")
	text(",\n    ")
	field("d", "datname")
	text(",\n    ")
	field("s", "relid")
	text(",\n    ")
	if !pretty {
		text("(")
	}
	field("s", "param7")
	if !pretty {
		text(")")
	}
	text("::")
	p = append(p, viewType("oid"))
	text(" AS ")
	ident("index_relid")
	text(",\n        CASE ")
	field("s", "param1")
	text("\n")
	for i, label := range []string{"CREATE INDEX", "CREATE INDEX CONCURRENTLY", "REINDEX", "REINDEX CONCURRENTLY"} {
		text("            WHEN " + strconv.Itoa(i+1) + " THEN '" + label + "'::")
		p = append(p, viewType("text"))
		text("\n")
	}
	text("            ELSE NULL::")
	p = append(p, viewType("text"))
	text("\n        END AS ")
	ident("command")
	text(",\n        CASE ")
	field("s", "param10")
	text("\n")
	phases := []string{"initializing", "waiting for writers before build", "", "waiting for writers before validation", "index validation: scanning index", "index validation: sorting tuples", "index validation: scanning table", "waiting for old snapshots", "waiting for readers before marking dead", "waiting for readers before dropping"}
	for i, label := range phases {
		text("            WHEN " + strconv.Itoa(i) + " THEN ")
		if i == 2 {
			if !pretty {
				text("(")
			}
			text("'building index'::")
			p = append(p, viewType("text"))
			text(" || COALESCE(")
			if !pretty {
				text("(")
			}
			text("': '::")
			p = append(p, viewType("text"))
			text(" || ")
			p = append(p, viewRoutine("pg_indexam_progress_phasename", "26,20"))
			text("(")
			if !pretty {
				text("(")
			}
			field("s", "param9")
			if !pretty {
				text(")")
			}
			text("::")
			p = append(p, viewType("oid"))
			text(", ")
			field("s", "param11")
			text(")")
			if !pretty {
				text(")")
			}
			text(", ''::")
			p = append(p, viewType("text"))
			text(")")
			if !pretty {
				text(")")
			}
		} else {
			text("'" + label + "'::")
			p = append(p, viewType("text"))
		}
		text("\n")
	}
	text("            ELSE NULL::")
	p = append(p, viewType("text"))
	text("\n        END AS ")
	ident("phase")
	for i, param := range []int{4, 5, 6, 16, 17, 12, 13, 14, 15} {
		text(",\n    ")
		field("s", "param"+strconv.Itoa(param))
		text(" AS ")
		ident(progressCreateIndexColumns[i+7].Name)
	}
	text("\n   FROM ")
	if !pretty {
		text("(")
	}
	p = append(p, viewRoutine("pg_stat_get_progress_info", "25"))
	text("('CREATE INDEX'::")
	p = append(p, viewType("text"))
	text(") ")
	ident("s")
	text("(")
	for i, name := range append([]string{"pid", "datid", "relid"}, progressParameterNames()...) {
		if i > 0 {
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

func progressParameterNames() []string {
	names := make([]string, 20)
	for i := range names {
		names[i] = "param" + strconv.Itoa(i+1)
	}
	return names
}

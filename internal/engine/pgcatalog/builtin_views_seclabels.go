package pgcatalog

// REL_18_6 system_views.sql:427-601 and rules.out's pg_seclabels entry.
// These are display references, not executable dependencies or a second label
// store. Visibility qualifies names; target ACLs and loaded providers do not
// filter the released view.
func seclabelsViewParts(pretty bool) []builtinViewPart {
	var parts []builtinViewPart
	text := func(s string) { parts = append(parts, viewText(s)) }
	ident := func(s string) { parts = append(parts, viewIdentifier(s)) }
	col := func(alias, name string) {
		ident(alias)
		text(".")
		ident(name)
	}
	typ := func(s string) { parts = append(parts, viewType(s)) }
	literal := func(s, t string) {
		text("'" + s + "'::")
		typ(t)
	}
	paren := func(s string) {
		if !pretty {
			text(s)
		}
	}
	quote := func(alias, name string) {
		parts = append(parts, viewRoutine("quote_ident", "25"))
		text("(")
		paren("(")
		col(alias, name)
		paren(")")
		text("::")
		typ("text")
		text(")")
	}
	visibleName := func(alias, name, visibility string, nested bool) {
		if nested {
			text("\n        CASE\n            WHEN ")
		} else {
			text("    CASE\n            WHEN ")
		}
		parts = append(parts, viewRoutine(visibility, "26"))
		text("(")
		col(alias, "oid")
		text(") THEN ")
		quote(alias, name)
		// isSimpleNode retains nested || parentheses even in pretty mode.
		text("\n            ELSE (")
		paren("(")
		quote("nsp", "nspname")
		text(" || ")
		literal(".", "text")
		text(") || ")
		quote(alias, name)
		paren(")")
		text("\n        END")
	}
	branches := [...]struct {
		catalog, alias, kind, name, namespace string
		shared                               bool
	}{
		{"pg_class", "rel", "", "relname", "relnamespace", false},
		{"pg_class", "rel", "column", "relname", "relnamespace", false},
		{"pg_proc", "pro", "", "proname", "pronamespace", false},
		{"pg_type", "typ", "", "typname", "typnamespace", false},
		{"pg_largeobject_metadata", "lom", "large object", "", "", false},
		{"pg_language", "lan", "language", "lanname", "", false},
		{"pg_namespace", "nsp", "schema", "nspname", "oid", false},
		{"pg_event_trigger", "evt", "event trigger", "evtname", "", false},
		{"pg_publication", "p", "publication", "pubname", "", false},
		{"pg_subscription", "s", "subscription", "subname", "", true},
		{"pg_database", "dat", "database", "datname", "", true},
		{"pg_tablespace", "spc", "tablespace", "spcname", "", true},
		{"pg_authid", "rol", "role", "rolname", "", true},
	}
	for i, b := range branches {
		if i != 0 {
			text("\nUNION ALL\n")
		}
		text(" SELECT ")
		col("l", "objoid")
		text(",\n    ")
		col("l", "classoid")
		text(",\n    ")
		if b.shared {
			// ruleutils prints the released 0::int4 constant without a cast.
			text("0 AS ")
			ident("objsubid")
		} else {
			col("l", "objsubid")
		}
		text(",\n    ")
		switch i {
		case 0:
			text("    CASE\n            WHEN ")
			paren("(")
			col("rel", "relkind")
			text(" = ANY (ARRAY[")
			literal("r", "\"char\"")
			text(", ")
			literal("p", "\"char\"")
			text("])")
			paren(")")
			text(" THEN ")
			literal("table", "text")
			for _, pair := range [...][2]string{{"v", "view"}, {"m", "materialized view"}, {"S", "sequence"}, {"f", "foreign table"}} {
				text("\n            WHEN ")
				paren("(")
				col("rel", "relkind")
				text(" = ")
				literal(pair[0], "\"char\"")
				paren(")")
				text(" THEN ")
				literal(pair[1], "text")
			}
			text("\n            ELSE NULL::")
			typ("text")
			text("\n        END")
		case 2:
			text("    CASE ")
			col("pro", "prokind")
			for _, pair := range [...][2]string{{"a", "aggregate"}, {"f", "function"}, {"p", "procedure"}, {"w", "window"}} {
				text("\n            WHEN ")
				literal(pair[0], "\"char\"")
				text(" THEN ")
				literal(pair[1], "text")
			}
			text("\n            ELSE NULL::")
			typ("text")
			text("\n        END")
		case 3:
			text("    CASE\n            WHEN ")
			paren("(")
			col("typ", "typtype")
			text(" = ")
			literal("d", "\"char\"")
			paren(")")
			text(" THEN ")
			literal("domain", "text")
			text("\n            ELSE ")
			literal("type", "text")
			text("\n        END")
		default:
			literal(b.kind, "text")
		}
		text(" AS ")
		ident("objtype")
		text(",\n    ")
		if b.namespace == "" {
			text("NULL::")
			typ("oid")
		} else {
			col(b.alias, b.namespace)
		}
		text(" AS ")
		ident("objnamespace")
		text(",\n    ")
		switch i {
		case 0, 1, 2, 3:
			if i == 1 {
				text("(")
				paren("(")
			}
			if i == 2 {
				text("((")
				paren("(")
			}
			visibility := "pg_table_is_visible"
			if i == 2 {
				visibility = "pg_function_is_visible"
			}
			if i == 3 {
				visibility = "pg_type_is_visible"
			}
			visibleName(b.alias, b.name, visibility, i == 1 || i == 2)
			if i == 1 {
				text(" || ")
				literal(".", "text")
				text(") || ")
				paren("(")
				col("att", "attname")
				paren(")")
				text("::")
				typ("text")
				paren(")")
			}
			if i == 2 {
				text(" || ")
				literal("(", "text")
				text(") || ")
				parts = append(parts, viewRoutine("pg_get_function_arguments", "26"))
				text("(")
				col("pro", "oid")
				text(")) || ")
				literal(")", "text")
				paren(")")
			}
		case 4:
			paren("(")
			col("l", "objoid")
			paren(")")
			text("::")
			typ("text")
		default:
			quote(b.alias, b.name)
		}
		text(" AS ")
		ident("objname")
		text(",\n    ")
		col("l", "provider")
		text(",\n    ")
		col("l", "label")
		text("\n   FROM ")
		joins := 1
		if i <= 3 {
			joins++
		}
		if i == 1 {
			joins++
		}
		for range joins {
			paren("(")
		}
		backing := "pg_seclabel"
		if b.shared {
			backing = "pg_shseclabel"
		}
		parts = append(parts, viewClass(backing))
		text(" ")
		ident("l")
		text("\n     JOIN ")
		parts = append(parts, viewClass(b.catalog))
		text(" ")
		ident(b.alias)
		text(" ON ")
		paren("((")
		if i == 4 {
			col("l", "objoid")
			text(" = ")
			col(b.alias, "oid")
		} else {
			paren("(")
			col("l", "classoid")
			text(" = ")
			col(b.alias, "tableoid")
			paren(")")
			text(" AND ")
			paren("(")
			col("l", "objoid")
			text(" = ")
			col(b.alias, "oid")
			paren(")")
		}
		paren(")))")
		if i == 1 {
			text("\n     JOIN ")
			parts = append(parts, viewClass("pg_attribute"))
			text(" ")
			ident("att")
			text(" ON ")
			paren("(((")
			col("rel", "oid")
			text(" = ")
			col("att", "attrelid")
			paren(")")
			text(" AND ")
			paren("(")
			col("l", "objsubid")
			text(" = ")
			col("att", "attnum")
			paren("))))")
		}
		if i <= 3 {
			text("\n     JOIN ")
			parts = append(parts, viewClass("pg_namespace"))
			text(" ")
			ident("nsp")
			text(" ON ")
			paren("((")
			col(b.alias, b.namespace)
			text(" = ")
			col("nsp", "oid")
			paren(")))")
		}
		if !b.shared {
			text("\n  WHERE ")
			paren("(")
			if i == 4 {
				paren("(")
				col("l", "classoid")
				text(" = ")
				paren("(")
				parts = append(parts, viewRegclass("pg_largeobject"))
				text("::")
				typ("regclass")
				paren(")")
				text("::")
				typ("oid")
				paren(")")
				text(" AND ")
				paren("(")
			}
			col("l", "objsubid")
			if i == 1 {
				text(" <> 0")
			} else {
				text(" = 0")
			}
			if i == 4 {
				paren(")")
			}
			paren(")")
		}
	}
	text(";")
	return parts
}

package pgcatalog

var activityViewDefinitionParts = activityViewParts()

// Display only: execution is the one shared-provider producer, not a second SQL
// implementation or an invented SQL-visible backing routine.
func activityViewParts() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range activityColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		alias, name := "s", column.Name
		if i == 1 {
			alias, name = "d", "datname"
		}
		if i == 5 {
			alias, name = "u", "rolname"
		}
		parts = append(parts, viewIdentifier(alias), viewText("."), viewIdentifier(name))
		if i == 5 {
			parts = append(parts, viewText(" AS "), viewIdentifier("usename"))
		}
	}
	return append(parts, viewText("\n   FROM (("), viewRoutine("pg_stat_get_activity", "23"), viewText("(NULL::"), viewType("integer"), viewText(") "), viewIdentifier("s"),
		viewText("\n     LEFT JOIN "), viewClass("pg_database"), viewText(" "), viewIdentifier("d"), viewText(" ON (("), viewIdentifier("s"), viewText("."), viewIdentifier("datid"), viewText(" = "), viewIdentifier("d"), viewText("."), viewIdentifier("oid"), viewText(")))\n     LEFT JOIN "), viewClass("pg_authid"), viewText(" "), viewIdentifier("u"), viewText(" ON (("), viewIdentifier("s"), viewText("."), viewIdentifier("usesysid"), viewText(" = "), viewIdentifier("u"), viewText("."), viewIdentifier("oid"), viewText(")));"))
}

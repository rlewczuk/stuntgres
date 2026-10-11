package fixture

func CheckViolation(table, name, detail string) Diagnostic {
	return Diagnostic{Code: "23514", Message: `new row for relation "` + table + `" violates check constraint "` + name + `"`, Detail: "Failing row contains (" + detail + ").", Schema: "public", Table: table, Constraint: name}
}

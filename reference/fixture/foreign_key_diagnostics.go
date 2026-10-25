package fixture

func ForeignKeyError(code, message, detail, table, constraint string) Diagnostic {
	d := Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail, Table: table, Constraint: constraint}
	if table != "" {
		d.Schema = "public"
	}
	return d
}

func ForeignKeyChild(table, constraint, detail string) Diagnostic {
	return ForeignKeyError("23503", `insert or update on table "`+table+`" violates foreign key constraint "`+constraint+`"`, detail, table, constraint)
}

func ForeignKeyParent(parent, child, constraint, detail string) Diagnostic {
	return ForeignKeyError("23503", `update or delete on table "`+parent+`" violates foreign key constraint "`+constraint+`" on table "`+child+`"`, detail, child, constraint)
}

func ForeignKeyFail(q string, d Diagnostic, prepared bool) Step {
	s := ErrorDiagnostic(q, d)
	s.Prepared, s.ErrorStage = prepared, "execute"
	return s
}

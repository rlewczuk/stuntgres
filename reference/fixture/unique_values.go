package fixture

import (
	"fmt"
)

func UniqueReferenceDuplicate(table, constraint, detail string) Diagnostic {
	d := ControlsReferenceDiagnostic("23505", fmt.Sprintf(`duplicate key value violates unique constraint "%s"`, constraint))
	d.Schema, d.Table, d.Constraint, d.Detail = "public", table, constraint, detail
	return d
}

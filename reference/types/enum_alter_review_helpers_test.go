package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
)

func enumAlterReviewLiteral(label string) string {
	return "'" + strings.ReplaceAll(label, "'", "''") + "'"
}

func enumAlterReviewUnsafe(label, typ string) fixture.Diagnostic {
	d := enumAlterDiagnostic("55P04", "unsafe use of new value \""+label+"\" of enum type "+typ)
	d.Hint = "New enum values must be committed before they can be used."
	return d
}

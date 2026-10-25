package reference_test

import (
	"fmt"
	"regexp"

	"stuntgres/reference/fixture"
)

// Complete PG18.0 records were discovered in root tests-1.md through tests-4.md.
// Domain spellings in this suite are either simple ASCII identifiers or quoted
// Unicode identifiers; the display name is separate from structured DataType.
var domainCheckSimpleName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

func domainCheckDiagnostic(domain, constraint string) fixture.Diagnostic {
	display := domain
	if !domainCheckSimpleName.MatchString(domain) {
		display = fmt.Sprintf("%q", domain)
	}
	want := fixture.ControlsReferenceDiagnostic("23514", fmt.Sprintf("value for domain domain_s.%s violates check constraint %q", display, constraint))
	want.Schema, want.DataType, want.Constraint = "domain_s", domain, constraint
	return want
}

func domainCheckFailure(query, domain, constraint string, rows, prepared bool, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(query, domainCheckDiagnostic(domain, constraint), args...)
	s.SelectRows, s.Prepared, s.ErrorStage = rows, prepared, "execute"
	return s
}

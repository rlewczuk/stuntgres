package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/table"
)

// ScopeDeparseInputs calls x.deparseInputs.
func ScopeDeparseInputs(x *StatementScope) table.DeparseInputs {
	return x.deparseInputs()
}

// ExpectedOf calls expectedOf.
func ExpectedOf(d *Description) analyze.Expected {
	return expectedOf(d)
}

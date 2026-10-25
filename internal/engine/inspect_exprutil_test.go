package engine

import (
	"stuntgres/internal/engine/table"
)

// ScopeDeparseInputs calls x.deparseInputs.
func ScopeDeparseInputs(x *StatementScope) table.DeparseInputs {
	return x.deparseInputs()
}

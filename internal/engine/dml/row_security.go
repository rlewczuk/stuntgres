package dml

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
)

// MutationMatches is ordered deliberately: the named-target OLD policy guard
// precedes user predicates and UPDATE assignments, including refreshed rows.
func MutationMatches(h Host, c *exec.Context, predicate expr.Expr) (bool, error) {
	pass, _, err := exec.EvaluateRowSecurity(h.Command().RLS.Old, c, c.Row)
	if err != nil || !pass {
		return false, err
	}
	return exec.UpdateMatches(c, predicate)
}

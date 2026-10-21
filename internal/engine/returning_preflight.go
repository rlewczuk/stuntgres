package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
)

func PreflightReturning(c *exec.Context, output *analyze.Returning) (*analyze.Returning, error) {
	var expressions []expr.Expr
	for i, bound := range output.Expressions {
		// Plan genuine SQL constants before importing frame coercions and before
		// tuple/index demand. Row-dependent output still executes after insertion
		// preparation; an imported parameter must not become a SQL constant.
		planned, changed, err := exec.SimplifyConditional(c, bound, false)
		if err != nil {
			return nil, err
		}
		folded, converted, err := exec.PreflightCoercions(c, planned)
		changed = changed || converted
		if err != nil {
			return nil, err
		}
		if changed {
			if expressions == nil {
				expressions = append([]expr.Expr(nil), output.Expressions...)
			}
			expressions[i] = folded
		}
	}
	if expressions == nil {
		return output, nil
	}
	private := *output
	private.Expressions = expressions
	return &private, nil
}

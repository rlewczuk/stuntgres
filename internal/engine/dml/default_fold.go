package dml

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/view"
)

// FoldDefault plans the default expression of b's column slot for
// row-at-a-time evaluation: on a copy of evaluation with the default's origin
// applied, it folds the coercions and then runs admit over the folded
// expression and read. COPY FROM admits with the join-execution check, an FK
// SET DEFAULT action with exec.AdmitExpression.
func FoldDefault(evaluation *exec.Context, b *analyze.Binding, slot int, expression expr.Expr, read catalog.Read, admit func(*exec.Context, expr.Expr, catalog.Read) error) (expr.Expr, error) {
	local := *evaluation
	analyze.ApplyDefaultOrigin(view.BindingDefaultOrigin(b, slot), &local)
	folded, _, err := exec.PreflightCoercionsWithPolicy(&local, expression, true)
	if err != nil {
		return nil, err
	}
	if err := admit(&local, folded, read); err != nil {
		return nil, err
	}
	return folded, nil
}

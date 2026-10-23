package plan

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// PLAssignmentProjectionError distinguishes an error produced by an actual
// set-projection row from ordinary scalar assignment execution.
type PLAssignmentProjectionError struct{ Err error }

func (e *PLAssignmentProjectionError) Error() string { return e.Err.Error() }
func (e *PLAssignmentProjectionError) Unwrap() error { return e.Err }

// PLAssignmentTargetExpr reconstructs one semantic assignment datum per query
// output row. ProjectSet owns zero/many-row scheduling; this node never publishes
// activation values and always reads the same immutable original datum.
type PLAssignmentTargetExpr struct {
	expr.Info
	Current       expr.Expr
	Child         expr.Expr
	Target        *ArrayAssignmentTarget
	SetProjection bool
}

func (n PLAssignmentTargetExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	current, err := n.Current.Eval(ctx)
	if err == nil {
		current, err = n.Target.ApplyExpression(ctx, current, n.Child)
	}
	if err != nil && n.SetProjection {
		return sqltype.Value{}, &PLAssignmentProjectionError{Err: err}
	}
	return current, err
}

func (n PLAssignmentTargetExpr) Children() []expr.Expr {
	count := 2
	for target := n.Target; target != nil; target = target.Next {
		for _, bound := range target.Node.Subscripts {
			if bound.Upper != nil {
				count++
			}
			if bound.Lower != nil {
				count++
			}
		}
	}
	children := make([]expr.Expr, 0, count)
	children = append(children, n.Current)
	for target := n.Target; target != nil; target = target.Next {
		for _, bound := range target.Node.Subscripts {
			if bound.Upper != nil {
				children = append(children, bound.Upper)
			}
		}
		for _, bound := range target.Node.Subscripts {
			if bound.Lower != nil {
				children = append(children, bound.Lower)
			}
		}
	}
	return append(children, n.Child)
}

func (n PLAssignmentTargetExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if n.Target == nil || len(children) < 2 || children[0] == nil || children[len(children)-1] == nil || children[0].ResultUsage() != n.Target.Container || children[len(children)-1].ResultUsage() != n.Target.Destination {
		return nil, fmt.Errorf("invalid PL assignment target operands")
	}
	n.Info, n.Current, n.Child = info, children[0], children[len(children)-1]
	bounds := children[1 : len(children)-1]
	var err error
	n.Target, err = rebuildPLAssignmentBounds(n.Target, &bounds)
	if err != nil || len(bounds) != 0 {
		return nil, fmt.Errorf("invalid PL assignment target bounds")
	}
	n.Property = n.Current.Properties()
	for _, child := range children[1:] {
		n.Property = expr.MergeProperties(n.Property, child.Properties())
	}
	return n, nil
}

func rebuildPLAssignmentBounds(target *ArrayAssignmentTarget, children *[]expr.Expr) (*ArrayAssignmentTarget, error) {
	if target == nil {
		return nil, nil
	}
	copy := *target
	copy.Node.Subscripts = slices.Clone(target.Node.Subscripts)
	take := func() (expr.Expr, error) {
		if len(*children) == 0 || (*children)[0] == nil {
			return nil, fmt.Errorf("missing PL assignment bound")
		}
		child := (*children)[0]
		*children = (*children)[1:]
		return child, nil
	}
	for i := range copy.Node.Subscripts {
		if copy.Node.Subscripts[i].Upper != nil {
			child, err := take()
			if err != nil {
				return nil, err
			}
			copy.Node.Subscripts[i].Upper = child
		}
	}
	for i := range copy.Node.Subscripts {
		if copy.Node.Subscripts[i].Lower != nil {
			child, err := take()
			if err != nil {
				return nil, err
			}
			copy.Node.Subscripts[i].Lower = child
		}
	}
	var err error
	copy.Next, err = rebuildPLAssignmentBounds(target.Next, children)
	return &copy, err
}

func (PLAssignmentTargetExpr) PlanningFold() expr.Fold { return expr.FoldNever }

// Dynamic RHS coercions must not overtake runtime bound evaluation. The outer
// PLAssignmentExpr already performs SQL-constant planning before this barrier.
func (PLAssignmentTargetExpr) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (PLAssignmentTargetExpr) ValidateStoredProvider() error {
	return fmt.Errorf("invocation-only PL assignment target")
}

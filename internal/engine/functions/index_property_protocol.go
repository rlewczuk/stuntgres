package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (n IndexProperty) Children() []expr.Expr { return n.children() }
func (n IndexProperty) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("index property", children, n.Kind.count()); err != nil {
		return nil, err
	}
	n.Info = info
	copy(n.Args[:], children)
	n.Property = expr.Properties{Volatility: expr.Stable}
	for _, child := range children {
		n.Property = expr.MergeProperties(n.Property, child.Properties())
	}
	return n, nil
}
func (n IndexProperty) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	next, err := n.Substitute(info, children)
	if err != nil {
		return nil, err
	}
	n = next.(IndexProperty)
	if n.Kind != AccessMethodProperty {
		n.oidPlan, err = sqltype.PlanCoercion(n.Args[0].ResultUsage(), n.oidUsage, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
	}
	return n, nil
}
func (n IndexProperty) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	if err := checkChildren("index property", children, n.Kind.count()); err != nil {
		return nil, false, err
	}
	n.Info = info
	copy(n.Args[:], children)
	var err error
	if n.Kind != AccessMethodProperty {
		typ := env.Type(n.oidUsage.Type)
		changed = changed || typ != n.oidUsage.Type
		n.oidUsage.Type = typ
		n.oidPlan, err = sqltype.PlanCoercion(n.Args[0].ResultUsage(), n.oidUsage, sqltype.Implicit)
		if err != nil {
			return nil, false, err
		}
	}
	return n, changed, nil
}

// Keep the historical analyzed-property fallback, including eliminated arms.
func (n IndexProperty) IntrinsicVolatility() expr.Volatility { return n.Property.Volatility }
func (IndexProperty) PlanningFold() expr.Fold                { return expr.FoldNever }
func (IndexProperty) PreflightFold() expr.Preflight          { return expr.PreflightChildren }
func (IndexProperty) LooksLikeFunction() bool                { return true }
func (n IndexProperty) SnapshotDemand(func(expr.Expr) bool) (bool, bool, bool) {
	return true, false, false
}
func (n IndexProperty) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(IndexProperty)
	if !ok || n.Kind != o.Kind {
		return false
	}
	for i := range n.Kind.count() {
		if !same(n.Args[i], o.Args[i]) {
			return false
		}
	}
	return true
}
func (n IndexProperty) Render() (expr.Rendering, error) {
	if err := validateStoredIndexPropertyExpression(n); err != nil {
		return expr.Rendering{}, err
	}
	children := n.Children()
	args := make([]expr.RenderArg, len(children))
	for i, arg := range children {
		args[i] = expr.RenderArg{Expr: arg}
	}
	spec := expr.Rendering{Tree: fmt.Sprintf("IndexProperty kind %d", n.Kind), Inputs: children, Form: expr.FormCall, Name: n.Kind.name(), Args: args, Replay: expr.Replay{RelationOnly: true, CatalogSnapshot: true}}
	if n.Kind != AccessMethodProperty {
		spec.Replay.Types = []*sqltype.Descriptor{n.oidUsage.Type}
	}
	return spec, nil
}
func (n IndexProperty) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureShape(n.Kind.count(), len(n.Args))
}
func (n IndexProperty) CaptureOperand(written int) (expr.Expr, error) {
	return captureAt(n.Args[:], n.Kind.count(), written)
}

func (IndexProperty) GroupProperties(children []expr.Expr) expr.Properties {
	properties := expr.Properties{Volatility: expr.Stable}
	for _, child := range children {
		properties = expr.MergeProperties(properties, child.Properties())
	}
	return properties
}

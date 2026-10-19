package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type JSONRecord struct {
	expr.Info
	Plan    sqltype.JSONRecordPlan
	Args    []expr.Expr
	Defined bool
}

func (e JSONRecord) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Plan.ReturnsSet() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted JSON recordset call")
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args {
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return e.Plan.Evaluate(ctx, values[:len(e.Args)], c.FunctionInput())
}
func (e JSONRecord) Reconstruct(info expr.Info, args []expr.Expr) (JSONRecord, error) {
	if len(args) < 1 || len(args) > 2 {
		return e, fmt.Errorf("invalid JSON record argument count")
	}
	var usages [2]sqltype.Usage
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindJSONRecord(e.Plan.Name(), info.Usage, e.Plan.BaseIndex(), usages[:len(args)]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// JSONRecord implements expr.Composite.
var _ expr.Composite = JSONRecord{}

func (e JSONRecord) Children() []expr.Expr { return e.Args }
func (e JSONRecord) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONRecord", children, len(e.Args)); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children))
}
func NewJSONRecord(info expr.Info, name string, baseIndex int, args []expr.Expr) (JSONRecord, error) {
	var usages [2]sqltype.Usage
	if len(args) < 1 || len(args) > 2 {
		return JSONRecord{}, fmt.Errorf("invalid JSON record argument count")
	}
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindJSONRecord(name, info.Usage, baseIndex, usages[:len(args)]...)
	return JSONRecord{Info: info, Plan: plan, Args: args}, err
}

func (e JSONRecord) IntrinsicVolatility() expr.Volatility { return expr.Stable }

func (e JSONRecord) PlanningFold() expr.Fold { return expr.FoldNever }

var _ expr.Renderable = JSONRecord{}

// Render implements expr.Renderable.
func (e JSONRecord) Render() (expr.Rendering, error) {
	spec := callRendering("JSONRecord "+e.Plan.Name(), e.Args, e.Plan.Name(), e.Args)
	spec.Quote, spec.LabelQuote = expr.QuoteWhenNeeded, expr.QuoteVerbatim
	if e.Plan.BaseIndex() == 1 {
		spec.Args[0].Label, spec.Args[1].Label = "from_json", "base"
	}
	spec.Replay.Validate = func() error {
		if _, err := e.Reconstruct(e.Info, e.Args); err != nil {
			return err
		}
		if e.Plan.ReturnsSet() {
			return fmt.Errorf("stuntgres: stored scalar contains JSON recordset")
		}
		return nil
	}
	return spec, nil
}

// SameGroup implements expr.GroupIdentity.
func (e JSONRecord) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(JSONRecord)
	if !ok || e.Plan.Name() != o.Plan.Name() || e.Plan.BaseIndex() != o.Plan.BaseIndex() || e.Defined != o.Defined || !sqltype.SameRecordUsage(e.ResultUsage(), o.ResultUsage()) || len(e.Args) != len(o.Args) {
		return false
	}
	for i, arg := range e.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

// PreflightFold implements expr.Preflighting.
func (e JSONRecord) PreflightFold() expr.Preflight { return expr.PreflightChildren }

// Substitute implements expr.Substitutable.
func (e JSONRecord) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONRecord", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info, e.Args = info, children
	return e, nil
}

func (e JSONRecord) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	args := e.Children()
	return expr.CaptureOperands{Args: args, Count: len(args)}, true, nil
}

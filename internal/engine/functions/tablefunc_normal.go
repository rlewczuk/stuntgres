package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// TablefuncNormal is immutable bound code; each opening owns its advancement.
type TablefuncNormal struct {
	expr.Info
	Args [3]expr.Expr
}

func bindTablefuncNormal(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var small [3]Argument
	operands := small[:]
	if len(raw.Args) != 3 { operands = make([]Argument, len(raw.Args)) }
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil { return nil, err }
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil { return nil, err }
	routine, err := b.InstalledRoutine(raw.Name, TablefuncExtension, "23,701,701")
	if err != nil { return nil, err }
	formals := [3]sqltype.Usage{b.Type(sqltype.Int4).Usage(), b.Type(sqltype.Float8).Usage(), b.Type(sqltype.Float8).Usage()}
	valid := routine != nil && !raw.ResolutionOnly && !raw.Variadic && !raw.Star && len(operands) == 3 && len(raw.ArgNames) == 0
	if valid {
		for i, arg := range operands {
			if arg.Usage.Type != nil {
				if _, err := sqltype.PlanCoercion(arg.Usage, formals[i], sqltype.Implicit); err != nil { valid = false; break }
			}
		}
	}
	if !valid { return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands)) }
	if err := routine.Validate(); err != nil { return nil, err }
	if err := CallDecorationError(raw); err != nil { return nil, err }
	var call TablefuncNormal
	properties := expr.Properties{Volatility: expr.Volatile, StageDependent: true}
	for i, arg := range operands {
		call.Args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: DirectFormalArgument, Target: formals[i]})
		if err != nil { return nil, err }
		properties = expr.MergeProperties(properties, call.Args[i].Properties())
	}
	call.Info = b.Info(formals[1], raw.Pos, properties)
	return routine.Attach(call)
}
func (TablefuncNormal) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: normal_rand requires a source invocation")
}
func (n TablefuncNormal) Children() []expr.Expr { return n.Args[:] }
func (n TablefuncNormal) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 3 { return nil, fmt.Errorf("stuntgres: invalid normal_rand arguments") }
	for i, child := range children {
		want := sqltype.Float8
		if i == 0 { want = sqltype.Int4 }
		if child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.BaseType().ID() != want {
			return nil, fmt.Errorf("stuntgres: invalid normal_rand argument")
		}
		n.Args[i] = child
	}
	n.Info = info
	return n, nil
}
func (n TablefuncNormal) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) { return n.Rebuild(info, children) }
func (TablefuncNormal) SourceAdmission() (bool, bool) { return true, true }
func (TablefuncNormal) ScalarSourceOutName() string { return "normal_rand" }
func (TablefuncNormal) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (TablefuncNormal) PlanningFold() expr.Fold { return expr.FoldNever }
func (TablefuncNormal) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (TablefuncNormal) LooksLikeFunction() bool { return true }

type tablefuncNormalCursor struct {
	remaining int64
	mean, deviation float64
	owner expr.RandomOwner
}

func (n TablefuncNormal) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if _, err := evaluationContext(ctx); err != nil { return expr.SourceCursor{}, err }
	var values [3]sqltype.Value
	for i, arg := range n.Args {
		value, err := arg.Eval(ctx)
		if err != nil { return expr.SourceCursor{}, err }
		values[i] = value
	}
	for _, value := range values {
		if value.IsNull() { return expr.OwnedSource(n.Usage.Type, nil), nil }
	}
	count, err := values[0].Integer()
	if err != nil { return expr.SourceCursor{}, err }
	if count < 0 { return expr.SourceCursor{}, sqlError("22023", "number of rows cannot be negative") }
	if count == 0 { return expr.OwnedSource(n.Usage.Type, nil), nil }
	mean, err := values[1].Float()
	if err != nil { return expr.SourceCursor{}, err }
	deviation, err := values[2].Float()
	if err != nil { return expr.SourceCursor{}, err }
	owner, err := ctx.FunctionRandomOwner()
	if err != nil { return expr.SourceCursor{}, err }
	state := &tablefuncNormalCursor{remaining: count, mean: mean, deviation: deviation, owner: owner}
	return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, State: state, Advance: nextTablefuncNormal}), nil
}

func nextTablefuncNormal(_ context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	state := cursor.State.(*tablefuncNormalCursor)
	if state.remaining == 0 { return sqltype.Value{}, false, nil }
	generator, err := state.owner.Borrow()
	if err != nil { return sqltype.Value{}, false, err }
	z := generator.NormFloat64()
	state.owner.Release()
	state.remaining--
	value, err := sqltype.Float(cursor.Type, tablefuncNormalTransform(state.mean, state.deviation, z))
	return value, err == nil, err
}

// Deliberately raw IEEE arithmetic, not SQL checked float operators.
func tablefuncNormalTransform(mean, deviation, z float64) float64 { return mean + deviation*z }

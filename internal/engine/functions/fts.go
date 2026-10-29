package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqltype"
)

type FTS struct {
	expr.Info
	Plan      sqltype.FTSPlan
	Args      [3]expr.Expr
	Namespace string
	Function  bool
}

func NewFTS(info expr.Info, op sqltype.FTSOperator, args [3]expr.Expr, namespace string, function bool) (FTS, error) {
	if op.Name() == "" {
		return FTS{}, fmt.Errorf("stuntgres: invalid FTS operation")
	}
	var usages [3]sqltype.Usage
	for i, arg := range args {
		if i < op.Arity() {
			if arg == nil {
				return FTS{}, fmt.Errorf("stuntgres: missing FTS operand")
			}
			usages[i] = arg.ResultUsage()
		} else if arg != nil {
			return FTS{}, fmt.Errorf("stuntgres: extra FTS operand")
		}
	}
	p, err := sqltype.BindFTS(op, info.Usage, usages)
	return FTS{info, p, args, namespace, function}, err
}
func (e FTS) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	count := e.Plan.Operation().Arity()
	for _, arg := range e.Args[:count] {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [3]sqltype.Value
	for i, arg := range e.Args[:count] {
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if e.Plan.Operation() == sqltype.FTSMatchTT || e.Plan.Operation() == sqltype.FTSMatchTQ {
		null, err := e.Plan.MatchInputs(ctx, values)
		if err != nil {
			return sqltype.Value{}, err
		}
		if null {
			return sqltype.Null(e.Usage.Type), nil
		}
		runtime, ok := c.(FTSConversionRuntime)
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: text search conversion lacks statement authority")
		}
		target, err := e.Plan.MatchConversionTarget(0)
		if err != nil {
			return sqltype.Value{}, err
		}
		document, err := values[0].Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		vector, err := runtime.ConvertTextSearch(fts.VectorConversion, false, sqltype.Value{}, document, target)
		if err != nil {
			return sqltype.Value{}, err
		}
		query := values[1]
		if e.Plan.Operation() == sqltype.FTSMatchTT {
			target, err = e.Plan.MatchConversionTarget(1)
			if err != nil {
				return sqltype.Value{}, err
			}
			text, err := query.Text()
			if err != nil {
				return sqltype.Value{}, err
			}
			query, err = runtime.ConvertTextSearch(fts.PlainConversion, false, sqltype.Value{}, text, target)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		return e.Plan.EvaluateNormalizedMatch(ctx, vector, query)
	}
	return e.Plan.Evaluate(ctx, values)
}
func (e FTS) Children() []expr.Expr { return e.Args[:e.Plan.Operation().Arity()] }
func (e FTS) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("FTS", children, e.Plan.Operation().Arity()); err != nil {
		return nil, err
	}
	var args [3]expr.Expr
	copy(args[:], children)
	return rebuilt(NewFTS(info, e.Plan.Operation(), args, e.Namespace, e.Function))
}
func (e FTS) Validate() error {
	next, err := NewFTS(e.Info, e.Plan.Operation(), e.Args, e.Namespace, e.Function)
	if err != nil || next.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored FTS provider")
	}
	return nil
}
func (e FTS) ValidateStoredProvider() error { return e.Validate() }
func (e FTS) IntrinsicVolatility() expr.Volatility {
	if e.Plan.Operation() == sqltype.FTSMatchTT || e.Plan.Operation() == sqltype.FTSMatchTQ {
		return expr.Stable
	}
	return expr.Immutable
}
func (e FTS) PreflightFold() expr.Preflight { return expr.PreflightNullOrEvaluate }
func (e FTS) LooksLikeFunction() bool       { return e.Function }
func (e FTS) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	name := e.Plan.Name()
	if e.Function {
		name = e.Plan.Operation().Routine()
	}
	spec := callRendering("FTS "+name+" namespace "+e.Namespace, e.Children(), name, e.Children())
	spec.Namespace = e.Namespace
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.Validate
	if !e.Function {
		spec.Form = expr.FormInfix
		if e.Plan.Operation() == sqltype.FTSNot {
			spec.Form = expr.FormTemplate
			spec.Pieces = []expr.Piece{expr.TextPiece("( !! "), expr.ArgPiece(0), expr.TextPiece(" )")}
			namespace := e.Namespace
			if namespace == "" {
				namespace = "pg_catalog"
			}
			spec.Replay.Pieces = []expr.Piece{expr.TextPiece("( OPERATOR("), expr.IdentifierPiece(namespace), expr.TextPiece(".!!) "), expr.ArgPiece(0), expr.TextPiece(" )")}
			if namespace != "pg_catalog" {
				spec.Pieces = spec.Replay.Pieces
			}
		}
	}
	return spec, nil
}
func (e FTS) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(FTS)
	if !ok || e.Plan != o.Plan || e.Namespace != o.Namespace || e.Function != o.Function {
		return false
	}
	for i, arg := range e.Args[:e.Plan.Operation().Arity()] {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}
func (e FTS) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("FTS", children, e.Plan.Operation().Arity()); err != nil {
		return nil, err
	}
	return e.Rebuild(info, children)
}
func (FTS) ClosureTransparent() {}
func (e FTS) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	want := expr.CaptureOperator
	if e.Function {
		want = expr.CaptureFunction
	}
	if kind != want {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: e.Plan.Operation().Arity(), Indexed: true}, true, nil
}
func (e FTS) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= e.Plan.Operation().Arity() || e.Args[written] == nil {
		return nil, fmt.Errorf("invalid FTS capture operand")
	}
	return e.Args[written], nil
}

var _ expr.Composite = FTS{}
var _ expr.Renderable = FTS{}

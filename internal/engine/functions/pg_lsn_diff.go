package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// PGLSNDiff retains only typed operands, arithmetic and ordinary routine authority.
// It neither requests nor retains physical WAL state.
type PGLSNDiff struct {
	expr.Info
	Args     [2]expr.Expr
	Provider sqltype.Arithmetic
}

var _ expr.StatementJumbler = PGLSNDiff{}

func bindPGLSNDiff(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [2]Argument
	valid := len(raw.Args) == 2 && !raw.Variadic && len(raw.ArgNames) == 0
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < 2 {
			operands[i] = arg
		}
		if arg.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(arg.Usage, b.Type(sqltype.PgLSN).Usage(), sqltype.Implicit); err != nil {
				valid = false
			}
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !valid || !visible {
		return nil, b.CallSignatureError(raw)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	var args [2]expr.Expr
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, arg := range operands {
		args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(sqltype.PgLSN).Usage(), Label: "pg_wal_lsn_diff"})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Numeric).Usage(), raw.Pos, properties), CatalogRoutineProvider, "pg_wal_lsn_diff", sqltype.PgLSN, sqltype.PgLSN)
	if err != nil {
		return nil, err
	}
	return newPGLSNDiff(info, args)
}

func newPGLSNDiff(info expr.Info, args [2]expr.Expr) (PGLSNDiff, error) {
	if missingArgument(args[:]) || info.Usage.Type == nil || info.Usage.Type.ID() != sqltype.Numeric {
		return PGLSNDiff{}, fmt.Errorf("stuntgres: invalid pg_wal_lsn_diff signature")
	}
	for _, arg := range args {
		if arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != sqltype.PgLSN {
			return PGLSNDiff{}, fmt.Errorf("stuntgres: invalid pg_wal_lsn_diff operand")
		}
	}
	provider, err := sqltype.BindArithmetic(sqltype.ArithmeticSubtract, args[0].ResultUsage().Type, args[1].ResultUsage().Type, info.Usage.Type)
	return PGLSNDiff{Info: info, Args: args, Provider: provider}, err
}

func (e PGLSNDiff) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Args {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	a, err := e.Args[0].Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.Args[1].Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Provider.Binary(ctx, a, b, c.FunctionInput())
}

func (e PGLSNDiff) Children() []expr.Expr { return e.Args[:] }
func (e PGLSNDiff) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("PGLSNDiff", children, 2); err != nil {
		return nil, err
	}
	return rebuilt(newPGLSNDiff(info, [2]expr.Expr{children[0], children[1]}))
}
func (e PGLSNDiff) ValidateStoredProvider() error {
	n, err := newPGLSNDiff(e.Info, e.Args)
	if err != nil || n.Provider != e.Provider {
		return fmt.Errorf("stuntgres: invalid pg_wal_lsn_diff provider")
	}
	return nil
}
func (e PGLSNDiff) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e PGLSNDiff) Render() (expr.Rendering, error) {
	spec := callRendering("PGLSNDiff", e.Args[:], "pg_wal_lsn_diff", e.Args[:])
	spec.Namespace = "pg_catalog"
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = e.ValidateStoredProvider
	return spec, nil
}
func (e PGLSNDiff) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(PGLSNDiff)
	return ok && e.Provider == o.Provider && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e PGLSNDiff) LooksLikeFunction() bool { return true }
func (e PGLSNDiff) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e PGLSNDiff) ClosureTransparent() {}
func (e PGLSNDiff) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Inline: e.Args, Count: 2}, true, nil
}

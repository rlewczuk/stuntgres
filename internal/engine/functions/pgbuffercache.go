package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// PgBuffercache carries only the occurrence's caller-completed anonymous record.
// Authority and the detached sparse snapshot belong to the executing statement.
type PgBuffercache struct{ expr.Info }

func bindPgBuffercache(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	operands := make([]Argument, len(raw.Args))
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	routine, err := b.InstalledRoutine(raw.Name, PgBuffercacheExtension, "")
	if err != nil {
		return nil, err
	}
	if routine == nil || raw.ResolutionOnly || raw.Variadic || len(operands) != 0 || len(raw.ArgNames) != 0 {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	return routine.Attach(PgBuffercache{Info: b.Info(b.Type(2249).Usage(), raw.Pos, expr.Properties{Volatility: expr.Volatile, StageDependent: true})})
}
func (PgBuffercache) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: pg_buffercache requires a source invocation")
}
func (PgBuffercache) Children() []expr.Expr { return nil }
func (n PgBuffercache) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 {
		return nil, fmt.Errorf("stuntgres: invalid pg_buffercache arguments")
	}
	n.Info = info
	return n, nil
}
func (n PgBuffercache) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (PgBuffercache) SourceAdmission() (bool, bool)        { return true, true }
func (PgBuffercache) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (PgBuffercache) PlanningFold() expr.Fold              { return expr.FoldNever }
func (PgBuffercache) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (PgBuffercache) LooksLikeFunction() bool              { return true }
func (PgBuffercache) OpenSource(expr.Context) (expr.SourceCursor, error) {
	return expr.SourceCursor{}, fmt.Errorf("stuntgres: pg_buffercache lacks installed authority")
}

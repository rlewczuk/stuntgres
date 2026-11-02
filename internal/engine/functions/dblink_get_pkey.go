package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const DblinkGetPkeyName = "dblink_get_pkey"

// DblinkGetPkey owns its argument and nominal result, never runtime authority.
type DblinkGetPkey struct {
	expr.Info
	Arg expr.Expr
}

func bindDblinkGetPkey(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var single [1]Argument
	operands := single[:]
	if len(raw.Args) != 1 {
		operands = make([]Argument, len(raw.Args))
	}
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
	routine, err := b.InstalledRoutine(raw.Name, DblinkExtension, "25")
	if err != nil {
		return nil, err
	}
	valid := routine != nil && !raw.ResolutionOnly && !raw.Variadic && len(operands) == 1 && len(raw.ArgNames) == 0
	formal := b.Type(sqltype.Text).Usage()
	if valid && operands[0].Usage.Type != nil {
		_, err = sqltype.PlanCoercion(operands[0].Usage, formal, sqltype.Implicit)
		valid = err == nil
	}
	if !valid {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	arg, err := b.CoerceArgument(operands[0], CoercionRequest{Intent: DirectFormalArgument, Target: formal})
	if err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Volatile, StageDependent: true}, arg.Properties())
	// Attachment supplies the selected installation's nominal return type.
	return routine.Attach(DblinkGetPkey{Info: b.Info(b.Type(2249).Usage(), raw.Pos, properties), Arg: arg})
}

func (n DblinkGetPkey) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: dblink_get_pkey requires a source invocation")
}
func (n DblinkGetPkey) Children() []expr.Expr { return []expr.Expr{n.Arg} }
func (n DblinkGetPkey) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 || children[0] == nil || children[0].ResultUsage().Type == nil || children[0].ResultUsage().Type.BaseType().ID() != sqltype.Text || info.Usage.Type == nil || !info.Usage.Type.IsComposite() || !info.Usage.Type.IsUserDefined() || info.Usage.Type.IsDomain() || sqltype.IsPseudo(info.Usage.Type.ID()) {
		return nil, fmt.Errorf("stuntgres: invalid dblink_get_pkey expression")
	}
	n.Info, n.Arg = info, children[0]
	return n, nil
}
func (n DblinkGetPkey) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (DblinkGetPkey) SourceAdmission() (bool, bool)        { return true, true }
func (DblinkGetPkey) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (DblinkGetPkey) PlanningFold() expr.Fold              { return expr.FoldNever }
func (DblinkGetPkey) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (DblinkGetPkey) LooksLikeFunction() bool              { return true }

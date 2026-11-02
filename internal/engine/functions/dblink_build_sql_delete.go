package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const DblinkDeleteName = "dblink_build_sql_delete"
const DblinkDeleteSignature = "25,22,23,1009"

// DblinkBuildSQLDelete retains only typed operands. Relation metadata and the
// returned SQL are invocation-time facts, never prepared-code state.
type DblinkBuildSQLDelete struct {
	expr.Info
	Args [4]expr.Expr
}

func bindDblinkBuildSQLDelete(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var small [4]Argument
	operands := small[:]
	if len(raw.Args) != len(small) { operands = make([]Argument, len(raw.Args)) }
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil { return nil, err }
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil { return nil, err }
	routine, err := b.InstalledRoutine(raw.Name, DblinkExtension, DblinkDeleteSignature)
	if err != nil { return nil, err }
	ids := [...]sqltype.ID{sqltype.Text, sqltype.Int2Vector, sqltype.Int4, 1009}
	valid := routine != nil && !raw.ResolutionOnly && !raw.Variadic && !raw.Star && len(operands) == 4 && len(raw.ArgNames) == 0
	if valid {
		for i, arg := range operands {
			if arg.Usage.Type != nil {
				if _, err := sqltype.PlanCoercion(arg.Usage, b.Type(ids[i]).Usage(), sqltype.Implicit); err != nil { valid = false; break }
			}
		}
	}
	if !valid { return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands)) }
	if err := routine.Validate(); err != nil { return nil, err }
	if err := CallDecorationError(raw); err != nil { return nil, err }
	var call DblinkBuildSQLDelete
	properties := expr.Properties{Volatility: expr.Volatile, StageDependent: true}
	for i, arg := range operands {
		call.Args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: DirectFormalArgument, Target: b.Type(ids[i]).Usage()})
		if err != nil { return nil, err }
		properties = expr.MergeProperties(properties, call.Args[i].Properties())
	}
	call.Info = b.Info(b.Type(sqltype.Text).Usage(), raw.Pos, properties)
	return routine.Attach(call)
}
func (DblinkBuildSQLDelete) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: dblink constructor requires installed invocation authority")
}
func (n DblinkBuildSQLDelete) Children() []expr.Expr { return n.Args[:] }
func (n DblinkBuildSQLDelete) ValidateArguments(children []expr.Expr) error {
	ids := [...]sqltype.ID{sqltype.Text, sqltype.Int2Vector, sqltype.Int4, 1009}
	if len(children) != len(ids) { return fmt.Errorf("stuntgres: invalid dblink constructor arity") }
	for i, child := range children {
		if child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.BaseType().ID() != ids[i] {
			return fmt.Errorf("stuntgres: invalid dblink constructor argument")
		}
	}
	return nil
}
func (n DblinkBuildSQLDelete) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := n.ValidateArguments(children); err != nil { return nil, err }
	n.Info = info
	copy(n.Args[:], children)
	return n, nil
}
func (n DblinkBuildSQLDelete) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (DblinkBuildSQLDelete) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (DblinkBuildSQLDelete) PlanningFold() expr.Fold { return expr.FoldNever }
func (DblinkBuildSQLDelete) SourceAdmission() (bool, bool) { return true, false }
func (DblinkBuildSQLDelete) StrictNull() expr.Strict { return expr.StrictAfterBare }
func (DblinkBuildSQLDelete) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (DblinkBuildSQLDelete) LooksLikeFunction() bool { return true }
func (n DblinkBuildSQLDelete) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Args: n.Args[:], Count: len(n.Args)}, kind == expr.CaptureFunction, nil
}

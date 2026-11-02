package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const DblinkInsertName = "dblink_build_sql_insert"
const DblinkInsertSignature = "25,22,23,1009,1009"

// DblinkBuildSQLInsert retains only typed operands. Relation metadata and the
// returned SQL are invocation-time facts, never prepared-code state.
type DblinkBuildSQLInsert struct {
	expr.Info
	Args [5]expr.Expr
}

func bindDblinkBuildSQLInsert(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var small [5]Argument
	operands := small[:]
	if len(raw.Args) != len(small) {
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
	routine, err := b.InstalledRoutine(raw.Name, DblinkExtension, DblinkInsertSignature)
	if err != nil {
		return nil, err
	}
	ids := [...]sqltype.ID{sqltype.Text, sqltype.Int2Vector, sqltype.Int4, 1009, 1009}
	valid := routine != nil && !raw.ResolutionOnly && !raw.Variadic && !raw.Star && len(operands) == 5 && len(raw.ArgNames) == 0
	if valid {
		for i, arg := range operands {
			if arg.Usage.Type != nil {
				if _, err := sqltype.PlanCoercion(arg.Usage, b.Type(ids[i]).Usage(), sqltype.Implicit); err != nil {
					valid = false
					break
				}
			}
		}
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
	var call DblinkBuildSQLInsert
	properties := expr.Properties{Volatility: expr.Volatile, StageDependent: true}
	for i, arg := range operands {
		call.Args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: DirectFormalArgument, Target: b.Type(ids[i]).Usage()})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, call.Args[i].Properties())
	}
	call.Info = b.Info(b.Type(sqltype.Text).Usage(), raw.Pos, properties)
	return routine.Attach(call)
}
func (DblinkBuildSQLInsert) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: dblink constructor requires installed invocation authority")
}
func (n DblinkBuildSQLInsert) Children() []expr.Expr { return n.Args[:] }
func (n DblinkBuildSQLInsert) ValidateArguments(children []expr.Expr) error {
	ids := [...]sqltype.ID{sqltype.Text, sqltype.Int2Vector, sqltype.Int4, 1009, 1009}
	if len(children) != len(ids) {
		return fmt.Errorf("stuntgres: invalid dblink constructor arity")
	}
	for i, child := range children {
		if child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.BaseType().ID() != ids[i] {
			return fmt.Errorf("stuntgres: invalid dblink constructor argument")
		}
	}
	return nil
}
func (n DblinkBuildSQLInsert) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := n.ValidateArguments(children); err != nil {
		return nil, err
	}
	n.Info = info
	copy(n.Args[:], children)
	return n, nil
}
func (n DblinkBuildSQLInsert) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (DblinkBuildSQLInsert) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (DblinkBuildSQLInsert) PlanningFold() expr.Fold              { return expr.FoldNever }
func (DblinkBuildSQLInsert) SourceAdmission() (bool, bool)        { return true, false }
func (DblinkBuildSQLInsert) StrictNull() expr.Strict              { return expr.StrictAfterBare }
func (DblinkBuildSQLInsert) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (DblinkBuildSQLInsert) LooksLikeFunction() bool              { return true }
func (n DblinkBuildSQLInsert) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Args: n.Args[:], Count: len(n.Args)}, kind == expr.CaptureFunction, nil
}

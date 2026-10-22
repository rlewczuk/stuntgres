package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Pgrowlocks is a strict, materialized installed source. Runtime state is never
// retained on this node; the engine wrapper supplies the installed identity.
type Pgrowlocks struct {
	expr.Info
	Arg expr.Expr
}

func bindPgrowlocks(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	routine, err := b.InstalledRoutine(raw.Name, PgrowlocksExtension, "25")
	if err != nil {
		return nil, err
	}
	valid := routine != nil && !raw.ResolutionOnly && !raw.Variadic && len(operands) == 1
	if len(raw.ArgNames) != 0 {
		valid = valid && len(raw.ArgNames) == 1 && raw.ArgNames[0].Text == "relname"
	}
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
	if raw.Star {
		return nil, expr.ErrorAt("42809", "pgrowlocks(*) specified, but pgrowlocks is not an aggregate function", raw.Pos)
	}
	arg, err := b.CoerceArgument(operands[0], CoercionRequest{Intent: DirectFormalArgument, Target: formal})
	if err != nil {
		return nil, err
	}
	names := [6]string{"locked_row", "locker", "multi", "xids", "modes", "pids"}
	types := [6]sqltype.ID{sqltype.Tid, sqltype.Xid, sqltype.Bool, 1011, 1009, 1007}
	members := make([]sqltype.CompositeMember, len(names))
	for i, name := range names {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: name, Usage: b.Type(types[i]).Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Volatile, StageDependent: true}, arg.Properties())
	return routine.Attach(Pgrowlocks{Info: b.Info(shape.Usage(), raw.Pos, properties), Arg: arg})
}

func (n Pgrowlocks) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: pgrowlocks requires a source invocation")
}
func (n Pgrowlocks) Children() []expr.Expr { return []expr.Expr{n.Arg} }
func (n Pgrowlocks) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 || children[0] == nil || children[0].ResultUsage().Type == nil || children[0].ResultUsage().Type.BaseType().ID() != sqltype.Text {
		return nil, fmt.Errorf("stuntgres: invalid pgrowlocks argument")
	}
	n.Info, n.Arg = info, children[0]
	return n, nil
}
func (n Pgrowlocks) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (Pgrowlocks) SourceAdmission() (bool, bool) { return true, true }

// PgrowlocksRuntime is implemented by the existing statement evaluation host.
type PgrowlocksRuntime interface {
	Pgrowlocks(string, *sqltype.Descriptor) ([]sqltype.Value, error)
}

func (n Pgrowlocks) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Arg.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if value.IsNull() {
		return expr.OwnedSource(n.Usage.Type, nil), nil
	}
	name, err := value.Text()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	runtime, ok := ctx.(PgrowlocksRuntime)
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing pgrowlocks statement runtime")
	}
	rows, err := runtime.Pgrowlocks(name, n.Usage.Type)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(n.Usage.Type, rows), nil
}

func (Pgrowlocks) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (Pgrowlocks) PlanningFold() expr.Fold              { return expr.FoldNever }
func (Pgrowlocks) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (Pgrowlocks) LooksLikeFunction() bool              { return true }

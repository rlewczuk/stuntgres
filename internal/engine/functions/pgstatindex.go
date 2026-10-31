package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Pgstatindex owns the typed operand, not the installed routine's authority.
type Pgstatindex struct {
	expr.Info
	Args  []expr.Expr
	Input sqltype.ID
}

var pgstatindexOutputNames = [...]string{"version", "tree_level", "index_size", "root_block_no", "internal_pages", "leaf_pages", "empty_pages", "deleted_pages", "avg_leaf_density", "leaf_fragmentation"}
var pgstatindexOutputTypes = [...]sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Float8, sqltype.Float8}

func bindPgstatindex(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	operands := make([]Argument, len(raw.Args))
	for i, child := range raw.Args {
		v, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = v
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	routine, err := b.InstalledRoutine(raw.Name, PgstattupleExtension, "")
	if err != nil {
		return nil, err
	}
	_, valid := namedSlots(raw, []string{"relname"}, 1)
	if !valid || len(operands) != 1 || routine == nil || raw.ResolutionOnly || raw.Variadic {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", "pgstatindex(*) specified, but pgstatindex is not an aggregate function", raw.Pos)
	}
	authority, ok := routine.(interface{ PgstatindexInput() sqltype.ID })
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing pgstatindex declaration")
	}
	input := authority.PgstatindexInput()
	if input != sqltype.Text && input != sqltype.Regclass {
		return nil, fmt.Errorf("stuntgres: invalid pgstatindex declaration")
	}
	target := b.Type(input).Usage()
	if operands[0].Usage.Type != nil {
		if _, err := sqltype.PlanCoercion(operands[0].Usage, target, sqltype.Implicit); err != nil {
			return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
		}
	}
	child, err := b.CoerceArgument(operands[0], CoercionRequest{Intent: DirectFormalArgument, Target: target})
	if err != nil {
		return nil, err
	}
	members := make([]sqltype.CompositeMember, len(pgstatindexOutputNames))
	for i, name := range pgstatindexOutputNames {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: name, Usage: b.Type(pgstatindexOutputTypes[i]).Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	n := Pgstatindex{Args: []expr.Expr{child}, Input: input}
	n.Info = b.Info(shape.Usage(), raw.Pos, expr.MergeProperties(expr.Properties{Volatility: expr.Volatile, StageDependent: true}, child.Properties()))
	return routine.Attach(n)
}
func namedPgstatindex(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	slots, ok := namedSlots(raw, []string{"relname"}, 1)
	if !ok || raw.Variadic || len(args) != 1 || len(declared) != 1 || declared[0].Type == nil || declared[0].Type.ID() != sqltype.Text && declared[0].Type.ID() != sqltype.Regclass {
		return nil, nil, false
	}
	return namedInputs(b, args, declared, slots[:1])
}
func (Pgstatindex) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: pgstatindex requires installed invocation authority")
}
func (n Pgstatindex) Children() []expr.Expr { return n.Args }
func (n Pgstatindex) ValidateArguments(children []expr.Expr) error {
	if n.Input != sqltype.Text && n.Input != sqltype.Regclass || len(children) != 1 || children[0] == nil || children[0].ResultUsage().Type == nil || children[0].ResultUsage().Type.BaseType().ID() != n.Input {
		return fmt.Errorf("stuntgres: invalid pgstatindex argument")
	}
	return nil
}
func (n Pgstatindex) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := n.ValidateArguments(children); err != nil {
		return nil, err
	}
	n.Info = info
	n.Args = append([]expr.Expr(nil), children...)
	return n, nil
}
func (n Pgstatindex) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (Pgstatindex) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (Pgstatindex) PlanningFold() expr.Fold              { return expr.FoldNever }
func (Pgstatindex) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (Pgstatindex) LooksLikeFunction() bool              { return true }
func (n Pgstatindex) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Args: n.Args, Count: 1}, kind == expr.CaptureFunction, nil
}
func (Pgstatindex) SourceAdmission() (bool, bool) { return true, false }
func (Pgstatindex) ScalarSourceOutName() string   { return "pgstatindex" }

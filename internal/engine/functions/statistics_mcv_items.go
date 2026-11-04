package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var statisticsMCVItemNames = [...]string{"index", "values", "nulls", "frequency", "base_frequency"}
var statisticsMCVItemTypes = [...]sqltype.ID{sqltype.Int4, 1009, 1000, sqltype.Float8, sqltype.Float8}

// StatisticsMCVItems retains one typed operand and an occurrence-owned OUT shape.
// Typed distributions and output policy are borrowed only by an open invocation.
type StatisticsMCVItems struct {
	expr.Info
	Arg expr.Expr
}

var _ expr.StatementJumbler = StatisticsMCVItems{}

func namedStatisticsMCVItems(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Variadic || !declaredTypes(declared, []sqltype.ID{sqltype.PgMCVList}) {
		return nil, nil, false
	}
	return fixedNamedInputs(b, raw, args, declared, []string{"mcv_list"}, 1)
}

func bindStatisticsMCVItems(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i == 0 {
			operand = arg
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	_, named := namedSlots(raw, []string{"mcv_list"}, 1)
	target := b.Type(sqltype.PgMCVList).Usage()
	if !visible || !named || raw.Variadic || raw.ResolutionOnly || !knownCallEdges(b, []Argument{operand}, []sqltype.Usage{target}) {
		return nil, b.CallSignatureError(raw)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	child, err := b.CoerceArgument(operand, CoercionRequest{Intent: ConditionalArgument, Target: target, Label: "pg_mcv_list_items"})
	if err != nil {
		return nil, err
	}
	if err := b.AdmitSetReturning(raw.Pos); err != nil {
		return nil, err
	}
	members := make([]sqltype.CompositeMember, len(statisticsMCVItemNames))
	for i, name := range statisticsMCVItemNames {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: name, Usage: b.Type(statisticsMCVItemTypes[i]).Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(expr.Properties{Volatility: expr.Stable}, child.Properties())
	info, err := b.BuiltinRoutineInfo(b.Info(shape.Usage(), raw.Pos, properties), EmbeddedRoutineProvider, "pg_mcv_list_items", sqltype.PgMCVList)
	if err != nil {
		return nil, err
	}
	// The formal hides the dictionary types: any invocation may need current
	// catalog-name presentation even though the public result is plain TEXT[].
	b.NoteCatalogText()
	return StatisticsMCVItems{Info: info, Arg: child}, nil
}

func (n StatisticsMCVItems) ValidateStoredProvider() error {
	d := n.Usage.Type
	if n.Arg == nil || n.Arg.ResultUsage().Type == nil || n.Arg.ResultUsage().Type.ID() != sqltype.PgMCVList || d == nil || d.ID() != 2249 || !d.IsComposite() || d.CompositeLen() != len(statisticsMCVItemNames) {
		return fmt.Errorf("stuntgres: invalid MCV item signature")
	}
	for i, name := range statisticsMCVItemNames {
		member := d.CompositeMember(i)
		if member.Attribute != int32(i+1) || member.Name != name || member.Usage.Type == nil || member.Usage.Type.ID() != statisticsMCVItemTypes[i] {
			return fmt.Errorf("stuntgres: invalid MCV item output shape")
		}
	}
	return nil
}

func (n StatisticsMCVItems) Children() []expr.Expr { return []expr.Expr{n.Arg} }
func (n StatisticsMCVItems) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("StatisticsMCVItems", children, 1); err != nil {
		return nil, err
	}
	n.Info, n.Arg = info, children[0]
	return n, n.ValidateStoredProvider()
}
func (n StatisticsMCVItems) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (StatisticsMCVItems) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: MCV item set used as scalar")
}
func (StatisticsMCVItems) IntrinsicVolatility() expr.Volatility    { return expr.Stable }
func (StatisticsMCVItems) PlanningFold() expr.Fold                 { return expr.FoldNever }
func (StatisticsMCVItems) PreflightFold() expr.Preflight           { return expr.PreflightKeep }
func (StatisticsMCVItems) StrictNull() expr.Strict                 { return expr.StrictAfterBare }
func (StatisticsMCVItems) LooksLikeFunction() bool                 { return true }
func (StatisticsMCVItems) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (n StatisticsMCVItems) SnapshotDemand(literalNull func(expr.Expr) bool) (bool, bool, bool) {
	return false, !literalNull(n.Arg), false
}
func (n StatisticsMCVItems) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(StatisticsMCVItems)
	return ok && sqltype.SameRecordUsage(n.Usage, o.Usage) && same(n.Arg, o.Arg)
}
func (n StatisticsMCVItems) Render() (expr.Rendering, error) {
	if err := n.ValidateStoredProvider(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "StatisticsMCVItems", Inputs: n.Children(), Form: expr.FormCall, Name: "pg_mcv_list_items", Quote: expr.QuoteIdentifier, Args: []expr.RenderArg{{Expr: n.Arg}}, Replay: expr.Replay{RelationOnly: true, CatalogSnapshot: true, Validate: n.ValidateStoredProvider}}, nil
}
func (n StatisticsMCVItems) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureShape(1, 1)
}
func (n StatisticsMCVItems) CaptureOperand(written int) (expr.Expr, error) {
	return captureAt(n.Children(), 1, written)
}

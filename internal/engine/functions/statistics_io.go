package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var statisticsIONames = []string{
	"pg_ndistinct_in", "pg_ndistinct_out", "pg_ndistinct_send",
	"pg_dependencies_in", "pg_dependencies_out", "pg_dependencies_send",
	"pg_mcv_list_in", "pg_mcv_list_out", "pg_mcv_list_send",
}

func statisticsIOSignature(name string) (sqltype.ID, sqltype.ID, sqltype.ID, string) {
	for _, family := range []struct {
		name string
		id   sqltype.ID
	}{{"pg_ndistinct", sqltype.PgNDistinct}, {"pg_dependencies", sqltype.PgDependencies}, {"pg_mcv_list", sqltype.PgMCVList}} {
		if !strings.HasPrefix(name, family.name+"_") {
			continue
		}
		operation := strings.TrimPrefix(name, family.name+"_")
		switch operation {
		case "in":
			return 2275, family.id, family.id, operation
		case "out":
			return family.id, 2275, family.id, operation
		case "send":
			return family.id, sqltype.Bytea, family.id, operation
		}
	}
	return 0, 0, 0, ""
}
func bindStatisticsIO(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	formal, result, datum, operation := statisticsIOSignature(name)
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
	if !visible || formal == 0 || len(raw.Args) != 1 || len(raw.ArgNames) != 0 || raw.Variadic {
		return nil, b.CallSignatureError(raw)
	}
	target := b.Type(formal).Usage()
	if !knownCallEdges(b, []Argument{operand}, []sqltype.Usage{target}) {
		return nil, b.CallSignatureError(raw)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	child, err := b.CoerceArgument(operand, CoercionRequest{Intent: ConditionalArgument, Target: target, Label: name})
	if err != nil {
		return nil, err
	}
	rule := expr.ExactImmutable
	if operation == "send" {
		rule = expr.ExactStable
	}
	properties := expr.MergeProperties(expr.IntrinsicProperties(rule), child.Properties())
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(result).Usage(), raw.Pos, properties), EmbeddedRoutineProvider, name, formal)
	if err != nil {
		return nil, err
	}
	return StatisticsIO{Info: info, Arg: child, Name: name, Datum: b.Type(datum), Operation: operation}, nil
}

type StatisticsIO struct {
	expr.Info
	Arg             expr.Expr
	Name, Operation string
	Datum           *sqltype.Descriptor
}

func (e StatisticsIO) Children() []expr.Expr { return []expr.Expr{e.Arg} }
func (e StatisticsIO) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("StatisticsIO", children, 1); err != nil {
		return nil, err
	}
	e.Info, e.Arg = info, children[0]
	// Remap through the actual result/formal generation, never retain a source
	// descriptor beside remapped expression ownership.
	if e.Operation == "in" {
		e.Datum = e.Usage.Type
	} else {
		e.Datum = e.Arg.ResultUsage().Type
	}
	return e, e.ValidateStoredProvider()
}
func (e StatisticsIO) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e StatisticsIO) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if c.FunctionLiteralNull(e.Arg) {
		return sqltype.Null(e.Usage.Type), nil
	}
	v, err := e.Arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if v.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	switch e.Operation {
	case "in":
		text, err := v.Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		return e.Datum.Input(ctx, text, -1)
	case "out":
		text, err := v.Type().OutputWithEnvironment(ctx, v, c.FunctionStringOutput())
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.String(e.Usage.Type, text), nil
	case "send":
		return v.StatisticsSend(ctx, e.Usage.Type)
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid statistics I/O operation")
}
func (e StatisticsIO) IntrinsicVolatility() expr.Volatility {
	if e.Operation == "send" {
		return expr.Stable
	}
	return expr.Immutable
}
func (e StatisticsIO) PlanningFold() expr.Fold {
	if e.Operation == "send" {
		return expr.FoldNever
	}
	return expr.FoldConstant
}
func (e StatisticsIO) PreflightFold() expr.Preflight {
	if e.Operation == "send" {
		return expr.PreflightChildren
	}
	return expr.PreflightNullOrEvaluate
}
func (e StatisticsIO) ValidateStoredProvider() error {
	formal, result, datum, operation := statisticsIOSignature(e.Name)
	if operation == "" || operation != e.Operation || e.Arg == nil || e.Arg.ResultUsage().Type.ID() != formal || e.Usage.Type.ID() != result || e.Datum == nil || e.Datum.ID() != datum {
		return fmt.Errorf("stuntgres: invalid statistics I/O provider")
	}
	return nil
}
func (e StatisticsIO) Render() (expr.Rendering, error) {
	spec := callRendering("StatisticsIO", e.Children(), e.Name, e.Children())
	spec.Replay.Validate = e.ValidateStoredProvider
	return spec, nil
}
func (e StatisticsIO) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(StatisticsIO)
	return ok && e.Name == o.Name && e.Datum == o.Datum && e.Usage == o.Usage && same(e.Arg, o.Arg)
}

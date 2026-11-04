package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// PgStatStatementsColumns is the immutable released 1.12 output declaration.
var PgStatStatementsColumns = [...]struct {
	Name string
	Type sqltype.ID
}{
	{"userid", 26}, {"dbid", 26}, {"toplevel", 16}, {"queryid", 20}, {"query", 25}, {"plans", 20},
	{"total_plan_time", 701}, {"min_plan_time", 701}, {"max_plan_time", 701}, {"mean_plan_time", 701}, {"stddev_plan_time", 701},
	{"calls", 20}, {"total_exec_time", 701}, {"min_exec_time", 701}, {"max_exec_time", 701}, {"mean_exec_time", 701}, {"stddev_exec_time", 701}, {"rows", 20},
	{"shared_blks_hit", 20}, {"shared_blks_read", 20}, {"shared_blks_dirtied", 20}, {"shared_blks_written", 20},
	{"local_blks_hit", 20}, {"local_blks_read", 20}, {"local_blks_dirtied", 20}, {"local_blks_written", 20},
	{"temp_blks_read", 20}, {"temp_blks_written", 20}, {"shared_blk_read_time", 701}, {"shared_blk_write_time", 701},
	{"local_blk_read_time", 701}, {"local_blk_write_time", 701}, {"temp_blk_read_time", 701}, {"temp_blk_write_time", 701},
	{"wal_records", 20}, {"wal_fpi", 20}, {"wal_bytes", 1700}, {"wal_buffers_full", 20},
	{"jit_functions", 20}, {"jit_generation_time", 701}, {"jit_inlining_count", 20}, {"jit_inlining_time", 701},
	{"jit_optimization_count", 20}, {"jit_optimization_time", 701}, {"jit_emission_count", 20}, {"jit_emission_time", 701},
	{"jit_deform_count", 20}, {"jit_deform_time", 701}, {"parallel_workers_to_launch", 20}, {"parallel_workers_launched", 20},
	{"stats_since", 1184}, {"minmax_stats_since", 1184},
}
var PgStatStatementsInfoColumns = [...]struct {
	Name string
	Type sqltype.ID
}{{"dealloc", 20}, {"stats_reset", 1184}}

type PgStatStatements struct {
	expr.Info
	ShowText expr.Expr
	InfoOnly bool
}

func bindPgStatStatements(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	infoOnly := raw.Name.Parts[len(raw.Name.Parts)-1].Text == "pg_stat_statements_info"
	signature, count := "16", 1
	if infoOnly {
		signature, count = "", 0
	}
	var single [1]Argument
	operands := single[:min(len(raw.Args), 1)]
	if len(raw.Args) > 1 {
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
	routine, err := b.InstalledRoutine(raw.Name, PgStatStatementsExtension, signature)
	if err != nil {
		return nil, err
	}
	if routine == nil || raw.ResolutionOnly || raw.Variadic || len(operands) != count || len(raw.ArgNames) != 0 && (infoOnly || !pgStatStatementsNamed(raw)) {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	var show expr.Expr
	if !infoOnly {
		show, err = b.CoerceArgument(operands[0], CoercionRequest{Intent: DirectFormalArgument, Target: b.Type(sqltype.Bool).Usage()})
		if err != nil {
			return nil, err
		}
	}
	columns := PgStatStatementsColumns[:]
	if infoOnly {
		columns = PgStatStatementsInfoColumns[:]
	}
	members := make([]sqltype.CompositeMember, len(columns))
	for i, c := range columns {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: c.Name, Usage: b.Type(c.Type).Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	props := expr.Properties{Volatility: expr.Volatile, StageDependent: true}
	if show != nil {
		props = expr.MergeProperties(props, show.Properties())
	}
	return routine.Attach(PgStatStatements{Info: b.Info(shape.Usage(), raw.Pos, props), ShowText: show, InfoOnly: infoOnly})
}
func (n PgStatStatements) Eval(ctx expr.Context) (sqltype.Value, error) {
	if !n.InfoOnly {
		return sqltype.Value{}, fmt.Errorf("stuntgres: statement statistics requires source invocation")
	}
	runtime, ok := ctx.(interface {
		PgStatStatements(bool, bool, *sqltype.Descriptor) ([]sqltype.Value, error)
	})
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing statement statistics runtime")
	}
	body, err := n.EnterRoutine(ctx, nil)
	if err != nil {
		return sqltype.Value{}, err
	}
	defer body.Leave()
	rows, err := runtime.PgStatStatements(true, n.InfoOnly, n.Usage.Type)
	if err != nil {
		return sqltype.Value{}, err
	}
	return rows[0], nil
}
func (n PgStatStatements) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	show := true
	if n.ShowText != nil {
		value, err := n.ShowText.Eval(ctx)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		if value.IsNull() {
			return expr.OwnedSource(n.Usage.Type, nil), nil
		}
		show, err = value.Boolean()
		if err != nil {
			return expr.SourceCursor{}, err
		}
	}
	runtime, ok := ctx.(interface {
		PgStatStatements(bool, bool, *sqltype.Descriptor) ([]sqltype.Value, error)
	})
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing statement statistics runtime")
	}
	facts := expr.RoutineBodyInputs{Occurrence: n.Occurrence, Source: n.Source}
	if n.ShowText != nil {
		facts.Count = 1
		facts.First = n.ShowText.ResultUsage().Type
	}
	body, err := ctx.FunctionEnterRoutine(n.RoutineReference, n.RoutineRecipeReference, facts)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	defer body.Leave()
	rows, err := runtime.PgStatStatements(show, n.InfoOnly, n.Usage.Type)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(n.Usage.Type, rows), nil
}
func (n PgStatStatements) Children() []expr.Expr {
	if n.ShowText == nil {
		return nil
	}
	return []expr.Expr{n.ShowText}
}
func (n PgStatStatements) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	want := 1
	if n.InfoOnly {
		want = 0
	}
	if len(children) != want {
		return nil, fmt.Errorf("stuntgres: invalid statement statistics arguments")
	}
	n.Info = info
	if want == 1 {
		n.ShowText = children[0]
	}
	return n, nil
}
func (n PgStatStatements) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n PgStatStatements) SourceAdmission() (bool, bool)      { return true, !n.InfoOnly }
func (PgStatStatements) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (PgStatStatements) PlanningFold() expr.Fold              { return expr.FoldNever }
func (PgStatStatements) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (PgStatStatements) LooksLikeFunction() bool              { return true }

func pgStatStatementsNamed(raw parser.FunctionExpr) bool {
	return len(raw.ArgNames) == 1 && raw.ArgNames[0].Text == "showtext"
}

func namedPgStatStatements(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Variadic || len(args) != 1 || !pgStatStatementsNamed(raw) || !declaredTypes(declared, []sqltype.ID{sqltype.Bool}) {
		return nil, nil, false
	}
	return namedInputs(b, args, declared, []int{0})
}

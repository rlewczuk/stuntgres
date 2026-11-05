package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The operation is installed declaration authority, independent of SQL spelling.
type PgBuffercacheAggregateKind uint8

const (
	BuffercacheSummary PgBuffercacheAggregateKind = iota
	BuffercacheUsageCounts
)

func (k PgBuffercacheAggregateKind) Set() bool { return k == BuffercacheUsageCounts }

// PgBuffercacheCounts is a detached fixed-size observation of the shared owner.
// Dirty remains zero because the logical read cache has no dirty entries.
type PgBuffercacheCounts struct {
	Capacity, Used, Dirty, Pinned, UsageTotal int64
	Buckets [6]PgBuffercacheBucket
}
type PgBuffercacheBucket struct { Buffers, Dirty, Pinned int64 }

type PgBuffercacheAggregate struct {
	expr.Info
	Kind PgBuffercacheAggregateKind
}

func bindPgBuffercacheAggregate(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	operands := make([]Argument, len(raw.Args))
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil { return nil, err }
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil { return nil, err }
	routine, err := b.InstalledRoutine(raw.Name, PgBuffercacheExtension, "")
	if err != nil { return nil, err }
	missing := func() (expr.Expr, error) {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if routine == nil || raw.ResolutionOnly || raw.Variadic || len(operands) != 0 { return missing() }
	if err := routine.Validate(); err != nil { return nil, err }
	declaration, ok := routine.(interface {
		PgBuffercacheAggregateDeclaration() (PgBuffercacheAggregateKind, []sqltype.CompositeMember)
	})
	if !ok { return nil, fmt.Errorf("stuntgres: missing pg_buffercache aggregate declaration") }
	kind, members := declaration.PgBuffercacheAggregateDeclaration()
	if kind > BuffercacheUsageCounts { return missing() }
	for _, label := range raw.ArgNames { if label.Text != "" { return missing() } }
	if err := CallDecorationError(raw); err != nil { return nil, err }
	if raw.Star {
		name := qualifiedName(raw.Name)
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil { return nil, err }
	return routine.Attach(PgBuffercacheAggregate{Info: b.Info(shape.Usage(), raw.Pos, expr.Properties{Volatility: expr.Volatile, StageDependent: true}), Kind: kind})
}
func (PgBuffercacheAggregate) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: pg_buffercache aggregate requires installed authority")
}
func (PgBuffercacheAggregate) Children() []expr.Expr { return nil }
func (n PgBuffercacheAggregate) ValidateArguments(children []expr.Expr) error {
	if n.Kind > BuffercacheUsageCounts || len(children) != 0 { return fmt.Errorf("stuntgres: invalid pg_buffercache aggregate arguments") }
	return nil
}
func (n PgBuffercacheAggregate) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := n.ValidateArguments(children); err != nil { return nil, err }
	n.Info = info
	return n, nil
}
func (n PgBuffercacheAggregate) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) { return n.Rebuild(info, children) }
func (PgBuffercacheAggregate) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (PgBuffercacheAggregate) PlanningFold() expr.Fold { return expr.FoldNever }
func (PgBuffercacheAggregate) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (PgBuffercacheAggregate) StrictNull() expr.Strict { return expr.StrictNone }
func (PgBuffercacheAggregate) LooksLikeFunction() bool { return true }
func (PgBuffercacheAggregate) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{}, kind == expr.CaptureFunction, nil
}
func (n PgBuffercacheAggregate) SourceAdmission() (bool, bool) { return true, n.Kind.Set() }

package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// PgstattupleStats owns one typed installed pgstattuple statistics operand.
// Operation, not routine spelling, selects the released shape and capture.
type PgstattupleStats struct {
	expr.Info
	Args  []expr.Expr
	Input sqltype.ID
	Operation PgstattupleOperation
}

type PgstattupleOperation uint8

const (
	BtreeStats PgstattupleOperation = iota
	HashStats
)

func (o PgstattupleOperation) Name() string {
	switch o {
	case BtreeStats:
		return "pgstatindex"
	case HashStats:
		return "pgstathashindex"
	default:
		return ""
	}
}

var pgstatindexOutputNames = [...]string{"version", "tree_level", "index_size", "root_block_no", "internal_pages", "leaf_pages", "empty_pages", "deleted_pages", "avg_leaf_density", "leaf_fragmentation"}
var pgstatindexOutputTypes = [...]sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Float8, sqltype.Float8}

func bindPgstattupleStats(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	authority, ok := routine.(interface{
		PgstattupleStatsInput() sqltype.ID
		PgstattupleOperation() PgstattupleOperation
	})
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing pgstattuple statistics declaration")
	}
	input, operation := authority.PgstattupleStatsInput(), authority.PgstattupleOperation()
	if raw.Star {
		return nil, expr.ErrorAt("42809", fmt.Sprintf("%s(*) specified, but %s is not an aggregate function", operation.Name(), operation.Name()), raw.Pos)
	}
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
	names, types := pgstatindexOutputNames[:], pgstatindexOutputTypes[:]
	if operation == HashStats {
		names = []string{"version", "bucket_pages", "overflow_pages", "bitmap_pages", "unused_pages", "live_items", "dead_items", "free_percent"}
		types = []sqltype.ID{sqltype.Int4, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Float8}
	} else if operation != BtreeStats {
		return nil, fmt.Errorf("stuntgres: invalid pgstattuple statistics operation")
	}
	members := make([]sqltype.CompositeMember, len(names))
	for i, name := range names {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: name, Usage: b.Type(types[i]).Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	n := PgstattupleStats{Args: []expr.Expr{child}, Input: input, Operation: operation}
	n.Info = b.Info(shape.Usage(), raw.Pos, expr.MergeProperties(expr.Properties{Volatility: expr.Volatile, StageDependent: true}, child.Properties()))
	return routine.Attach(n)
}
func namedPgstattupleStats(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	slots, ok := namedSlots(raw, []string{"relname"}, 1)
	if !ok || raw.Variadic || len(args) != 1 || len(declared) != 1 || declared[0].Type == nil || declared[0].Type.ID() != sqltype.Text && declared[0].Type.ID() != sqltype.Regclass {
		return nil, nil, false
	}
	return namedInputs(b, args, declared, slots[:1])
}
func (PgstattupleStats) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: pgstatindex requires installed invocation authority")
}
func (n PgstattupleStats) Children() []expr.Expr { return n.Args }
func (n PgstattupleStats) ValidateArguments(children []expr.Expr) error {
	if n.Operation.Name() == "" || n.Operation == HashStats && n.Input != sqltype.Regclass || n.Input != sqltype.Text && n.Input != sqltype.Regclass || len(children) != 1 || children[0] == nil || children[0].ResultUsage().Type == nil || children[0].ResultUsage().Type.BaseType().ID() != n.Input {
		return fmt.Errorf("stuntgres: invalid pgstatindex argument")
	}
	return nil
}
func (n PgstattupleStats) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := n.ValidateArguments(children); err != nil {
		return nil, err
	}
	n.Info = info
	n.Args = append([]expr.Expr(nil), children...)
	return n, nil
}
func (n PgstattupleStats) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (PgstattupleStats) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (PgstattupleStats) PlanningFold() expr.Fold              { return expr.FoldNever }
func (PgstattupleStats) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (PgstattupleStats) LooksLikeFunction() bool              { return true }
func (n PgstattupleStats) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Args: n.Args, Count: 1}, kind == expr.CaptureFunction, nil
}
func (PgstattupleStats) SourceAdmission() (bool, bool) { return true, false }
func (n PgstattupleStats) ScalarSourceOutName() string   { return n.Operation.Name() }

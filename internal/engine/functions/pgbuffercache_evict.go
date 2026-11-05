package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// PgBuffercacheEvictionKind is declaration authority, never mutable SQL spelling.
type PgBuffercacheEvictionKind uint8

const (
	EvictBuffer PgBuffercacheEvictionKind = iota
	EvictRelation
	EvictAll
)

func (k PgBuffercacheEvictionKind) Arguments() int {
	if k == EvictAll {
		return 0
	}
	return 1
}
func (k PgBuffercacheEvictionKind) Input() sqltype.ID {
	if k == EvictRelation {
		return sqltype.Regclass
	}
	return sqltype.Int4
}

type PgBuffercacheEviction struct {
	expr.Info
	Args []expr.Expr
	Kind PgBuffercacheEvictionKind
}

func bindPgBuffercacheEviction(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	operands := make([]Argument, len(raw.Args))
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
	// Shared overload resolution selects the authentic installed declaration;
	// the signature is only a hint when ordinary actual-type resolution applies.
	routine, err := b.InstalledRoutine(raw.Name, PgBuffercacheExtension, "")
	if err != nil {
		return nil, err
	}
	missing := func() (expr.Expr, error) {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if routine == nil || raw.ResolutionOnly || raw.Variadic {
		return missing()
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	declaration, ok := routine.(interface {
		PgBuffercacheEvictionDeclaration() (PgBuffercacheEvictionKind, []sqltype.CompositeMember)
	})
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing pg_buffercache eviction declaration")
	}
	kind, members := declaration.PgBuffercacheEvictionDeclaration()
	if kind > EvictAll || len(operands) != kind.Arguments() {
		return missing()
	}
	for _, label := range raw.ArgNames {
		if label.Text != "" {
			return missing()
		}
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if raw.Star {
		name := qualifiedName(raw.Name)
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	call := PgBuffercacheEviction{Kind: kind, Args: make([]expr.Expr, len(operands))}
	properties := expr.Properties{Volatility: expr.Volatile, StageDependent: true}
	for i, operand := range operands {
		target := b.Type(kind.Input()).Usage()
		if operand.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(operand.Usage, target, sqltype.Implicit); err != nil {
				return missing()
			}
		}
		child, err := b.CoerceArgument(operand, CoercionRequest{Intent: DirectFormalArgument, Target: target})
		if err != nil {
			return nil, err
		}
		call.Args[i] = child
		properties = expr.MergeProperties(properties, child.Properties())
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	call.Info = b.Info(shape.Usage(), raw.Pos, properties)
	return routine.Attach(call)
}
func (PgBuffercacheEviction) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: pg_buffercache eviction requires installed authority")
}
func (n PgBuffercacheEviction) Children() []expr.Expr { return n.Args }
func (n PgBuffercacheEviction) ValidateArguments(children []expr.Expr) error {
	if n.Kind > EvictAll || len(children) != n.Kind.Arguments() {
		return fmt.Errorf("stuntgres: invalid pg_buffercache eviction arguments")
	}
	for _, child := range children {
		if child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.BaseType().ID() != n.Kind.Input() {
			return fmt.Errorf("stuntgres: invalid pg_buffercache eviction input")
		}
	}
	return nil
}
func (n PgBuffercacheEviction) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := n.ValidateArguments(children); err != nil {
		return nil, err
	}
	n.Info, n.Args = info, append([]expr.Expr(nil), children...)
	return n, nil
}
func (n PgBuffercacheEviction) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (PgBuffercacheEviction) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (PgBuffercacheEviction) PlanningFold() expr.Fold { return expr.FoldNever }
func (PgBuffercacheEviction) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (PgBuffercacheEviction) StrictNull() expr.Strict { return expr.StrictNone }
func (PgBuffercacheEviction) LooksLikeFunction() bool { return true }
func (n PgBuffercacheEviction) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Args: n.Args, Count: len(n.Args)}, kind == expr.CaptureFunction, nil
}
func (PgBuffercacheEviction) SourceAdmission() (bool, bool) { return true, false }

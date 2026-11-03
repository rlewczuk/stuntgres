package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// PgvisibilityKind is a sealed operation, independent of mutable SQL names.
type PgvisibilityKind uint8

const (
	VisibilityMapBlock PgvisibilityKind = iota
	VisibilityPageBlock
	VisibilityMapRelation
	VisibilityPageRelation
	VisibilityMapSummary
	VisibilityCheckVisible
	VisibilityCheckFrozen
	VisibilityTruncateMap
)

func (k PgvisibilityKind) Block() bool { return k == VisibilityMapBlock || k == VisibilityPageBlock }
func (k PgvisibilityKind) Set() bool {
	return k == VisibilityMapRelation || k == VisibilityPageRelation || k.Check()
}
func (k PgvisibilityKind) Check() bool {
	return k == VisibilityCheckVisible || k == VisibilityCheckFrozen
}
func (k PgvisibilityKind) Page() bool { return k == VisibilityPageBlock || k == VisibilityPageRelation }

type Pgvisibility struct {
	expr.Info
	Args  []expr.Expr
	Slots [2]uint8
	Kind  PgvisibilityKind
}

func bindPgvisibility(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	operands := make([]Argument, len(raw.Args))
	for i, child := range raw.Args {
		argument, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = argument
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	signature := "2205"
	if len(operands) == 2 {
		signature = "2205,20"
	}
	routine, err := b.InstalledRoutine(raw.Name, PgvisibilityExtension, signature)
	if err != nil {
		return nil, err
	}
	if routine == nil || raw.ResolutionOnly || raw.Variadic {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	declaration, ok := routine.(interface {
		PgvisibilityDeclaration() (PgvisibilityKind, []sqltype.CompositeMember)
	})
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing pg_visibility declaration")
	}
	kind, members := declaration.PgvisibilityDeclaration()
	count := 1
	if kind.Block() {
		count = 2
	}
	labels := [...]string{"", "blkno"}
	slots, valid := namedSlots(raw, labels[:count], count)
	if !valid || len(operands) != count {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", raw.Name.Parts[len(raw.Name.Parts)-1].Text+"(*) specified, but "+raw.Name.Parts[len(raw.Name.Parts)-1].Text+" is not an aggregate function", raw.Pos)
	}
	call := Pgvisibility{Kind: kind, Args: make([]expr.Expr, count)}
	properties := expr.Properties{Volatility: expr.Volatile, StageDependent: true}
	for i, operand := range operands {
		typ := sqltype.Regclass
		if slots[i] == 1 {
			typ = sqltype.Int8
		}
		target := b.Type(typ).Usage()
		if operand.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(operand.Usage, target, sqltype.Implicit); err != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
			}
		}
		child, err := b.CoerceArgument(operand, CoercionRequest{Intent: DirectFormalArgument, Target: target})
		if err != nil {
			return nil, err
		}
		call.Args[i], call.Slots[i] = child, uint8(slots[i])
		properties = expr.MergeProperties(properties, child.Properties())
	}
	usage := b.Type(sqltype.Tid).Usage()
	if kind == VisibilityTruncateMap {
		usage = b.Type(sqltype.Void).Usage()
	} else if !kind.Check() {
		shape, err := sqltype.RecordShape(members)
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
	}
	call.Info = b.Info(usage, raw.Pos, properties)
	return routine.Attach(call)
}
func namedPgvisibility(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	count := len(declared)
	if count < 1 || count > 2 || len(args) != count || raw.Variadic {
		return nil, nil, false
	}
	types := [...]sqltype.ID{sqltype.Regclass, sqltype.Int8}
	labels := [...]string{"", "blkno"}
	slots, ok := namedSlots(raw, labels[:count], count)
	if !ok || !declaredTypes(declared, types[:count]) {
		return nil, nil, false
	}
	return namedInputs(b, args, declared, slots[:count])
}
func (Pgvisibility) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: pg_visibility requires installed invocation authority")
}
func (n Pgvisibility) Children() []expr.Expr { return n.Args }
func (n Pgvisibility) ValidateArguments(children []expr.Expr) error {
	count := 1
	if n.Kind.Block() {
		count = 2
	}
	if n.Kind > VisibilityTruncateMap || len(children) != count {
		return fmt.Errorf("stuntgres: invalid pg_visibility arguments")
	}
	seen := uint8(0)
	for i, child := range children {
		slot := n.Slots[i]
		if int(slot) >= count || seen&(1<<slot) != 0 || child == nil || child.ResultUsage().Type == nil {
			return fmt.Errorf("stuntgres: invalid pg_visibility argument")
		}
		typ := sqltype.Regclass
		if slot == 1 {
			typ = sqltype.Int8
		}
		if child.ResultUsage().Type.BaseType().ID() != typ {
			return fmt.Errorf("stuntgres: invalid pg_visibility argument type")
		}
		seen |= 1 << slot
	}
	return nil
}
func (n Pgvisibility) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := n.ValidateArguments(children); err != nil {
		return nil, err
	}
	n.Info, n.Args = info, append([]expr.Expr(nil), children...)
	return n, nil
}
func (n Pgvisibility) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (Pgvisibility) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (Pgvisibility) PlanningFold() expr.Fold              { return expr.FoldNever }
func (Pgvisibility) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (Pgvisibility) StrictNull() expr.Strict              { return expr.StrictNone }
func (Pgvisibility) LooksLikeFunction() bool              { return true }
func (n Pgvisibility) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Args: n.Args, Count: len(n.Args)}, kind == expr.CaptureFunction, nil
}
func (n Pgvisibility) SourceAdmission() (bool, bool) { return true, n.Kind.Set() }

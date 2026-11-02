package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Pgsurgery carries only the implemented freeze operation. Slots map lexical
// children to declared inputs without changing their evaluation order.
type Pgsurgery struct {
	expr.Info
	Args  []expr.Expr
	Slots [2]uint8
}

func bindPgsurgery(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	routine, err := b.InstalledRoutine(raw.Name, PgsurgeryExtension, "2205,1010")
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
	labels := [...]string{"reloid", "tids"}
	slots, valid := namedSlots(raw, labels[:], 2)
	if !valid || len(operands) != 2 {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", raw.Name.Parts[len(raw.Name.Parts)-1].Text+"(*) specified, but "+raw.Name.Parts[len(raw.Name.Parts)-1].Text+" is not an aggregate function", raw.Pos)
	}
	call := Pgsurgery{Args: make([]expr.Expr, 2)}
	properties := expr.Properties{Volatility: expr.Volatile, StageDependent: true}
	for i, operand := range operands {
		typ := sqltype.Regclass
		if slots[i] == 1 {
			typ = 1010
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
	call.Info = b.Info(b.Type(sqltype.Void).Usage(), raw.Pos, properties)
	return routine.Attach(call)
}
func namedPgsurgery(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if len(args) != 2 || raw.Variadic || !declaredTypes(declared, []sqltype.ID{sqltype.Regclass, 1010}) {
		return nil, nil, false
	}
	slots, ok := namedSlots(raw, []string{"reloid", "tids"}, 2)
	if !ok {
		return nil, nil, false
	}
	return namedInputs(b, args, declared, slots[:2])
}
func (Pgsurgery) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: pg_surgery requires installed invocation authority")
}
func (n Pgsurgery) Children() []expr.Expr { return n.Args }
func (n Pgsurgery) ValidateArguments(children []expr.Expr) error {
	if len(children) != 2 {
		return fmt.Errorf("stuntgres: invalid pg_surgery arguments")
	}
	seen := uint8(0)
	for i, child := range children {
		slot := n.Slots[i]
		if slot > 1 || seen&(1<<slot) != 0 || child == nil || child.ResultUsage().Type == nil {
			return fmt.Errorf("stuntgres: invalid pg_surgery argument")
		}
		typ := sqltype.Regclass
		if slot == 1 {
			typ = 1010
		}
		if child.ResultUsage().Type.BaseType().ID() != typ {
			return fmt.Errorf("stuntgres: invalid pg_surgery argument type")
		}
		seen |= 1 << slot
	}
	return nil
}
func (n Pgsurgery) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := n.ValidateArguments(children); err != nil {
		return nil, err
	}
	n.Info, n.Args = info, append([]expr.Expr(nil), children...)
	return n, nil
}
func (n Pgsurgery) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (Pgsurgery) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (Pgsurgery) PlanningFold() expr.Fold              { return expr.FoldNever }
func (Pgsurgery) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (Pgsurgery) StrictNull() expr.Strict              { return expr.StrictNone }
func (Pgsurgery) LooksLikeFunction() bool              { return true }
func (n Pgsurgery) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Args: n.Args, Count: len(n.Args)}, kind == expr.CaptureFunction, nil
}

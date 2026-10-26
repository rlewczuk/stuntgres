package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Amcheck keeps children in their written evaluation order. Slots map written
// children to formal arguments; named calls never reorder their effects.
type Amcheck struct {
	expr.Info
	Args   []expr.Expr
	Slots  [4]uint8
	Parent bool
}

func amcheckSlots(raw parser.FunctionExpr) ([7]int, bool) {
	labels := []string{"index", "heapallindexed", "checkunique"}
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text == "bt_index_parent_check" {
		labels = []string{"index", "heapallindexed", "rootdescend", "checkunique"}
	}
	if len(raw.Args) < 1 || len(raw.Args) > len(labels) {
		return [7]int{}, false
	}
	return namedSlots(raw, labels[:len(raw.Args)], len(raw.Args))
}
func bindAmcheck(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var inline [4]Argument
	operands := inline[:]
	if len(raw.Args) <= len(inline) {
		operands = inline[:len(raw.Args)]
	} else {
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
	slots, valid := amcheckSlots(raw)
	signature := ""
	if valid {
		signature = [...]string{"2205", "2205,16", "2205,16,16", "2205,16,16,16"}[len(raw.Args)-1]
	}
	routine, err := b.InstalledRoutine(raw.Name, AmcheckExtension, signature)
	if err != nil {
		return nil, err
	}
	if !valid || routine == nil || raw.ResolutionOnly || raw.Variadic {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", "amcheck(*) specified, but amcheck is not an aggregate function", raw.Pos)
	}
	n := Amcheck{Args: make([]expr.Expr, len(operands)), Parent: raw.Name.Parts[len(raw.Name.Parts)-1].Text == "bt_index_parent_check"}
	properties := expr.Properties{Volatility: expr.Volatile, StageDependent: true}
	for i, operand := range operands {
		typ := sqltype.Bool
		if slots[i] == 0 {
			typ = sqltype.Regclass
		}
		formal := b.Type(typ).Usage()
		if operand.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(operand.Usage, formal, sqltype.Implicit); err != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
			}
		}
		arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: DirectFormalArgument, Target: formal})
		if err != nil {
			return nil, err
		}
		n.Args[i], n.Slots[i] = arg, uint8(slots[i])
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	n.Info = b.Info(b.Type(sqltype.Void).Usage(), raw.Pos, properties)
	return routine.Attach(n)
}
func namedAmcheck(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	slots, valid := amcheckSlots(raw)
	if !valid || raw.Variadic || len(declared) != len(args) {
		return nil, nil, false
	}
	for i, usage := range declared {
		id := sqltype.Bool
		if i == 0 {
			id = sqltype.Regclass
		}
		if usage.Type == nil || usage.Type.ID() != id {
			return nil, nil, false
		}
	}
	return namedInputs(b, args, declared, slots[:len(args)])
}
func (Amcheck) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: amcheck requires installed invocation authority")
}
func (n Amcheck) Children() []expr.Expr { return n.Args }
func (n Amcheck) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	maximum := 3
	if n.Parent {
		maximum = 4
	}
	if len(children) < 1 || len(children) > maximum || len(children) != len(n.Args) {
		return nil, fmt.Errorf("stuntgres: invalid amcheck arguments")
	}
	var seen [4]bool
	for i, child := range children {
		slot := n.Slots[i]
		id := sqltype.Bool
		if slot == 0 {
			id = sqltype.Regclass
		}
		if int(slot) >= len(children) || seen[slot] || child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.ID() != id {
			return nil, fmt.Errorf("stuntgres: invalid amcheck argument")
		}
		seen[slot] = true
	}
	n.Info, n.Args = info, children
	return n, nil
}
func (n Amcheck) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (Amcheck) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (Amcheck) PlanningFold() expr.Fold              { return expr.FoldNever }
func (Amcheck) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (Amcheck) LooksLikeFunction() bool              { return true }
func (n Amcheck) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Args: n.Args, Count: len(n.Args)}, kind == expr.CaptureFunction, nil
}

package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var pgPrewarmTypes = [...]sqltype.ID{sqltype.Regclass, sqltype.Text, sqltype.Text, sqltype.Int8, sqltype.Int8}
var pgPrewarmNames = [...]string{"", "mode", "fork", "first_block", "last_block"}

// PgPrewarm retains supplied operands in lexical order followed by literal
// defaults. Slots maps each occurrence to its genuine five-input declaration.
// Neither binding nor reconstruction performs preload work.
type PgPrewarm struct {
	expr.Info
	Args []expr.Expr
	Slots [5]uint8
}

func bindPgPrewarm(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var small [5]Argument
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) { operands = make([]Argument, len(raw.Args)) }
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil { return nil, err }
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil { return nil, err }
	routine, err := b.InstalledRoutine(raw.Name, PgPrewarmExtension, "2205,25,25,20,20")
	if err != nil { return nil, err }
	slots, valid := namedSlots(raw, pgPrewarmNames[:], 1)
	valid = valid && routine != nil && !raw.ResolutionOnly && !raw.Variadic
	if valid {
		for i, arg := range operands {
			if arg.Usage.Type != nil && !b.CanCoerce(arg.Usage, b.Type(pgPrewarmTypes[slots[i]]).Usage(), sqltype.Implicit) { valid = false; break }
		}
	}
	if !valid { return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands)) }
	if err := routine.Validate(); err != nil { return nil, err }
	if err := CallDecorationError(raw); err != nil { return nil, err }
	if raw.Star { return nil, expr.ErrorAt("42809", "pg_prewarm(*) specified, but pg_prewarm is not an aggregate function", raw.Pos) }
	call := PgPrewarm{Args: make([]expr.Expr, 0, 5)}
	var seen uint8
	properties := expr.Properties{Volatility: expr.Volatile, StageDependent: true}
	for i, arg := range operands {
		slot := slots[i]
		child, err := b.CoerceArgument(arg, CoercionRequest{Intent: DirectFormalArgument, Target: b.Type(pgPrewarmTypes[slot]).Usage()})
		if err != nil { return nil, err }
		call.Slots[len(call.Args)] = uint8(slot)
		call.Args = append(call.Args, child)
		seen |= 1 << slot
		properties = expr.MergeProperties(properties, child.Properties())
	}
	for slot := range 5 {
		if seen & (1 << slot) != 0 { continue }
		var value sqltype.Value
		switch slot {
		case 1: value = sqltype.String(b.Type(sqltype.Text), "buffer")
		case 2: value = sqltype.String(b.Type(sqltype.Text), "main")
		case 3, 4: value = sqltype.Null(b.Type(sqltype.Int8))
		default: return nil, fmt.Errorf("stuntgres: missing pg_prewarm relation")
		}
		call.Slots[len(call.Args)] = uint8(slot)
		call.Args = append(call.Args, b.Literal(value, raw.Pos))
	}
	call.Info = b.Info(b.Type(sqltype.Int8).Usage(), raw.Pos, properties)
	return routine.Attach(call)
}
func namedPgPrewarm(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Variadic || !declaredTypes(declared, pgPrewarmTypes[:]) { return nil, nil, false }
	return fixedNamedInputs(b, raw, args, declared, pgPrewarmNames[:], 1)
}
func (PgPrewarm) Eval(expr.Context) (sqltype.Value, error) { return sqltype.Value{}, fmt.Errorf("stuntgres: pg_prewarm requires installed invocation authority") }
func (n PgPrewarm) Children() []expr.Expr { return n.Args }
func (n PgPrewarm) ValidateArguments(children []expr.Expr) error {
	if len(children) != 5 { return fmt.Errorf("stuntgres: invalid pg_prewarm arguments") }
	var seen uint8
	for i, child := range children {
		slot := n.Slots[i]
		if slot >= 5 || seen & (1 << slot) != 0 || child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.BaseType().ID() != pgPrewarmTypes[slot] { return fmt.Errorf("stuntgres: invalid pg_prewarm argument") }
		seen |= 1 << slot
	}
	return nil
}
func (n PgPrewarm) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := n.ValidateArguments(children); err != nil { return nil, err }
	n.Info, n.Args = info, append([]expr.Expr(nil), children...)
	return n, nil
}
func (n PgPrewarm) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) { return n.Rebuild(info, children) }
func (PgPrewarm) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (PgPrewarm) PlanningFold() expr.Fold { return expr.FoldNever }
func (PgPrewarm) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (PgPrewarm) StrictNull() expr.Strict { return expr.StrictNone }
func (PgPrewarm) LooksLikeFunction() bool { return true }
func (PgPrewarm) SourceAdmission() (bool, bool) { return true, false }
func (n PgPrewarm) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) { return expr.CaptureOperands{Args: n.Args, Count: len(n.Args)}, kind == expr.CaptureFunction, nil }

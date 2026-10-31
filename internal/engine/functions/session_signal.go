package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type SessionSignalKind uint8

const (
	SignalCancel SessionSignalKind = iota
	SignalTerminate
)

var sessionSignalNames = [...]string{"pg_cancel_backend", "pg_terminate_backend"}
var sessionSignalTypes = [...]sqltype.ID{sqltype.Int4, sqltype.Int8}
var sessionSignalLabels = [...]string{"pid", "timeout"}

func (k SessionSignalKind) Name() string {
	if int(k) >= len(sessionSignalNames) {
		return ""
	}
	return sessionSignalNames[k]
}

func (k SessionSignalKind) count() int {
	switch k {
	case SignalCancel:
		return 1
	case SignalTerminate:
		return 2
	default:
		return 0
	}
}

// SessionSignal retains only supplied immutable operands in written order.
// The declaration owns the omitted timeout; no target or actor survives binding.
type SessionSignal struct {
	expr.Info
	Kind  SessionSignalKind
	Arity uint8
	Args  [2]expr.Expr
	Slots [2]uint8
}

func namedSessionSignal(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text != "pg_terminate_backend" || !declaredTypes(declared, sessionSignalTypes[:]) {
		return nil, nil, false
	}
	return fixedNamedInputs(b, raw, args, declared, sessionSignalLabels[:], 1)
}

func bindSessionSignal(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [2]Argument
	var inline [2]string
	names := inline[:min(len(raw.Args), 2)]
	if len(raw.Args) > 2 && len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < 2 {
			operands[i] = arg
		}
		if i < len(names) {
			typ := "unknown"
			if arg.Usage.Type != nil {
				typ = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, typ)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := SessionSignalKind(255)
	for i, candidate := range sessionSignalNames {
		if name == candidate {
			kind = SessionSignalKind(i)
		}
	}
	count := kind.count()
	slots, valid := namedSlots(raw, sessionSignalLabels[:count], 1)
	if kind == SignalCancel && len(raw.ArgNames) != 0 {
		valid = false
	}
	if !visible || count == 0 || !valid || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	node := SessionSignal{Kind: kind, Arity: uint8(len(raw.Args))}
	properties := expr.Properties{Volatility: expr.Volatile}
	for i := range raw.Args {
		target := b.Type(sessionSignalTypes[slots[i]]).Usage()
		if operands[i].Usage.Type != nil {
			if _, failure := sqltype.PlanCoercion(operands[i].Usage, target, sqltype.Implicit); failure != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
		}
		arg, err := b.CoerceArgument(operands[i], CoercionRequest{Intent: ExactFormalArgument, Target: target})
		if err != nil {
			return nil, err
		}
		node.Args[i], node.Slots[i] = arg, uint8(slots[i])
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	node.Info, err = b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, sessionSignalTypes[:count]...)
	return node, err
}

func (n SessionSignal) Validate() error {
	count := n.Kind.count()
	if count == 0 || n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Bool || n.Usage != n.Usage.Type.Usage() || n.Arity < 1 || int(n.Arity) > count {
		return fmt.Errorf("stuntgres: invalid session signal signature")
	}
	var seen [2]bool
	for i := range int(n.Arity) {
		slot, arg := n.Slots[i], n.Args[i]
		if int(slot) >= count || seen[slot] || arg == nil {
			return fmt.Errorf("stuntgres: invalid session signal mapping")
		}
		seen[slot] = true
		u := arg.ResultUsage()
		if u.Type == nil || u != u.Type.Usage() || u.Type.ID() != sessionSignalTypes[slot] {
			return fmt.Errorf("stuntgres: invalid session signal operand")
		}
	}
	if !seen[0] {
		return fmt.Errorf("stuntgres: missing session signal PID")
	}
	for i := int(n.Arity); i < 2; i++ {
		if n.Args[i] != nil || n.Slots[i] != 0 {
			return fmt.Errorf("stuntgres: unused session signal operand")
		}
	}
	return nil
}

// SessionSignalRuntime borrows the executing statement, never prepared authority.
type SessionSignalRuntime interface {
	SessionSignalValue(SessionSignalKind, int64, int64, *sqltype.Descriptor) (sqltype.Value, error)
}

func (n SessionSignal) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]int64
	null := false
	for i := range int(n.Arity) {
		v, err := n.Args[i].Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if v.IsNull() {
			null = true
			continue
		}
		values[n.Slots[i]], err = v.Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	runtime, ok := ctx.(SessionSignalRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing session signal runtime")
	}
	return runtime.SessionSignalValue(n.Kind, values[0], values[1], n.Usage.Type)
}

func (n SessionSignal) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}

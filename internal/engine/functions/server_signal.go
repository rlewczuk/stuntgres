package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ServerSignalKind identifies a request, never physical process state.
type ServerSignalKind uint8

const (
	SignalReload ServerSignalKind = iota
	SignalRotate
	SignalMemory
)

var serverSignalNames = [...]string{"pg_reload_conf", "pg_rotate_logfile", "pg_log_backend_memory_contexts"}
var serverSignalInputs = [...]sqltype.ID{sqltype.Int4}

func (k ServerSignalKind) Name() string {
	if int(k) >= len(serverSignalNames) { return "" }
	return serverSignalNames[k]
}

// ServerSignal retains only typed operands and ordinary routine provenance.
type ServerSignal struct {
	expr.Info
	Kind ServerSignalKind
	Args [1]expr.Expr
}

func bindServerSignal(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operand Argument
	var inline [1]string
	names := inline[:min(len(raw.Args), 1)]
	if len(raw.Args) > 1 && len(raw.Args) <= 100 { names = make([]string, len(raw.Args)) }
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil { return nil, err }
		if i == 0 { operand = arg }
		if i < len(names) {
			name := "unknown"
			if arg.Usage.Type != nil { name = b.DiagnosticTypeName(arg.Usage.Type) }
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 { return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos) }
	if err := ValidateCallSyntax(b, raw); err != nil { return nil, err }
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil { return nil, err }
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := ServerSignalKind(255)
	for i, candidate := range serverSignalNames {
		if name == candidate { kind = ServerSignalKind(i); break }
	}
	count := 0
	if kind == SignalMemory { count = 1 }
	if !visible || kind.Name() == "" || len(raw.Args) != count || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	node := ServerSignal{Kind: kind}
	properties := expr.Properties{Volatility: expr.Volatile}
	if count == 1 {
		target := b.Type(sqltype.Int4).Usage()
		if operand.Usage.Type != nil {
			if _, failure := sqltype.PlanCoercion(operand.Usage, target, sqltype.Implicit); failure != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
		}
		arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: target})
		if err != nil { return nil, err }
		if arg.ResultUsage() != target {
			plan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
			if failure != nil { return nil, failure }
			arg, err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[0], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
			if err != nil { return nil, err }
		}
		node.Args[0] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if raw.Star { return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos) }
	if err := CallDecorationError(raw); err != nil { return nil, err }
	node.Info, err = b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, serverSignalInputs[:count]...)
	if err != nil { return nil, err }
	return node, nil
}

func (n ServerSignal) Validate() error {
	if n.Kind.Name() == "" || n.Usage.Type == nil || n.Usage.Type.ID() != sqltype.Bool || n.Usage != n.Usage.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid server signal signature")
	}
	if n.Kind == SignalMemory {
		if n.Args[0] == nil { return fmt.Errorf("stuntgres: missing server signal PID") }
		u := n.Args[0].ResultUsage()
		if u.Type == nil || u.Type.ID() != sqltype.Int4 || u != u.Type.Usage() {
			return fmt.Errorf("stuntgres: invalid server signal PID")
		}
	} else if n.Args[0] != nil {
		return fmt.Errorf("stuntgres: unexpected server signal operand")
	}
	return nil
}

func (n ServerSignal) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil { return sqltype.Value{}, err }
	if err := n.Validate(); err != nil { return sqltype.Value{}, err }
	var pid int64
	if n.Kind == SignalMemory {
		v, err := n.Args[0].Eval(ctx)
		if err != nil { return sqltype.Value{}, err }
		if v.IsNull() { return sqltype.Null(n.Usage.Type), nil }
		pid, err = v.Integer()
		if err != nil { return sqltype.Value{}, err }
	}
	runtime, ok := ctx.(ServerSignalRuntime)
	if !ok { return sqltype.Value{}, fmt.Errorf("stuntgres: missing server signal runtime") }
	return runtime.ServerSignalValue(n.Kind, pid, n.Usage.Type)
}

func (n ServerSignal) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.ScalarSource(value), nil
}

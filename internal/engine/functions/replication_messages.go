package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type ReplicationMessagesKind uint8

const (
	ReplicationMessageText ReplicationMessagesKind = iota
	ReplicationMessageBinary
	ReplicationSyncSlots
)

var replicationMessagesNames = [...]string{"pg_logical_emit_message", "pg_sync_replication_slots"}
var replicationMessagesLabels = [...]string{"transactional", "prefix", "message", "flush"}

func (k ReplicationMessagesKind) Name() string {
	if k <= ReplicationMessageBinary {
		return replicationMessagesNames[0]
	}
	if k == ReplicationSyncSlots {
		return replicationMessagesNames[1]
	}
	return ""
}
func (k ReplicationMessagesKind) signature() (types [4]sqltype.ID, count, required int, result sqltype.ID) {
	if k == ReplicationSyncSlots {
		return types, 0, 0, sqltype.Void
	}
	if k > ReplicationMessageBinary {
		return
	}
	message := sqltype.Text
	if k == ReplicationMessageBinary {
		message = sqltype.Bytea
	}
	return [4]sqltype.ID{sqltype.Bool, sqltype.Text, message, sqltype.Bool}, 4, 3, sqltype.PgLSN
}

// ReplicationMessages owns supplied children in written order. Omitted flush is
// the declaration's false default, not a borrowed datum or explicit NULL.
type ReplicationMessages struct {
	expr.Info
	Kind  ReplicationMessagesKind
	Arity uint8
	Args  [4]expr.Expr
	Slots [4]uint8
}

func namedReplicationMessages(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text != replicationMessagesNames[0] {
		return nil, nil, false
	}
	for _, kind := range []ReplicationMessagesKind{ReplicationMessageText, ReplicationMessageBinary} {
		types, count, required, _ := kind.signature()
		if declaredTypes(declared, types[:count]) {
			return fixedNamedInputs(b, raw, args, declared, replicationMessagesLabels[:], required)
		}
	}
	return nil, nil, false
}
func bindReplicationMessages(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [4]Argument
	var inline [4]string
	names := inline[:min(len(raw.Args), 4)]
	if len(raw.Args) > 4 && len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < 4 {
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
	kind := ReplicationMessagesKind(255)
	if name == replicationMessagesNames[0] {
		kind = ReplicationMessageText
	}
	if name == replicationMessagesNames[1] {
		kind = ReplicationSyncSlots
	}
	types, count, required, result := kind.signature()
	slots, valid := namedSlots(raw, replicationMessagesLabels[:count], required)
	if !visible || kind.Name() == "" || !valid || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	// The ordinary SQL resolver has already coerced known operands to its winning
	// declaration. Preserve that concrete bytea selection rather than inspecting values.
	for i := range raw.Args {
		if slots[i] == 2 && operands[i].Usage.Type != nil && operands[i].Usage.Type.BaseType().ID() == sqltype.Bytea {
			kind = ReplicationMessageBinary
		}
	}
	types, count, _, result = kind.signature()
	node := ReplicationMessages{Kind: kind, Arity: uint8(len(raw.Args))}
	properties := expr.Properties{Volatility: expr.Volatile}
	for i := range raw.Args {
		target := b.Type(types[slots[i]]).Usage()
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
	node.Info, err = b.BuiltinRoutineInfo(b.Info(b.Type(result).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, types[:count]...)
	return node, err
}
func (n ReplicationMessages) Validate() error {
	types, count, required, result := n.Kind.signature()
	if n.Kind.Name() == "" || n.Usage.Type == nil || n.Usage.Type.ID() != result || n.Usage != n.Usage.Type.Usage() || int(n.Arity) < required || int(n.Arity) > count {
		return fmt.Errorf("stuntgres: invalid replication messages signature")
	}
	var seen [4]bool
	for i := range int(n.Arity) {
		slot := n.Slots[i]
		arg := n.Args[i]
		if int(slot) >= count || seen[slot] || arg == nil {
			return fmt.Errorf("stuntgres: invalid replication messages mapping")
		}
		seen[slot] = true
		usage := arg.ResultUsage()
		if usage.Type == nil || usage != usage.Type.Usage() || usage.Type.ID() != types[slot] {
			return fmt.Errorf("stuntgres: invalid replication messages operand")
		}
	}
	for i := range required {
		if !seen[i] {
			return fmt.Errorf("stuntgres: missing replication messages operand")
		}
	}
	for i := int(n.Arity); i < 4; i++ {
		if n.Args[i] != nil || n.Slots[i] != 0 {
			return fmt.Errorf("stuntgres: unused replication messages operand")
		}
	}
	return nil
}

type SyncReplicationSlotsRuntime interface{ SyncReplicationSlots() error }

func (n ReplicationMessages) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	null := false
	for i := range int(n.Arity) {
		value, err := n.Args[i].Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	if n.Kind == ReplicationSyncSlots {
		runtime, ok := ctx.(SyncReplicationSlotsRuntime)
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: missing replication synchronization runtime")
		}
		return sqltype.Value{}, runtime.SyncReplicationSlots()
	}
	return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "pg_logical_emit_message logical WAL publication is unsupported in embedded mode"}
}
func (n ReplicationMessages) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}

package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// RecoveryControlKind selects a primary-mode recovery precondition.
type RecoveryControlKind uint8

const (
	RecoveryReplayPaused RecoveryControlKind = iota
	RecoveryReplayPauseState
	RecoveryPromote
	RecoveryReplayPause
	RecoveryReplayResume
)

var recoveryControlNames = [...]string{"pg_is_wal_replay_paused", "pg_get_wal_replay_pause_state", "pg_promote", "pg_wal_replay_pause", "pg_wal_replay_resume"}
var recoveryControlLabels = [...]string{"wait", "wait_seconds"}
var recoveryControlInputs = [...]sqltype.ID{sqltype.Bool, sqltype.Int4}

func (k RecoveryControlKind) Name() string {
	if k > RecoveryReplayResume { return "" }
	return recoveryControlNames[k]
}
func (k RecoveryControlKind) result() sqltype.ID {
	switch k {
	case RecoveryReplayPaused, RecoveryPromote: return sqltype.Bool
	case RecoveryReplayPauseState: return sqltype.Text
	case RecoveryReplayPause, RecoveryReplayResume: return sqltype.Void
	}
	return 0
}

// RecoveryControl owns only supplied operands in written order and their formal
// destinations. Omitted promotion inputs denote the declaration's true/60 defaults.
// It retains neither authorization nor simulated recovery state.
type RecoveryControl struct {
	expr.Info
	Kind RecoveryControlKind
	Arity uint8
	Args [2]expr.Expr
	Slots [2]uint8
}

func namedRecoveryControl(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text != recoveryControlNames[RecoveryPromote] || !declaredTypes(declared, recoveryControlInputs[:]) { return nil, nil, false }
	return fixedNamedInputs(b, raw, args, declared, recoveryControlLabels[:], 0)
}

func bindRecoveryControl(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [2]Argument
	var inline [2]string
	names := inline[:min(len(raw.Args), 2)]
	if len(raw.Args) > 2 && len(raw.Args) <= 100 { names = make([]string, len(raw.Args)) }
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil { return nil, err }
		if i < 2 { operands[i] = arg }
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
	kind := RecoveryControlKind(255)
	for i, candidate := range recoveryControlNames { if name == candidate { kind = RecoveryControlKind(i); break } }
	formalCount := 0
	if kind == RecoveryPromote { formalCount = 2 }
	slots, valid := namedSlots(raw, recoveryControlLabels[:formalCount], 0)
	if !visible || kind.Name() == "" || !valid || raw.Variadic || raw.ResolutionOnly { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
	for i := range raw.Args {
		if operands[i].Usage.Type != nil {
			if _, failure := sqltype.PlanCoercion(operands[i].Usage, b.Type(recoveryControlInputs[slots[i]]).Usage(), sqltype.Implicit); failure != nil { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
		}
	}
	node := RecoveryControl{Kind: kind, Arity: uint8(len(raw.Args))}
	properties := expr.Properties{Volatility: expr.Volatile}
	for i := range raw.Args {
		target := b.Type(recoveryControlInputs[slots[i]]).Usage()
		arg, err := b.CoerceArgument(operands[i], CoercionRequest{Intent: FinalizeArgument, Target: target})
		if err != nil { return nil, err }
		if arg.ResultUsage() != target {
			plan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
			if failure != nil { return nil, failure }
			arg, err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[i], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
			if err != nil { return nil, err }
		}
		node.Args[i], node.Slots[i] = arg, uint8(slots[i])
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if raw.Star { return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos) }
	if err := CallDecorationError(raw); err != nil { return nil, err }
	node.Info, err = b.BuiltinRoutineInfo(b.Info(b.Type(kind.result()).Usage(), raw.Pos, properties), CatalogRoutineProvider, name, recoveryControlInputs[:formalCount]...)
	if err != nil { return nil, err }
	return node, nil
}

func (n RecoveryControl) Validate() error {
	if n.Kind.Name() == "" || n.Usage.Type == nil || n.Usage.Type.ID() != n.Kind.result() || n.Usage != n.Usage.Type.Usage() || n.Arity > 2 || n.Kind != RecoveryPromote && n.Arity != 0 { return fmt.Errorf("stuntgres: invalid recovery control signature") }
	var seen [2]bool
	for i := range int(n.Arity) {
		slot := n.Slots[i]
		if slot >= 2 || seen[slot] || n.Args[i] == nil { return fmt.Errorf("stuntgres: invalid recovery control operand mapping") }
		seen[slot] = true
		usage := n.Args[i].ResultUsage()
		if usage.Type == nil || usage != usage.Type.Usage() || usage.Type.ID() != recoveryControlInputs[slot] { return fmt.Errorf("stuntgres: invalid recovery control operand type") }
	}
	for i := int(n.Arity); i < 2; i++ { if n.Args[i] != nil || n.Slots[i] != 0 { return fmt.Errorf("stuntgres: unused recovery control operand") } }
	return nil
}

func (n RecoveryControl) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil { return sqltype.Value{}, err }
	if err := n.Validate(); err != nil { return sqltype.Value{}, err }
	null := false
	for i := range int(n.Arity) {
		value, err := n.Args[i].Eval(ctx)
		if err != nil { return sqltype.Value{}, err }
		null = null || value.IsNull()
	}
	if null { return sqltype.Null(n.Usage.Type), nil }
	return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "recovery is not in progress", Hint: "Recovery control functions can only be executed during recovery."}
}
func (n RecoveryControl) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.ScalarSource(value), nil
}

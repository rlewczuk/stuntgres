package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// WALSummaryKind distinguishes physical inquiries without retaining physical state.
type WALSummaryKind uint8

const (
	WALSummaryAvailable WALSummaryKind = iota
	WALSummaryContents
	WALSummaryState
)

var walSummaryNames = [...]string{"pg_available_wal_summaries", "pg_wal_summary_contents", "pg_get_wal_summarizer_state"}
var walSummaryLabels = [...]string{"tli", "start_lsn", "end_lsn"}
var walSummaryInputs = [...]sqltype.ID{sqltype.Int8, sqltype.PgLSN, sqltype.PgLSN}
var walSummaryContentLabels = [...]string{"relfilenode", "reltablespace", "reldatabase", "relforknumber", "relblocknumber", "is_limit_block"}
var walSummaryContentTypes = [...]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Oid, sqltype.Int2, sqltype.Int8, sqltype.Bool}
var walSummaryStateLabels = [...]string{"summarized_tli", "summarized_lsn", "pending_lsn", "summarizer_pid"}
var walSummaryStateTypes = [...]sqltype.ID{sqltype.Int8, sqltype.PgLSN, sqltype.PgLSN, sqltype.Int4}

func (k WALSummaryKind) Name() string {
	if k > WALSummaryState {
		return ""
	}
	return walSummaryNames[k]
}
func (k WALSummaryKind) members() ([]string, []sqltype.ID) {
	switch k {
	case WALSummaryAvailable:
		return walSummaryLabels[:], walSummaryInputs[:]
	case WALSummaryContents:
		return walSummaryContentLabels[:], walSummaryContentTypes[:]
	case WALSummaryState:
		return walSummaryStateLabels[:], walSummaryStateTypes[:]
	}
	return nil, nil
}

// WALSummary owns written operands and their formal slots, never authority or cursors.
type WALSummary struct {
	expr.Info
	Kind  WALSummaryKind
	Arity uint8
	Args  [3]expr.Expr
	Slots [3]uint8
}

func namedWALSummary(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text != walSummaryNames[WALSummaryContents] || !declaredTypes(declared, walSummaryInputs[:]) {
		return nil, nil, false
	}
	return fixedNamedInputs(b, raw, args, declared, walSummaryLabels[:], 3)
}
func bindWALSummary(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [3]Argument
	var inline [3]string
	names := inline[:min(len(raw.Args), 3)]
	if len(raw.Args) > 3 && len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < 3 {
			operands[i] = arg
		}
		if i < len(names) {
			name := "unknown"
			if arg.Usage.Type != nil {
				name = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, name)
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
	kind := WALSummaryKind(3)
	for i, candidate := range walSummaryNames {
		if name == candidate {
			kind = WALSummaryKind(i)
			break
		}
	}
	arity := 0
	if kind == WALSummaryContents {
		arity = 3
	}
	slots, valid := namedSlots(raw, walSummaryLabels[:arity], arity)
	if !visible || kind.Name() == "" || !valid || len(raw.Args) != arity || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	for i := range arity {
		if operands[i].Usage.Type != nil {
			if _, failure := sqltype.PlanCoercion(operands[i].Usage, b.Type(walSummaryInputs[slots[i]]).Usage(), sqltype.Implicit); failure != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
		}
	}
	node := WALSummary{Kind: kind, Arity: uint8(arity)}
	properties := expr.Properties{Volatility: expr.Volatile}
	for i := range arity {
		target := b.Type(walSummaryInputs[slots[i]]).Usage()
		arg, err := b.CoerceArgument(operands[i], CoercionRequest{Intent: FinalizeArgument, Target: target})
		if err != nil {
			return nil, err
		}
		if arg.ResultUsage() != target {
			plan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
			if failure != nil {
				return nil, failure
			}
			arg, err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[i], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
			if err != nil {
				return nil, err
			}
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
	if kind != WALSummaryState {
		if err := b.AdmitSetReturning(raw.Pos); err != nil {
			return nil, err
		}
	}
	labels, types := kind.members()
	members := make([]sqltype.CompositeMember, len(labels))
	for i, label := range labels {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: label, Usage: b.Type(types[i]).Usage()}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	node.Info, err = b.BuiltinRoutineInfo(b.Info(shape.Usage(), raw.Pos, properties), CatalogRoutineProvider, name, walSummaryInputs[:arity]...)
	if err != nil {
		return nil, err
	}
	return node, nil
}
func (n WALSummary) Validate() error {
	arity := uint8(0)
	if n.Kind == WALSummaryContents {
		arity = 3
	}
	if n.Kind.Name() == "" || n.Arity != arity {
		return fmt.Errorf("stuntgres: invalid WAL summary signature")
	}
	var seen [3]bool
	for i := range int(n.Arity) {
		slot := n.Slots[i]
		if slot >= 3 || seen[slot] || n.Args[i] == nil {
			return fmt.Errorf("stuntgres: invalid WAL summary operand mapping")
		}
		seen[slot] = true
		usage := n.Args[i].ResultUsage()
		if usage.Type == nil || usage != usage.Type.Usage() || usage.Type.ID() != walSummaryInputs[slot] {
			return fmt.Errorf("stuntgres: invalid WAL summary operand type")
		}
	}
	for i := int(n.Arity); i < 3; i++ {
		if n.Args[i] != nil || n.Slots[i] != 0 {
			return fmt.Errorf("stuntgres: unused WAL summary operand")
		}
	}
	labels, types := n.Kind.members()
	d := n.Usage.Type
	if d == nil || d.ID() != 2249 || !d.IsComposite() || d.CompositeLen() != len(labels) || n.Usage != d.Usage() {
		return fmt.Errorf("stuntgres: invalid WAL summary record")
	}
	for i, label := range labels {
		m := d.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != label || m.Usage.Type == nil || m.Usage.Type.ID() != types[i] || m.Usage != m.Usage.Type.Usage() {
			return fmt.Errorf("stuntgres: invalid WAL summary member")
		}
	}
	return nil
}
func (n WALSummary) unsupported() error {
	mechanism := "physical WAL summary file listing"
	if n.Kind == WALSummaryContents {
		mechanism = "physical WAL summary file access"
	}
	if n.Kind == WALSummaryState {
		mechanism = "physical WAL summarizer state"
	}
	return expr.ErrorAt("0A000", n.Kind.Name()+" "+mechanism+" is unsupported in embedded mode", parser.Position{})
}
func (n WALSummary) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	if n.Kind != WALSummaryState {
		return sqltype.Value{}, fmt.Errorf("stuntgres: WAL summary set used as scalar")
	}
	return sqltype.Value{}, n.unsupported()
}
func (n WALSummary) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return expr.SourceCursor{}, err
	}
	if err := n.Validate(); err != nil {
		return expr.SourceCursor{}, err
	}
	var values [3]sqltype.Value
	null := false
	for i := range int(n.Arity) {
		value, err := n.Args[i].Eval(ctx)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		values[n.Slots[i]] = value
		null = null || value.IsNull()
	}
	if null {
		return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, Exhausted: true}), nil
	}
	if n.Kind == WALSummaryContents {
		timeline, err := values[0].Integer()
		if err != nil {
			return expr.SourceCursor{}, err
		}
		if timeline < 1 || timeline > 2147483647 {
			return expr.SourceCursor{}, expr.ErrorAt("22023", fmt.Sprintf("invalid timeline %d", timeline), parser.Position{})
		}
	}
	return expr.SourceCursor{}, n.unsupported()
}

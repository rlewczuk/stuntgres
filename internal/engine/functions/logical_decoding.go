package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var logicalDecodingNames = [...]string{"pg_logical_slot_get_changes", "pg_logical_slot_get_binary_changes", "pg_logical_slot_peek_changes", "pg_logical_slot_peek_binary_changes"}
var logicalDecodingFormals = [...]sqltype.ID{sqltype.Name, sqltype.PgLSN, sqltype.Int4, 1009}
var logicalDecodingLabels = [...]string{"slot_name", "upto_lsn", "upto_nchanges", "options"}

// LogicalDecoding retains written operands and immutable formal/result types,
// never an actor, replication slot, decoder or cursor.
type LogicalDecoding struct {
	expr.Info
	Kind uint8
	Args []expr.Expr
	Slots []uint8
	Labels []string
	Variadic bool
	EmptyOptions sqltype.Value
}

func logicalDecodingMapping(raw parser.FunctionExpr) ([]uint8, bool) {
	if len(raw.Args) < 3 || raw.Variadic && len(raw.Args) != 4 { return nil, false }
	slots := make([]uint8, len(raw.Args))
	if len(raw.ArgNames) != 0 {
		// PostgreSQL does not expand a variadic formal in named notation,
		// including an omitted variadic formal (18.6 oracle tests-1).
		if !raw.Variadic { return nil, false }
		mapped, ok := namedSlots(raw, logicalDecodingLabels[:], 4)
		if !ok || mapped[len(raw.Args)-1] != 3 { return nil, false }
		for i := range slots { slots[i] = uint8(mapped[i]) }
	} else {
		for i := range slots { slots[i] = uint8(min(i, 3)) }
	}
	return slots, true
}

func bindLogicalDecoding(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var local [8]Argument
	operands := local[:min(len(raw.Args), len(local))]
	if len(raw.Args) > len(local) && len(raw.Args) <= 100 { operands = make([]Argument, len(raw.Args)) }
	for i, child := range raw.Args {
		o, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil { return nil, err }
		if i < len(operands) { operands[i] = o }
	}
	if len(raw.Args) > 100 { return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos) }
	if err := ValidateCallSyntax(b, raw); err != nil { return nil, err }
	signature := func() (expr.Expr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			name := "unknown"
			if o.Usage.Type != nil { name = b.DiagnosticTypeName(o.Usage.Type) }
			names[i] = NamedArgumentType(raw, i, name)
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil { return nil, err }
	slots, valid := logicalDecodingMapping(raw)
	if !visible || !valid || raw.ResolutionOnly { return signature() }
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := uint8(len(logicalDecodingNames))
	for i, candidate := range logicalDecodingNames { if name == candidate { kind = uint8(i); break } }
	if int(kind) == len(logicalDecodingNames) { return signature() }
	target := func(i int) sqltype.Usage {
		id := logicalDecodingFormals[slots[i]]
		if slots[i] == 3 && !raw.Variadic { id = sqltype.Text }
		return b.Type(id).Usage()
	}
	for i, o := range operands {
		if o.Usage.Type != nil {
			if _, failure := sqltype.PlanCoercion(o.Usage, target(i), sqltype.Implicit); failure != nil { return signature() }
		}
	}
	node := LogicalDecoding{Kind: kind, Args: make([]expr.Expr, len(operands)), Slots: slots, Variadic: raw.Variadic}
	if len(raw.ArgNames) != 0 {
		node.Labels = make([]string, len(raw.ArgNames))
		for i, label := range raw.ArgNames { node.Labels[i] = label.Text }
	}
	properties := expr.Properties{Volatility: expr.Volatile}
	for i, o := range operands {
		arg, err := b.CoerceArgument(o, CoercionRequest{Intent: ExactFormalArgument, Target: target(i)})
		if err != nil { return nil, err }
		node.Args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if raw.Star { return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos) }
	if err := CallDecorationError(raw); err != nil { return nil, err }
	if err := b.AdmitSetReturning(raw.Pos); err != nil { return nil, err }
	data := sqltype.Text
	if kind%2 != 0 { data = sqltype.Bytea }
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{
		{Attribute: 1, Name: "lsn", Usage: b.Type(sqltype.PgLSN).Usage()},
		{Attribute: 2, Name: "xid", Usage: b.Type(sqltype.Xid).Usage()},
		{Attribute: 3, Name: "data", Usage: b.Type(data).Usage()},
	})
	if err != nil { return nil, err }
	node.EmptyOptions, err = sqltype.OwnedArrayValue(b.Type(1009), nil, nil)
	if err != nil { return nil, err }
	node.Info, err = b.BuiltinRoutineInfo(b.Info(shape.Usage(), raw.Pos, properties), CatalogRoutineProvider, name, logicalDecodingFormals[:]...)
	if err != nil { return nil, err }
	return node, nil
}

func namedLogicalDecoding(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if !raw.Variadic || len(args) != 4 || !declaredTypes(declared, logicalDecodingFormals[:]) { return nil, nil, false }
	slots, ok := logicalDecodingMapping(raw)
	if !ok { return nil, nil, false }
	var mapped [4]int
	for i, slot := range slots { mapped[i] = int(slot) }
	return namedInputs(b, args, declared, mapped[:len(slots)])
}

func (n LogicalDecoding) Validate() error {
	if int(n.Kind) >= len(logicalDecodingNames) || len(n.Args) < 3 || len(n.Args) > 100 || len(n.Slots) != len(n.Args) || len(n.Labels) != 0 && len(n.Labels) != len(n.Args) || n.Variadic && len(n.Args) != 4 { return fmt.Errorf("stuntgres: invalid logical decoding expression") }
	var supplied [4]bool
	for i, arg := range n.Args {
		slot := n.Slots[i]
		if slot > 3 || slot < 3 && supplied[slot] { return fmt.Errorf("stuntgres: invalid logical decoding formal mapping") }
		supplied[slot] = true
		id := logicalDecodingFormals[slot]
		if slot == 3 && !n.Variadic { id = sqltype.Text }
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != id { return fmt.Errorf("stuntgres: invalid logical decoding operand") }
	}
	if !supplied[0] || !supplied[1] || !supplied[2] || n.EmptyOptions.Type() == nil || n.EmptyOptions.Type().ID() != 1009 || n.EmptyOptions.IsNull() || n.EmptyOptions.ArrayLen() != 0 { return fmt.Errorf("stuntgres: invalid logical decoding defaults") }
	d := n.Usage.Type
	if d == nil || d.ID() != 2249 || !d.IsComposite() || d.CompositeLen() != 3 { return fmt.Errorf("stuntgres: invalid logical decoding record") }
	types := [3]sqltype.ID{sqltype.PgLSN, sqltype.Xid, sqltype.Text}
	if n.Kind%2 != 0 { types[2] = sqltype.Bytea }
	for i, name := range [3]string{"lsn", "xid", "data"} {
		m := d.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != name || m.Usage.Type == nil || m.Usage.Type.ID() != types[i] { return fmt.Errorf("stuntgres: invalid logical decoding result member") }
	}
	return nil
}

func (LogicalDecoding) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: logical decoding set used as scalar")
}

// LogicalDecodingRuntime is the used execution-authority bridge, not a slot or
// decoder provider. Every demanded call reaches it after evaluating operands.
type LogicalDecodingRuntime interface { LogicalDecoding(sqltype.Value, sqltype.Value) error }

func (n LogicalDecoding) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if _, err := evaluationContext(ctx); err != nil { return expr.SourceCursor{}, err }
	var slot sqltype.Value
	options := n.EmptyOptions
	var expanded []sqltype.Value
	if !n.Variadic && len(n.Args) > 3 { expanded = make([]sqltype.Value, len(n.Args)-3) }
	at := 0
	for i, arg := range n.Args {
		value, err := arg.Eval(ctx)
		if err != nil { return expr.SourceCursor{}, err }
		switch n.Slots[i] {
		case 0: slot = value
		case 3:
			if n.Variadic { options = value } else { expanded[at] = value; at++ }
		}
	}
	if len(expanded) != 0 {
		var err error
		options, err = sqltype.OwnedArrayValue(n.EmptyOptions.Type(), []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(expanded))}}, expanded)
		if err != nil { return expr.SourceCursor{}, err }
	}
	runtime, ok := ctx.(LogicalDecodingRuntime)
	if !ok { return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing logical decoding runtime") }
	return expr.SourceCursor{}, runtime.LogicalDecoding(slot, options)
}

// CheckLogicalDecodingOptions applies pure checks after live replication
// authorization. It neither looks up nor manufactures replication slots.
func CheckLogicalDecodingOptions(slot, options sqltype.Value) error {
	if slot.IsNull() { return sqlError("22004", "slot name must not be null") }
	if options.IsNull() { return sqlError("22004", "options array must not be null") }
	if options.ArrayRank() > 1 { return sqlError("0A000", "array must be one-dimensional") }
	for i := range options.ArrayLen() {
		if options.ArrayElement(i).IsNull() { return sqlError("0A000", "array must not contain nulls") }
	}
	if options.ArrayLen()%2 != 0 { return sqlError("0A000", "array must have even number of elements") }
	return nil
}

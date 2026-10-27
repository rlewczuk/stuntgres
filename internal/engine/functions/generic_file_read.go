package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// GenericFileReadKind selects a physical mechanism, never a file or authority.
type GenericFileReadKind uint8

const (
	GenericFileText GenericFileReadKind = iota
	GenericFileBinary
	GenericFileStat
)

var genericFileReadNames = [...]string{"pg_read_file", "pg_read_binary_file", "pg_stat_file"}

func (k GenericFileReadKind) Name() string {
	if int(k) >= len(genericFileReadNames) { return "" }
	return genericFileReadNames[k]
}

// Args and Slots preserve written evaluation order independently of formal order.
type GenericFileRead struct {
	expr.Info
	Kind GenericFileReadKind
	Arity uint8
	Args [4]expr.Expr
	Slots [4]uint8
}

func genericFileReadFormals(kind GenericFileReadKind, arity int) ([4]sqltype.ID, bool) {
	formals := [4]sqltype.ID{sqltype.Text}
	if kind.Name() == "" || arity < 1 || arity > 4 || kind == GenericFileStat && arity > 2 { return formals, false }
	if arity == 2 { formals[1] = sqltype.Bool }
	if arity >= 3 { formals[1], formals[2] = sqltype.Int8, sqltype.Int8 }
	if arity == 4 { formals[3] = sqltype.Bool }
	return formals, true
}

func bindGenericFileRead(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [4]Argument
	var inline [4]string
	names := inline[:min(len(raw.Args), 4)]
	if len(raw.Args) > 4 && len(raw.Args) <= 100 { names = make([]string, len(raw.Args)) }
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil { return nil, err }
		if i < 4 { operands[i] = arg }
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
	kind := GenericFileReadKind(len(genericFileReadNames))
	for i, candidate := range genericFileReadNames { if name == candidate { kind = GenericFileReadKind(i); break } }
	arity := len(raw.Args)
	formals, valid := genericFileReadFormals(kind, arity)
	slots := [4]uint8{0, 1, 2, 3}
	if len(raw.ArgNames) != 0 {
		if kind != GenericFileStat { valid = false } else {
			mapped, ok := namedSlots(raw, []string{"filename", "missing_ok"}[:min(arity, 2)], arity)
			valid = valid && ok
			if ok { for i := range arity { slots[i] = uint8(mapped[i]) } }
		}
	}
	if !visible || !valid || raw.Variadic || raw.ResolutionOnly { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
	for i := range arity {
		if operands[i].Usage.Type != nil {
			if _, failure := sqltype.PlanCoercion(operands[i].Usage, b.Type(formals[slots[i]]).Usage(), sqltype.Implicit); failure != nil { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
		}
	}
	node := GenericFileRead{Kind: kind, Arity: uint8(arity), Slots: slots}
	properties := expr.Properties{Volatility: expr.Volatile}
	for i := range arity {
		target := b.Type(formals[slots[i]]).Usage()
		arg, err := b.CoerceArgument(operands[i], CoercionRequest{Intent: FinalizeArgument, Target: target})
		if err != nil { return nil, err }
		if arg.ResultUsage() != target {
			plan, failure := sqltype.PlanCoercion(arg.ResultUsage(), target, sqltype.Implicit)
			if failure != nil { return nil, failure }
			arg, err = b.CoerceArgument(Argument{Expression: arg, Raw: raw.Args[i], Usage: arg.ResultUsage()}, CoercionRequest{Intent: BareFormalArgument, Target: target, Plan: plan, Planned: true})
			if err != nil { return nil, err }
		}
		node.Args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if raw.Star { return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos) }
	if err := CallDecorationError(raw); err != nil { return nil, err }
	result := b.Type(sqltype.Text)
	if kind == GenericFileBinary { result = b.Type(sqltype.Bytea) }
	if kind == GenericFileStat {
		members := make([]sqltype.CompositeMember, 6)
		for i, label := range []string{"size", "access", "modification", "change", "creation", "isdir"} {
			id := sqltype.Timestamptz
			if i == 0 { id = sqltype.Int8 }; if i == 5 { id = sqltype.Bool }
			members[i] = sqltype.CompositeMember{Attribute: int32(i+1), Name: label, Usage: b.Type(id).Usage()}
		}
		result, err = sqltype.RecordShape(members)
		if err != nil { return nil, err }
	}
	node.Info, err = b.BuiltinRoutineInfo(b.Info(result.Usage(), raw.Pos, properties), CatalogRoutineProvider, name, formals[:arity]...)
	if err != nil { return nil, err }
	return node, nil
}

func (n GenericFileRead) Validate() error {
	formals, ok := genericFileReadFormals(n.Kind, int(n.Arity))
	if !ok || n.Usage.Type == nil { return fmt.Errorf("stuntgres: invalid generic file read signature") }
	var seen [4]bool
	for i := range int(n.Arity) {
		slot := n.Slots[i]
		if n.Kind != GenericFileStat && slot != uint8(i) {
			return fmt.Errorf("stuntgres: invalid positional generic file read operand")
		}
		if slot >= n.Arity || seen[slot] || n.Args[i] == nil || n.Args[i].ResultUsage().Type == nil || n.Args[i].ResultUsage().Type.ID() != formals[slot] { return fmt.Errorf("stuntgres: invalid generic file read operand") }
		seen[slot] = true
	}
	for i := int(n.Arity); i < 4; i++ { if n.Args[i] != nil { return fmt.Errorf("stuntgres: unused generic file read operand") } }
	if n.Kind == GenericFileStat {
		d := n.Usage.Type
		if d.ID() != 2249 || !d.IsComposite() || d.CompositeLen() != 6 { return fmt.Errorf("stuntgres: invalid file stat record") }
		for i, name := range []string{"size", "access", "modification", "change", "creation", "isdir"} {
			id := sqltype.Timestamptz; if i == 0 { id = sqltype.Int8 }; if i == 5 { id = sqltype.Bool }
			m := d.CompositeMember(i)
			if m.Attribute != int32(i+1) || m.Name != name || m.Usage.Type == nil || m.Usage.Type.ID() != id { return fmt.Errorf("stuntgres: invalid file stat member") }
		}
	} else {
		id := sqltype.Text; if n.Kind == GenericFileBinary { id = sqltype.Bytea }
		if n.Usage.Type.ID() != id { return fmt.Errorf("stuntgres: invalid generic file read result") }
	}
	return nil
}

// GenericFileReadRuntime borrows current statement authority without host I/O.
type GenericFileReadRuntime interface { GenericFileRead(GenericFileReadKind, string, int64, bool) error }
type GenericFileTmpdirRuntime interface { GenericFileTmpdir(uint32) error }

func (n GenericFileRead) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil { return sqltype.Value{}, err }
	var values [4]sqltype.Value
	null := false
	for i := range int(n.Arity) {
		value, err := n.Args[i].Eval(ctx)
		if err != nil { return sqltype.Value{}, err }
		values[n.Slots[i]] = value
		null = null || value.IsNull()
	}
	if null { return sqltype.Null(n.Usage.Type), nil }
	filename, err := values[0].Text()
	if err != nil { return sqltype.Value{}, err }
	hasLength := n.Arity >= 3
	var length int64
	if hasLength { length, err = values[2].Integer(); if err != nil { return sqltype.Value{}, err } }
	runtime, ok := ctx.(GenericFileReadRuntime)
	if !ok { return sqltype.Value{}, fmt.Errorf("stuntgres: missing generic file read runtime") }
	return sqltype.Value{}, runtime.GenericFileRead(n.Kind, filename, length, hasLength)
}

func namedGenericFileRead(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text != "pg_stat_file" || len(args) < 1 || len(args) > 2 || !declaredTypes(declared, []sqltype.ID{sqltype.Text, sqltype.Bool}[:len(args)]) { return nil, nil, false }
	return fixedNamedInputs(b, raw, args, declared, []string{"filename", "missing_ok"}[:len(args)], len(args))
}

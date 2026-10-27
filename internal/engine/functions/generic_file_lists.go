package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// GenericFileListKind identifies only the selected physical listing mechanisms.
type GenericFileListKind uint8

const (
	GenericFileDir GenericFileListKind = iota
	GenericFileLog
	GenericFileWAL
	GenericFileLogicalMap
	GenericFileLogicalSnap
	GenericFileReplSlot
	GenericFileSummaries
	GenericFileArchiveStatus
)

var genericFileListNames = [...]string{
	"pg_ls_dir", "pg_ls_logdir", "pg_ls_waldir", "pg_ls_logicalmapdir",
	"pg_ls_logicalsnapdir", "pg_ls_replslotdir", "pg_ls_summariesdir", "pg_ls_archive_statusdir",
}

func (k GenericFileListKind) Name() string {
	if int(k) >= len(genericFileListNames) {
		return ""
	}
	return genericFileListNames[k]
}

// GenericFileList owns its operands, never a session, authority or open cursor.
type GenericFileList struct {
	expr.Info
	Kind  GenericFileListKind
	Arity uint8
	Args  [3]expr.Expr
}

func bindGenericFileList(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [3]Argument
	var inlineNames [3]string
	names := inlineNames[:min(len(raw.Args), 3)]
	if len(raw.Args) > 3 && len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
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
	kind := GenericFileListKind(len(genericFileListNames))
	for i, candidate := range genericFileListNames {
		if candidate == name {
			kind = GenericFileListKind(i)
			break
		}
	}
	arity := len(raw.Args)
	valid := kind.Name() != "" && (kind == GenericFileDir && (arity == 1 || arity == 3) ||
		kind == GenericFileReplSlot && arity == 1 ||
		kind != GenericFileDir && kind != GenericFileReplSlot && arity == 0)
	if len(raw.ArgNames) != 0 {
		valid = valid && kind == GenericFileReplSlot && arity == 1 && raw.ArgNames[0].Text == "slot_name"
	}
	if !visible || !valid || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	var formals [3]sqltype.ID
	for i := range arity {
		formals[i] = sqltype.Text
		if i > 0 {
			formals[i] = sqltype.Bool
		}
	}
	// Reject every impossible formal axis before converting UNKNOWN literals.
	for i := range arity {
		if operands[i].Usage.Type != nil {
			if _, failure := sqltype.PlanCoercion(operands[i].Usage, b.Type(formals[i]).Usage(), sqltype.Implicit); failure != nil {
				return nil, b.SignatureError(raw.Name, raw.Pos, names)
			}
		}
	}
	node := GenericFileList{Kind: kind, Arity: uint8(arity)}
	properties := expr.Properties{Volatility: expr.Volatile}
	for i := range arity {
		target := b.Type(formals[i]).Usage()
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
		node.Args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	if err := b.AdmitSetReturning(raw.Pos); err != nil {
		return nil, err
	}
	result := b.Type(sqltype.Text)
	if kind != GenericFileDir {
		result, err = sqltype.RecordShape([]sqltype.CompositeMember{
			{Attribute: 1, Name: "name", Usage: b.Type(sqltype.Text).Usage()},
			{Attribute: 2, Name: "size", Usage: b.Type(sqltype.Int8).Usage()},
			{Attribute: 3, Name: "modification", Usage: b.Type(sqltype.Timestamptz).Usage()},
		})
		if err != nil {
			return nil, err
		}
	}
	node.Info, err = b.BuiltinRoutineInfo(b.Info(result.Usage(), raw.Pos, properties), CatalogRoutineProvider, name, formals[:arity]...)
	if err != nil {
		return nil, err
	}
	return node, nil
}

func (n GenericFileList) Validate() error {
	if n.Kind.Name() == "" || n.Arity > 3 || n.Usage.Type == nil {
		return fmt.Errorf("stuntgres: invalid generic file listing")
	}
	expected := uint8(0)
	if n.Kind == GenericFileDir {
		expected = 1
		if n.Arity == 3 {
			expected = 3
		}
	}
	if n.Kind == GenericFileReplSlot {
		expected = 1
	}
	if n.Arity != expected {
		return fmt.Errorf("stuntgres: invalid generic file listing arity")
	}
	for i := range int(n.Arity) {
		id := sqltype.Text
		if i > 0 {
			id = sqltype.Bool
		}
		if n.Args[i] == nil || n.Args[i].ResultUsage().Type == nil || n.Args[i].ResultUsage().Type.ID() != id {
			return fmt.Errorf("stuntgres: invalid generic file listing operand")
		}
	}
	for i := int(n.Arity); i < len(n.Args); i++ {
		if n.Args[i] != nil {
			return fmt.Errorf("stuntgres: retained unused generic file listing operand")
		}
	}
	if n.Kind == GenericFileDir {
		if n.Usage.Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid directory listing result")
		}
		return nil
	}
	d := n.Usage.Type
	if !d.IsComposite() || d.ID() != 2249 || d.CompositeLen() != 3 {
		return fmt.Errorf("stuntgres: invalid file listing record")
	}
	names := [3]string{"name", "size", "modification"}
	types := [3]sqltype.ID{sqltype.Text, sqltype.Int8, sqltype.Timestamptz}
	for i := range names {
		m := d.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != names[i] || m.Usage.Type == nil || m.Usage.Type.ID() != types[i] {
			return fmt.Errorf("stuntgres: invalid file listing member")
		}
	}
	return nil
}

func (GenericFileList) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: generic file listing set used as scalar")
}

func (n GenericFileList) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return expr.SourceCursor{}, err
	}
	var values [3]sqltype.Value
	null := false
	for i := range int(n.Arity) {
		value, err := n.Args[i].Eval(ctx)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		values[i] = value
		null = null || value.IsNull()
	}
	if null {
		return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, Exhausted: true}), nil
	}
	dirname := ""
	if n.Arity != 0 {
		var err error
		dirname, err = values[0].Text()
		if err != nil {
			return expr.SourceCursor{}, err
		}
	}
	runtime, ok := ctx.(GenericFileListRuntime)
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing generic file listing runtime")
	}
	return expr.SourceCursor{}, runtime.GenericFileList(n.Kind, dirname)
}

func namedGenericFileList(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text != "pg_ls_replslotdir" || len(args) != 1 || len(raw.ArgNames) != 1 || raw.ArgNames[0].Text != "slot_name" || !declaredTypes(declared, []sqltype.ID{sqltype.Text}) {
		return nil, nil, false
	}
	return namedInputs(b, args, declared, []int{0})
}

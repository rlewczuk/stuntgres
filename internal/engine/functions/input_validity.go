package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// InputValidityRuntime checks only the destination producer's recoverable errors.
// Resolution, setup, and hard producer failures are returned as ordinary errors.
type InputValidityRuntime interface {
	CheckTypeInput(value, typeName string) (*sqlstate.Error, error)
}

type InputValidity struct {
	expr.Info
	Args      [2]expr.Expr // written order; Slots maps written operands to formals
	Slots     [2]int
	Labels    [2]string
	ErrorInfo bool
}

func namedInputValidity(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text != "pg_input_error_info" || raw.Variadic || !declaredTypes(declared, []sqltype.ID{sqltype.Text, sqltype.Text}) {
		return nil, nil, false
	}
	return fixedNamedInputs(b, raw, args, declared, []string{"value", "type_name"}, 2)
}

func bindInputValidity(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands [2]Argument
	var small [2]string
	names := small[:0]
	if len(raw.Args) <= 2 {
		names = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
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
	slots, valid := namedSlots(raw, []string{"value", "type_name"}, 2)
	if raw.Name.Parts[len(raw.Name.Parts)-1].Text == "pg_input_is_valid" && len(raw.ArgNames) != 0 {
		valid = false
	}
	if !visible || !valid || len(raw.Args) != 2 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	n := InputValidity{Slots: [2]int{slots[0], slots[1]}, ErrorInfo: name == "pg_input_error_info"}
	properties := expr.Properties{Volatility: expr.Stable}
	for i := range n.Args {
		n.Args[i], err = coerceInquiryFormal(b, operands[i], raw.Args[i], b.Type(sqltype.Text).Usage(), func() error { return b.SignatureError(raw.Name, raw.Pos, names) })
		if err != nil {
			return nil, err
		}
		if len(raw.ArgNames) > i {
			n.Labels[i] = raw.ArgNames[i].Text
		}
	}
	properties = expr.MergeProperties(n.Args[0].Properties(), n.Args[1].Properties())
	properties.Volatility = max(properties.Volatility, expr.Stable)
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	usage := b.Type(sqltype.Bool).Usage()
	if n.ErrorInfo {
		members := make([]sqltype.CompositeMember, 4)
		for i, label := range []string{"message", "detail", "hint", "sql_error_code"} {
			members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: label, Usage: b.Type(sqltype.Text).Usage()}
		}
		shape, err := sqltype.RecordShape(members)
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
	}
	n.Info, err = b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, properties), CatalogRoutineProvider, name, sqltype.Text, sqltype.Text)
	if err != nil {
		return nil, err
	}
	return n, n.Validate()
}

func (n InputValidity) Validate() error {
	if n.Slots[0] < 0 || n.Slots[0] > 1 || n.Slots[1] != 1-n.Slots[0] {
		return fmt.Errorf("stuntgres: invalid input-validity slots")
	}
	for i, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid input-validity operand")
		}
		if n.Labels[i] != "" && (!n.ErrorInfo || n.Labels[i] != [2]string{"value", "type_name"}[n.Slots[i]]) {
			return fmt.Errorf("stuntgres: invalid input-validity label")
		}
	}
	d := n.Usage.Type
	if d == nil {
		return fmt.Errorf("stuntgres: missing input-validity result")
	}
	if !n.ErrorInfo {
		if d.ID() != sqltype.Bool {
			return fmt.Errorf("stuntgres: invalid input-validity boolean")
		}
		return nil
	}
	if d.ID() != 2249 || !d.IsComposite() || d.CompositeLen() != 4 {
		return fmt.Errorf("stuntgres: invalid input-validity record")
	}
	for i, label := range []string{"message", "detail", "hint", "sql_error_code"} {
		m := d.CompositeMember(i)
		if m.Attribute != int32(i+1) || m.Name != label || m.Usage.Type == nil || m.Usage.Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid input-validity member")
		}
	}
	return nil
}

func (n InputValidity) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	// Named notation retains written children for capture/rendering, but normal
	// scalar evaluation follows formal order, as for other named builtin calls.
	for slot := range values {
		source := slot
		if n.Slots[0] == 1 {
			source = 1 - slot
		}
		v, err := n.Args[source].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[slot] = v
	}
	if values[0].IsNull() || values[1].IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	value, err := values[0].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	target, err := values[1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, ok := c.(InputValidityRuntime)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing type-input authority")
	}
	diagnostic, err := runtime.CheckTypeInput(value, target)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !n.ErrorInfo {
		return sqltype.Boolean(n.Usage.Type, diagnostic == nil), nil
	}
	fields := make([]sqltype.Value, 4)
	var text [4]string
	if diagnostic != nil {
		text = [4]string{diagnostic.Message, diagnostic.Detail, diagnostic.Hint, diagnostic.Code}
	}
	for i := range fields {
		d := n.Usage.Type.CompositeMember(i).Usage.Type
		fields[i] = sqltype.Null(d)
		if diagnostic != nil && (i == 0 || i == 3 || text[i] != "") {
			fields[i] = sqltype.String(d, text[i])
		}
	}
	return sqltype.OwnedCompositeValue(n.Usage.Type, fields)
}

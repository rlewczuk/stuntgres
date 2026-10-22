package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// jsonCallNames lists every JSON/JSONB processing call jsonCallClaims admits.
var jsonCallNames = [...]string{
	"to_json", "to_jsonb", "array_to_json", "row_to_json",
	"json_build_array", "jsonb_build_array", "json_build_object", "jsonb_build_object",
	"jsonb_path_query_array", "jsonb_path_exists", "jsonb_path_match", "jsonb_path_query_first", "jsonb_path_query",
	"jsonb_set", "jsonb_set_lax", "jsonb_insert", "json_strip_nulls",
	"json_extract_path", "json_extract_path_text", "jsonb_extract_path", "jsonb_extract_path_text",
	"jsonb_populate_record", "jsonb_populate_recordset", "jsonb_to_record", "jsonb_to_recordset", "jsonb_populate_record_valid",
	"json_typeof", "jsonb_typeof", "jsonb_pretty", "json_array_length", "jsonb_array_length",
	"json_object_keys", "jsonb_object_keys", "json_each", "json_each_text", "jsonb_each", "jsonb_each_text",
	"json_array_elements", "json_array_elements_text", "jsonb_array_elements", "jsonb_array_elements_text",
	"json_populate_record", "json_populate_recordset", "json_to_record", "json_to_recordset",
}

// jsonCallClaims is the union of the engine chain's JSON predicates, each
// with its own comparison: JSON creation and JSONB record names are
// lowercased, the processing names case-folded.
func jsonCallClaims(name string) bool {
	return JSONCreateKind(name) != 0 || strings.EqualFold(name, "jsonb_path_query_array") ||
		parser.JSONBPathQueryFunctionName(name) || parser.JSONBSetFunctionName(name) || parser.JSONFunctionName(name)
}

// bindJSONCall replays the engine chain's JSON branch order. JSON creation
// checks builtin visibility after analyzing its arguments; every other
// branch checks it first. Each binder owns exact-spelling resolution.
func bindJSONCall(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if JSONCreateKind(name) != 0 {
		return bindJSONCreate(b, raw)
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.EqualFold(name, "jsonb_path_query_array"):
		return bindJSONBPathQueryArray(b, raw, visible)
	case parser.JSONBPathQueryFunctionName(name):
		return bindJSONBPathQuery(b, raw, visible)
	case parser.JSONBSetFunctionName(name):
		return bindJSONBSet(b, raw, visible)
	case strings.EqualFold(name, "json_strip_nulls"):
		return bindJSONStripNulls(b, raw, visible)
	case name == "json_extract_path" || name == "json_extract_path_text" || name == "jsonb_extract_path" || name == "jsonb_extract_path_text":
		return bindJSONExtract(b, raw, visible)
	case parser.JSONBRecordFunctionName(name):
		return bindJSONBRecord(b, raw, visible)
	}
	return bindJSONFunction(b, raw, visible)
}

// textObjectNames sat in the engine's final name switch, after the shared
// call gates (SharedGates); builtin visibility follows them.
var textObjectNames = [...]string{"json_object", "jsonb_object"}

func bindTextObject(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	return bindTextObjectFunction(b, raw, raw.Name.Parts[len(raw.Name.Parts)-1].Text, visible)
}

// typeNameOrUnknown spells an analyzed argument type for a missing-signature
// diagnostic; deferred literals are unknown.
func typeNameOrUnknown(d *sqltype.Descriptor) string {
	if d != nil {
		return d.DiagnosticName()
	}
	return "unknown"
}

func bindJSONCreate(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	kind := JSONCreateKind(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	var operands []Argument
	var small [2]Argument
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, r := range raw.Args {
		o, err := b.AnalyzeArgument(r, AnalysisRequest{Intent: StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return nil, fmt.Errorf("invalid JSON function argument labels")
	}
	switch failure := AnalyzeLabels(raw.ArgNames, raw.Args); failure.Kind {
	case PositionalAfterNamed:
		return nil, expr.ErrorAt("42601", "positional argument cannot follow named argument", failure.Pos)
	case DuplicateLabel:
		return nil, expr.ErrorAt("42601", fmt.Sprintf("argument name %q used more than once", failure.Name), failure.Pos)
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	var boolean sqltype.Usage
	if !kind.Builder() && len(operands) == 2 && operands[1].Usage.Type != nil {
		boolean = b.Type(sqltype.Bool).Usage()
	}
	if ResolveJSONCreate(kind, raw.Name.Parts[len(raw.Name.Parts)-1].Text, visible, raw.Variadic, raw.ResolutionOnly, len(raw.ArgNames), operands, boolean) != Resolved {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = "unknown"
			if d := o.Usage.Type; d != nil {
				names[i] = b.DiagnosticTypeName(d)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	name := kind.Name()
	switch {
	case raw.Star:
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return nil, expr.ErrorAt("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expr.ErrorAt("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expr.ErrorAt("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Window != nil:
		return nil, expr.ErrorAt("42809", "OVER specified, but "+name+" is not a window function nor an aggregate function", raw.Pos)
	}
	args := make([]expr.Expr, len(operands))
	var unknown [2]uint64
	properties := expr.IntrinsicProperties(JSONCreateIntrinsicRule())
	for i, o := range operands {
		arg := o.Expression
		rawUnknown := JSONCreateRawUnknown(o.Raw)
		if rawUnknown {
			unknown[i/64] |= uint64(1) << uint(i%64)
		}
		switch JSONCreateRule(kind, i, o.Usage, rawUnknown) {
		case JSONUnknownPolymorphic:
			return nil, expr.ErrorAt("42804", "could not determine polymorphic type because input has type unknown", parser.Position{})
		case JSONRecordFormal:
			// The fixed RECORD formal supplies an occurrence-owned transport identity,
			// not a promotion of catalog RECORD or an anonymous composite text codec.
			arg, err = b.CoerceArgument(o, CoercionRequest{Intent: RecordFormalArgument})
		case JSONBooleanFormal:
			arg, err = b.CoerceArgument(o, CoercionRequest{Intent: LikeArgument, Target: b.Type(sqltype.Bool).Usage()})
		case JSONTextUnknown:
			arg, err = b.CoerceArgument(o, CoercionRequest{Intent: FinalizeArgument, Target: b.Type(sqltype.Text).Usage()})
		}
		if err != nil {
			return nil, err
		}
		if arg == nil {
			return nil, fmt.Errorf("missing JSON function operand")
		}
		args[i] = arg
		b.RetainUntypedParameter(arg)
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
		if arg.ResultUsage().Type != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
			b.NoteCatalogText()
		}
	}
	if kind.Builder() && raw.Variadic && !JSONCreateVariadicArgumentValid(args[0].ResultUsage()) {
		return nil, expr.ErrorAt("42804", "VARIADIC argument must be an array", raw.Args[0].SourcePosition())
	}
	info := b.Info(b.Type(JSONCreateResult(kind)).Usage(), raw.Pos, properties)
	node, err := NewJSONCreate(info, kind, args, raw.Variadic, unknown)
	if err != nil {
		return nil, err
	}
	var formal [2]sqltype.ID
	count := len(args)
	switch name {
	case "to_json", "to_jsonb":
		formal[0] = sqltype.ID(2283)
	case "array_to_json":
		formal = [2]sqltype.ID{sqltype.ID(2277), sqltype.Bool}
	case "row_to_json":
		formal = [2]sqltype.ID{sqltype.ID(2249), sqltype.Bool}
	default:
		count = 0
		if raw.Variadic || len(args) != 0 {
			formal[0], count = sqltype.ID(2276), 1
		}
	}
	info, err = b.BuiltinRoutineInfo(info, JSONRoutineProvider, name, formal[:count]...)
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}

func bindJSONFunction(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if jsonRecordFunction(name) {
		return bindJSONRecord(b, raw, visible)
	}
	var operand Argument
	var singleName [1]string
	var names []string
	if len(raw.Args) <= 1 {
		names = singleName[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, r := range raw.Args {
		o, err := b.AnalyzeArgument(r, AnalysisRequest{Intent: StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if names != nil {
			names[i] = "unknown"
			if d := o.Usage.Type; d != nil {
				names[i] = b.DiagnosticTypeName(d)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
		if i == 0 {
			operand = o
		}
	}
	if name == "json_object_keys" || name == "json_each" || name == "json_each_text" || name == "jsonb_each" || name == "jsonb_each_text" || name == "json_array_elements" || name == "json_array_elements_text" || name == "jsonb_array_elements" || name == "jsonb_array_elements_text" {
		if err := b.AnalyzeFilter(raw.Filter); err != nil {
			return nil, err
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	missing := func() (expr.Expr, error) { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
	namedCount := jsonNamedCount(raw)
	definition, resolution := JSONFunctionCall(name, visible, len(raw.Args), namedCount)
	if resolution != Resolved {
		return missing()
	}
	input := b.Type(definition.Input)
	if ResolveJSONArgument(operand.Usage, input.Usage()) != Resolved {
		return missing()
	}
	arg, err := b.CoerceArgument(operand, CoercionRequest{Intent: definition.ArgumentIntent, Target: input.Usage()})
	if err != nil {
		return nil, err
	}
	decoration, message := "", "not an aggregate function"
	switch {
	case raw.Distinct:
		decoration = "DISTINCT"
	case len(raw.Order) != 0:
		decoration = "ORDER BY"
	case raw.Filter != nil:
		decoration = "FILTER"
	case raw.Window != nil:
		decoration, message = "OVER", "not a window function nor an aggregate function"
	}
	if decoration != "" {
		diagnosticName := name
		if name == "json_object_keys" || name == "jsonb_pretty" || name == "jsonb_each" || name == "jsonb_each_text" || definition.Elements {
			diagnosticName = qualifiedName(raw.Name)
		}
		return nil, expr.ErrorAt("42809", decoration+" specified, but "+diagnosticName+" is "+message, raw.Pos)
	}
	properties := arg.Properties()
	usage := b.Type(definition.Result).Usage()
	if definition.Each {
		value := input.Usage()
		if name == "json_each_text" || name == "jsonb_each_text" {
			value = b.Type(sqltype.Text).Usage()
		}
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{
			{Attribute: 1, Name: "key", Usage: b.Type(sqltype.Text).Usage()},
			{Attribute: 2, Name: "value", Usage: value},
		})
		if err != nil {
			return nil, err
		}
		usage = shape.Usage()
	}
	info, err := b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, properties), JSONRoutineProvider, name, definition.Input)
	if err != nil {
		return nil, err
	}
	if definition.Keys || definition.Each || definition.Elements {
		if err := b.AdmitSetReturning(raw.Pos); err != nil {
			return nil, err
		}
		properties.StageDependent = true
		info.Property = properties
		if definition.Elements {
			if definition.Input == sqltype.JSONB {
				return NewJSONBElements(info, name, arg)
			}
			return NewJSONElements(info, name, arg)
		}
		if definition.Each {
			return NewJSONEach(info, name, arg)
		}
		return NewJSONKeys(info, name, arg)
	}
	return NewBinary(info, name, []expr.Expr{arg}, parser.OrdinaryFunctionSyntax)
}

// Explicit VARIADIC already carries its final array as one argument. These
// nonvariadic signatures therefore use exactly the ordinary argument path.
func bindTextObjectFunction(b Binder, raw parser.FunctionExpr, name string, visible bool) (expr.Expr, error) {
	var small [2]Argument
	var smallNames [2]string
	var operands []Argument
	var names []string
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
		names = smallNames[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, r := range raw.Args {
		o, err := b.AnalyzeArgument(r, AnalysisRequest{Intent: StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if names != nil {
			names[i] = "unknown"
			if d := o.Usage.Type; d != nil {
				names[i] = d.DiagnosticName()
			}
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	missing := func() (expr.Expr, error) { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
	definition, resolution := ResolveTextObject(name, visible, len(raw.Args))
	if resolution != Resolved {
		return missing()
	}
	target := b.Type(definition.Argument).Usage()
	if !TextObjectArgumentsViable(operands, target) {
		return missing()
	}
	args, err := CoerceTextObjectArguments(b, operands, target, definition.Intent)
	if err != nil {
		return nil, err
	}
	properties := expr.Properties{Volatility: expr.Immutable}
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	usage := b.Type(definition.Result).Usage()
	formal := [2]sqltype.ID{definition.Argument, definition.Argument}
	info, err := b.BuiltinRoutineInfo(b.Info(usage, raw.Pos, properties), JSONRoutineProvider, name, formal[:len(args)]...)
	if err != nil {
		return nil, err
	}
	return NewBinary(info, name, args, parser.OrdinaryFunctionSyntax)
}

func jsonNamedCount(raw parser.FunctionExpr) int {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if len(raw.ArgNames) == 1 && raw.ArgNames[0].Text == "from_json" {
		switch name {
		case "json_each", "json_each_text", "jsonb_each", "jsonb_each_text", "json_array_elements", "json_array_elements_text", "jsonb_array_elements", "jsonb_array_elements_text":
			return 0
		}
	}
	return len(raw.ArgNames)
}

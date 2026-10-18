package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// slotSpec describes a JSON call whose written arguments, positional or
// named, fill fixed formal slots.
type slotSpec struct {
	labels   []string     // formal argument names, one per slot
	formals  []sqltype.ID // formal types, one per slot
	required int          // leading slots every call must write
	// ordinalVariadic: PostgreSQL accepts redundant VARIADIC on these
	// nonvariadic routines, but named notation must leave the final written
	// argument at its own ordinal.
	ordinalVariadic bool
	volatility      expr.Volatility
}

// slotCall is an analyzed slot call: coerced arguments by slot and the
// written order, labels and slots for stored presentation.
type slotCall struct {
	args       [5]expr.Expr
	count      int
	slots      [5]uint8
	labels     [5]string
	supplied   [5]bool
	properties expr.Properties
}

// bindSlots follows the shared diagnostic order: argument analysis, the
// 100-argument limit, label validation, then, for an admitted call within
// the slot count, slot assignment, implicit-coercion viability, required
// slots, formal coercion and finally the scalar decoration check. Every
// resolution failure is the missing-signature error over the analyzed types.
func bindSlots(b Binder, raw parser.FunctionExpr, admitted bool, spec slotSpec) (slotCall, error) {
	var call slotCall
	var small [5]Argument
	operands := small[:min(len(raw.Args), len(spec.labels))]
	if len(raw.Args) > len(spec.labels) && len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return call, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return call, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() (slotCall, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = NamedArgumentType(raw, i, typeNameOrUnknown(o.Usage.Type))
		}
		return call, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return call, fmt.Errorf("invalid function argument labels")
	}
	failure := AnalyzeLabels(raw.ArgNames, raw.Args)
	switch failure.Kind {
	case PositionalAfterNamed:
		return call, expr.ErrorAt("42601", "positional argument cannot follow named argument", failure.Pos)
	case DuplicateLabel:
		return call, expr.ErrorAt("42601", fmt.Sprintf("argument name %q used more than once", failure.Name), failure.Pos)
	}
	count := len(spec.labels)
	if !admitted || len(operands) > count {
		return signature()
	}
	call.count = len(operands)
	for i, o := range operands {
		slot := i
		if len(raw.ArgNames) > 0 && raw.ArgNames[i].Text != "" {
			slot = -1
			for j, label := range spec.labels {
				if label == raw.ArgNames[i].Text {
					slot = j
					break
				}
			}
			call.labels[i] = raw.ArgNames[i].Text
		}
		if slot < 0 || slot >= count || call.supplied[slot] {
			return signature()
		}
		call.supplied[slot] = true
		call.slots[i] = uint8(slot)
		if o.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(o.Usage, b.Type(spec.formals[slot]).Usage(), sqltype.Implicit); err != nil {
				return signature()
			}
		}
	}
	for slot := range spec.required {
		if !call.supplied[slot] {
			return signature()
		}
	}
	if spec.ordinalVariadic && raw.Variadic && len(raw.ArgNames) != 0 && len(operands) != 0 &&
		int(call.slots[len(operands)-1]) != len(operands)-1 {
		return signature()
	}
	call.properties = expr.Properties{Volatility: spec.volatility}
	for i, o := range operands {
		slot := call.slots[i]
		arg, err := b.CoerceArgument(o, CoercionRequest{Intent: ExactFormalArgument, Target: b.Type(spec.formals[slot]).Usage()})
		if err != nil {
			return call, err
		}
		call.args[slot] = arg
		call.properties = expr.MergeProperties(call.properties, arg.Properties())
	}
	// Resolve and coerce ordinary arguments before rejecting scalar
	// decorations. Never silently discard AST aggregate/window modifiers
	// transported by the shared function grammar.
	return call, CallDecorationError(raw)
}

func bindJSONBSet(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	count := 4
	if name == "jsonb_set_lax" {
		count = 5
	}
	labels := [5]string{"jsonb_in", "path", "replacement", "create_if_missing", "null_value_treatment"}
	if name == "jsonb_insert" {
		labels[3] = "insert_after"
	}
	ids := [5]sqltype.ID{sqltype.JSONB, 1009, sqltype.JSONB, sqltype.Bool, sqltype.Text}
	admitted := visible && !raw.ResolutionOnly && (name == "jsonb_set" || name == "jsonb_set_lax" || name == "jsonb_insert")
	call, err := bindSlots(b, raw, admitted, slotSpec{labels: labels[:count], formals: ids[:count], required: 3, volatility: expr.Immutable})
	if err != nil {
		return nil, err
	}
	e := JSONBSet{Count: count, Args: call.args, WrittenCount: call.count, WrittenSlots: call.slots, Labels: call.labels, Variadic: raw.Variadic}
	if !call.supplied[3] {
		typ := b.Type(sqltype.Bool)
		e.Args[3] = b.Literal(sqltype.Boolean(typ, name != "jsonb_insert"), raw.Pos)
	}
	if count == 5 && !call.supplied[4] {
		typ := b.Type(sqltype.Text)
		e.Args[4] = b.Literal(sqltype.String(typ, "use_json_null"), raw.Pos)
	}
	e.Info = b.Info(b.Type(sqltype.JSONB).Usage(), raw.Pos, call.properties)
	var usages [5]sqltype.Usage
	for i := range count {
		usages[i] = e.Args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBSet(name, e.Usage, usages[:count]...)
	if err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(e.Usage, raw.Pos, call.properties), JSONRoutineProvider, name, ids[:count]...)
	if err != nil {
		return nil, err
	}
	e.Info = info
	e.Plan = plan
	return e, nil
}

// The JSONPath calls share one slot layout: target, path, vars, silent.
var (
	jsonPathLabels  = [4]string{"target", "path", "vars", "silent"}
	jsonPathFormals = [4]sqltype.ID{sqltype.JSONB, sqltype.JSONPath, sqltype.JSONB, sqltype.Bool}
)

// bindJSONPathSlots binds a JSONPath call and supplies its omitted vars
// ('{}'::jsonb) and silent (false) defaults.
func bindJSONPathSlots(b Binder, raw parser.FunctionExpr, admitted bool) ([4]expr.Expr, slotCall, error) {
	var args [4]expr.Expr
	call, err := bindSlots(b, raw, admitted, slotSpec{labels: jsonPathLabels[:], formals: jsonPathFormals[:], required: 2, ordinalVariadic: true, volatility: expr.Immutable})
	if err != nil {
		return args, call, err
	}
	copy(args[:], call.args[:])
	if !call.supplied[2] {
		typ := b.Type(sqltype.JSONB)
		value, err := typ.Input(b.Context(), "{}", -1)
		if err != nil {
			return args, call, err
		}
		args[2] = b.Literal(value, raw.Pos)
	}
	if !call.supplied[3] {
		typ := b.Type(sqltype.Bool)
		args[3] = b.Literal(sqltype.Boolean(typ, false), raw.Pos)
	}
	return args, call, nil
}

func bindJSONBPathQuery(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	args, call, err := bindJSONPathSlots(b, raw, visible && !raw.ResolutionOnly && JSONBPathQueryName(name))
	if err != nil {
		return nil, err
	}
	var slots [4]uint8
	var labels [4]string
	copy(slots[:], call.slots[:])
	copy(labels[:], call.labels[:])
	properties := call.properties
	if name == "jsonb_path_query" {
		if err := b.AdmitSetReturning(raw.Pos); err != nil {
			return nil, err
		}
		properties.StageDependent = true
	}
	resultID := sqltype.JSONB
	if name == "jsonb_path_exists" || name == "jsonb_path_match" {
		resultID = sqltype.Bool
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(resultID).Usage(), raw.Pos, properties), JSONRoutineProvider, name, jsonPathFormals[:]...)
	if err != nil {
		return nil, err
	}
	var usages [4]sqltype.Usage
	for i := range 4 {
		usages[i] = args[i].ResultUsage()
	}
	switch name {
	case "jsonb_path_exists":
		plan, err := sqltype.BindJSONBPathExists(b.Type(resultID).Usage(), usages[:]...)
		if err != nil {
			return nil, err
		}
		return JSONBPathExists{Info: info, Plan: plan, Args: args, WrittenCount: call.count, WrittenSlots: slots, Labels: labels, Variadic: raw.Variadic}, nil
	case "jsonb_path_match":
		plan, err := sqltype.BindJSONBPathMatch(b.Type(resultID).Usage(), usages[:]...)
		if err != nil {
			return nil, err
		}
		return JSONBPathMatch{Info: info, Plan: plan, Args: args, WrittenCount: call.count, WrittenSlots: slots, Labels: labels, Variadic: raw.Variadic}, nil
	case "jsonb_path_query_first":
		plan, err := sqltype.BindJSONBPathQueryFirst(b.Type(resultID).Usage(), usages[:]...)
		if err != nil {
			return nil, err
		}
		return JSONBPathQueryFirst{Info: info, Plan: plan, Args: args, WrittenCount: call.count, WrittenSlots: slots, Labels: labels, Variadic: raw.Variadic}, nil
	case "jsonb_path_query":
		plan, err := sqltype.BindJSONBPathQuery(b.Type(resultID).Usage(), usages[:]...)
		if err != nil {
			return nil, err
		}
		return JSONBPathQuery{Info: info, Plan: plan, Args: args, WrittenCount: call.count, WrittenSlots: slots, Labels: labels, Variadic: raw.Variadic}, nil
	}
	return nil, fmt.Errorf("invalid JSONPath query provider")
}

// JSONBPathQueryName reports the exact names of the four JSONPath query calls
// that share one slot layout.
func JSONBPathQueryName(name string) bool {
	switch name {
	case "jsonb_path_exists", "jsonb_path_match", "jsonb_path_query_first", "jsonb_path_query":
		return true
	}
	return false
}

func bindJSONBPathQueryArray(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	args, call, err := bindJSONPathSlots(b, raw, visible && !raw.ResolutionOnly && name == "jsonb_path_query_array")
	if err != nil {
		return nil, err
	}
	e := JSONBPathQueryArray{Args: args, WrittenCount: call.count, Variadic: raw.Variadic}
	copy(e.WrittenSlots[:], call.slots[:])
	copy(e.Labels[:], call.labels[:])
	e.Info = b.Info(b.Type(sqltype.JSONB).Usage(), raw.Pos, call.properties)
	var usages [4]sqltype.Usage
	for i := range 4 {
		usages[i] = e.Args[i].ResultUsage()
	}
	plan, err := sqltype.BindJSONBPathQueryArray(e.Usage, usages[:]...)
	if err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(e.Usage, raw.Pos, call.properties), JSONRoutineProvider, name, jsonPathFormals[:]...)
	if err != nil {
		return nil, err
	}
	e.Info = info
	e.Plan = plan
	return e, nil
}

func bindJSONStripNulls(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	labels := [2]string{"target", "strip_in_arrays"}
	ids := [2]sqltype.ID{sqltype.JSON, sqltype.Bool}
	call, err := bindSlots(b, raw, visible && !raw.ResolutionOnly && name == "json_strip_nulls", slotSpec{labels: labels[:], formals: ids[:], required: 1, ordinalVariadic: true, volatility: expr.Immutable})
	if err != nil {
		return nil, err
	}
	e := JSONStripNulls{WrittenCount: call.count, Variadic: raw.Variadic}
	copy(e.Args[:], call.args[:])
	copy(e.WrittenSlots[:], call.slots[:])
	copy(e.Labels[:], call.labels[:])
	if !call.supplied[1] {
		typ := b.Type(sqltype.Bool)
		e.Args[1] = b.Literal(sqltype.Boolean(typ, false), raw.Pos)
	}
	e.Info = b.Info(b.Type(sqltype.JSON).Usage(), raw.Pos, call.properties)
	plan, err := sqltype.BindJSONStripNulls(e.Usage, e.Args[0].ResultUsage(), e.Args[1].ResultUsage())
	if err != nil {
		return nil, err
	}
	e.Plan = plan
	return e, nil
}

func bindJSONExtract(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	document := b.Type(sqltype.JSON).Usage()
	if name == "jsonb_extract_path" || name == "jsonb_extract_path_text" {
		document = b.Type(sqltype.JSONB).Usage()
	}
	var local [16]Argument
	operands := local[:min(len(raw.Args), len(local))]
	if len(raw.Args) > len(local) && len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() (expr.Expr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = NamedArgumentType(raw, i, typeNameOrUnknown(o.Usage.Type))
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return nil, fmt.Errorf("invalid JSON extraction labels")
	}
	failure := AnalyzeLabels(raw.ArgNames, raw.Args)
	switch failure.Kind {
	case PositionalAfterNamed:
		return nil, expr.ErrorAt("42601", "positional argument cannot follow named argument", failure.Pos)
	case DuplicateLabel:
		return nil, expr.ErrorAt("42601", fmt.Sprintf("argument name %q used more than once", failure.Name), failure.Pos)
	}
	if !visible || raw.ResolutionOnly || len(operands) < 2 || raw.Variadic && len(operands) != 2 {
		return signature()
	}
	path := b.Type(sqltype.Text).Usage()
	if raw.Variadic {
		a, ok := b.ArrayType(b.Type(sqltype.Text))
		if !ok {
			return nil, fmt.Errorf("missing text array")
		}
		path = a.Usage()
	}
	for i, o := range operands {
		if len(raw.ArgNames) > 0 && raw.ArgNames[i].Text != "" {
			label := "from_json"
			if i > 0 {
				label = "path_elems"
			}
			if !raw.Variadic || raw.ArgNames[i].Text != label {
				return signature()
			}
		}
		target := path
		if i == 0 {
			target = document
		}
		if o.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(o.Usage, target, sqltype.Implicit); err != nil {
				return signature()
			}
		}
	}
	e := JSONExtract{Args: make([]expr.Expr, len(operands)), Variadic: raw.Variadic}
	if len(raw.ArgNames) > 0 {
		e.Labels = make([]string, len(operands))
		for i, n := range raw.ArgNames {
			e.Labels[i] = n.Text
		}
	}
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, o := range operands {
		target := path
		if i == 0 {
			target = document
		}
		arg, err := b.CoerceArgument(o, CoercionRequest{Intent: ExactFormalArgument, Target: target})
		if err != nil {
			return nil, err
		}
		e.Args[i] = arg
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	result := document
	if name == "json_extract_path_text" || name == "jsonb_extract_path_text" {
		result = b.Type(sqltype.Text).Usage()
	}
	info := b.Info(result, raw.Pos, properties)
	plan, err := sqltype.BindJSONExtract(name, result, e.Args[0].ResultUsage(), path, raw.Variadic)
	if err != nil {
		return nil, err
	}
	// The full released path formal is TEXT[], not expanded TEXT children.
	info, err = b.BuiltinRoutineInfo(info, JSONRoutineProvider, name, document.Type.ID(), sqltype.ID(1009))
	if err != nil {
		return nil, err
	}
	e.Info = info
	e.Plan = plan
	return e, nil
}

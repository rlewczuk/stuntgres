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
	slots, supplied, valid := jsonSlotMapping(raw, spec)
	if !valid {
		return signature()
	}
	call.supplied = supplied
	for i, o := range operands {
		slot := slots[i]
		call.slots[i] = uint8(slot)
		if len(raw.ArgNames) > i {
			call.labels[i] = raw.ArgNames[i].Text
		}
		if o.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(o.Usage, b.Type(spec.formals[slot]).Usage(), sqltype.Implicit); err != nil {
				return signature()
			}
		}
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
	spec, _ := jsonSlotSpecification(name)
	count := len(spec.labels)
	admitted := visible && !raw.ResolutionOnly && (name == "jsonb_set" || name == "jsonb_set_lax" || name == "jsonb_insert")
	call, err := bindSlots(b, raw, admitted, spec)
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
	info, err := b.BuiltinRoutineInfo(b.Info(e.Usage, raw.Pos, call.properties), JSONRoutineProvider, name, spec.formals...)
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
	spec, _ := jsonSlotSpecification("jsonb_path_query")
	call, err := bindSlots(b, raw, admitted, spec)
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
	spec, _ := jsonSlotSpecification(name)
	call, err := bindSlots(b, raw, visible && !raw.ResolutionOnly && name == "json_strip_nulls", spec)
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
	e.Info, err = b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.JSON).Usage(), raw.Pos, call.properties), JSONRoutineProvider, name, sqltype.JSON, sqltype.Bool)
	if err != nil {
		return nil, err
	}
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
	if !jsonExtractLabels(raw) {
		return signature()
	}
	for i, o := range operands {
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

var jsonSetLabels = [5]string{"jsonb_in", "path", "replacement", "create_if_missing", "null_value_treatment"}
var jsonInsertLabels = [4]string{"jsonb_in", "path", "replacement", "insert_after"}
var jsonSetFormals = [5]sqltype.ID{sqltype.JSONB, 1009, sqltype.JSONB, sqltype.Bool, sqltype.Text}
var jsonStripLabels = [2]string{"target", "strip_in_arrays"}
var jsonStripFormals = [2]sqltype.ID{sqltype.JSON, sqltype.Bool}

func jsonSlotSpecification(name string) (slotSpec, bool) {
	spec := slotSpec{volatility: expr.Immutable}
	switch name {
	case "jsonb_set":
		spec.labels, spec.formals, spec.required = jsonSetLabels[:4], jsonSetFormals[:4], 3
	case "jsonb_set_lax":
		spec.labels, spec.formals, spec.required = jsonSetLabels[:], jsonSetFormals[:], 3
	case "jsonb_insert":
		spec.labels, spec.formals, spec.required = jsonInsertLabels[:], jsonSetFormals[:4], 3
	case "json_strip_nulls":
		spec.labels, spec.formals, spec.required, spec.ordinalVariadic = jsonStripLabels[:], jsonStripFormals[:], 1, true
	case "jsonb_path_exists", "jsonb_path_match", "jsonb_path_query_array", "jsonb_path_query_first", "jsonb_path_query":
		spec.labels, spec.formals, spec.required, spec.ordinalVariadic = jsonPathLabels[:], jsonPathFormals[:], 2, true
	default:
		return spec, false
	}
	return spec, true
}

func jsonSlotMapping(raw parser.FunctionExpr, spec slotSpec) ([7]int, [5]bool, bool) {
	slots, valid := namedSlots(raw, spec.labels, spec.required)
	var supplied [5]bool
	if !valid {
		return slots, supplied, false
	}
	if spec.ordinalVariadic && raw.Variadic && len(raw.ArgNames) > 0 && len(raw.Args) > 0 && slots[len(raw.Args)-1] != len(raw.Args)-1 {
		return slots, supplied, false
	}
	for i := range raw.Args {
		supplied[slots[i]] = true
	}
	return slots, supplied, true
}

// StoredProviderCallShape checks the written syntax of families whose existing
// binder contract is richer than catalog arity (named slots or redundant
// VARIADIC). The caller must independently prove the captured provider identity.
func StoredProviderCallShape(symbol string, raw parser.FunctionExpr) (known, valid bool) {
	if raw.Star || raw.Within || len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) || CallLabelError(raw) != nil {
		return true, false
	}
	if spec, ok := jsonSlotSpecification(symbol); ok {
		_, _, valid := jsonSlotMapping(raw, spec)
		return true, valid && CallDecorationError(raw) == nil
	}
	if member, ok := UUIDOsspLookupSelector(symbol); ok {
		count := 0
		if member.Signature != "" {
			count = 2
		}
		_, valid := namedSlots(raw, member.ArgumentNames[:count], count)
		return true, valid && !raw.Variadic && CallDecorationError(raw) == nil
	}
	// JSONFunctionCall owns the legacy processing families' label and arity
	// contract, including from_json and redundant VARIADIC single operands.
	_, family := JSONFunctionCall(symbol, true, 1, 0)
	if family == Resolved {
		call := raw
		parts := [1]parser.Identifier{{Text: symbol}}
		call.Name = parser.ObjectName{Parts: parts[:]}
		_, resolution := JSONFunctionCall(symbol, true, len(raw.Args), jsonNamedCount(call))
		return true, resolution == Resolved && CallDecorationError(raw) == nil
	}
	return false, false
}

func namedJSONCall(b CallTypeContext, raw parser.FunctionExpr, args []Argument, declared []sqltype.Usage) ([]sqltype.Usage, []sqltype.Usage, bool) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if spec, known := jsonSlotSpecification(name); known {
		if !declaredTypes(declared, spec.formals) {
			return nil, nil, false
		}
		slots, _, valid := jsonSlotMapping(raw, spec)
		if !valid {
			return nil, nil, false
		}
		return namedInputs(b, args, declared, slots[:len(args)])
	}
	if name == "json_populate_record" || name == "json_populate_recordset" {
		return namedJSONRecord(b, raw, args, declared)
	}
	if name == "json_extract_path" || name == "json_extract_path_text" || name == "jsonb_extract_path" || name == "jsonb_extract_path_text" {
		if !raw.Variadic || len(args) != 2 || len(declared) != 2 || !jsonExtractLabels(raw) {
			return nil, nil, false
		}
		return namedInputs(b, args, declared, []int{0, 1})
	}
	if len(args) == 1 && jsonNamedCount(raw) == 0 {
		definition, resolution := JSONFunctionCall(name, true, 1, 0)
		if resolution != Resolved || !declaredTypes(declared, []sqltype.ID{definition.Input}) {
			return nil, nil, false
		}
		return namedInputs(b, args, declared, []int{0})
	}
	return nil, nil, false
}

func jsonExtractLabels(raw parser.FunctionExpr) bool {
	for i, label := range raw.ArgNames {
		if label.Text == "" {
			continue
		}
		expected := "from_json"
		if i > 0 {
			expected = "path_elems"
		}
		if !raw.Variadic || label.Text != expected {
			return false
		}
	}
	return true
}

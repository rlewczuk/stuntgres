package routine

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// StageCreateAggregate publishes a single real procedure/aggregate declaration
// through the ordinary schema stage. No state or support body executes here.
func StageCreateAggregate(h DDLHost, auth ddl.Authority, s *parser.CreateAggregate, input sqltype.InputEnvironment) error {
	ns, auth, err := h.CreationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	if err = h.CheckCreateIn(auth, ns); err != nil {
		return err
	}
	var support, finalName, movingName, inverseName, movingFinalName parser.ObjectName
	var stateSyntax, movingSyntax parser.TypeName
	var additional [5]parser.ObjectName
	var sortName parser.ObjectName
	parallel := byte('u')
	definition := &catalog.AggregateDefinition{Kind: "n", FinalModify: "r", MovingFinalModify: "r"}
	if s.Ordered {
		definition.Kind, definition.DirectArgs, definition.FinalModify, definition.MovingFinalModify = "o", int16(s.DirectArgs), "w", "w"
	}
	for _, option := range s.Options {
		switch option.Name {
		case "sfunc":
			support, err = operatorOptionName(option)
		case "finalfunc":
			finalName, err = operatorOptionName(option)
		case "finalfunc_extra":
			definition.FinalExtra, err = aggregateOptionBoolean(option)
		case "hypothetical":
			var hypothetical bool
			hypothetical, err = aggregateOptionBoolean(option)
			if err == nil && hypothetical {
				if !s.Ordered {
					return sqlError("42P13", "only ordered-set aggregates can be hypothetical")
				}
				definition.Kind = "h"
			}
		case "finalfunc_modify":
			definition.FinalModify, err = aggregateModify(option)
		case "msfunc":
			movingName, err = operatorOptionName(option)
		case "minvfunc":
			inverseName, err = operatorOptionName(option)
		case "mstype":
			movingSyntax, err = operatorOptionType(option)
		case "mfinalfunc":
			movingFinalName, err = operatorOptionName(option)
		case "mfinalfunc_extra":
			definition.MovingFinalExtra, err = aggregateOptionBoolean(option)
		case "mfinalfunc_modify":
			definition.MovingFinalModify, err = aggregateModify(option)
		case "minitcond":
			definition.MovingInitial, err = aggregateInitialText(option)
			definition.HasMovingInitial = true
		case "stype":
			stateSyntax, err = operatorOptionType(option)
		case "combinefunc", "serialfunc", "deserialfunc":
			slot := 2
			if option.Name == "serialfunc" {
				slot = 3
			} else if option.Name == "deserialfunc" {
				slot = 4
			}
			additional[slot], err = operatorOptionName(option)
		case "sortop":
			sortName, err = operatorOptionName(option)
		case "parallel":
			var value parser.ObjectName
			value, err = operatorOptionName(option)
			if err == nil && len(value.Parts) == 1 {
				switch strings.ToLower(value.Parts[0].Text) {
				case "safe":
					parallel = 's'
				case "restricted":
					parallel = 'r'
				case "unsafe":
					parallel = 'u'
				default:
					err = sqlError("42601", `parameter "parallel" must be SAFE, RESTRICTED, or UNSAFE`)
				}
			} else {
				err = sqlError("42601", `parameter "parallel" must be SAFE, RESTRICTED, or UNSAFE`)
			}
		case "initcond":
			definition.Initial, err = aggregateInitialText(option)
			definition.HasInitial = true
		case "sspace", "msspace":
			n, e := aggregateSpace(option)
			err = e
			if option.Name == "sspace" {
				definition.TransitionSpace = n
			} else {
				definition.MovingSpace = n
			}
		default:
			return expr.ErrorAt("0A000", "aggregate option "+option.Name+" is unsupported", option.Pos)
		}
		if err != nil {
			return err
		}
	}
	if stateSyntax.Name == "" {
		return sqlError("42P13", "aggregate stype must be specified")
	}
	if len(support.Parts) == 0 {
		return sqlError("42P13", "aggregate sfunc must be specified")
	}
	if movingSyntax.Name != "" {
		if len(movingName.Parts) == 0 {
			return sqlError("42P13", "aggregate msfunc must be specified when mstype is specified")
		}
		if len(inverseName.Parts) == 0 {
			return sqlError("42P13", "aggregate minvfunc must be specified when mstype is specified")
		}
	} else {
		for _, option := range []struct {
			name    string
			present bool
		}{
			{"msfunc", len(movingName.Parts) != 0}, {"minvfunc", len(inverseName.Parts) != 0},
			{"mfinalfunc", len(movingFinalName.Parts) != 0}, {"msspace", definition.MovingSpace != 0},
			{"minitcond", definition.HasMovingInitial},
		} {
			if option.present {
				return sqlError("42P13", "aggregate "+option.name+" must not be specified without mstype")
			}
		}
	}
	resolve := func(raw parser.TypeName) (sqltype.Usage, error) {
		id, err := h.ResolveRegtype(auth, raw)
		if err != nil {
			return sqltype.Usage{}, err
		}
		if err = checkOperatorArgumentUsage(h, auth, id); err != nil {
			return sqltype.Usage{}, err
		}
		typ, ok := h.DefinitionType(auth, sqltype.ID(id))
		if !ok || !typ.Concrete() && typ.ID() != 2281 && !(s.Ordered && typ.ID() == 2276) {
			return sqltype.Usage{}, expr.ErrorAt("0A000", "aggregate types must be concrete", raw.Pos)
		}
		return typ.Usage(), nil
	}
	declarations := make([]sqltype.Usage, len(s.Arguments))
	inputs := make([]sqltype.Usage, 1, len(s.Arguments)+1)
	inputs[0], err = resolve(stateSyntax)
	if err != nil {
		return err
	}
	if !inputs[0].Type.Concrete() && inputs[0].Type.ID() != 2281 {
		return sqlError("0A000", "aggregate state type must be concrete")
	}
	if inputs[0].Type.ID() == 2281 {
		av := h.AccessView(auth)
		actor, e := av.Roles.Descriptor(av.State.CurrentUser)
		if e != nil {
			return e
		}
		if !actor.Superuser {
			return sqlError("42P13", "aggregate transition data type cannot be internal")
		}
	}
	if (len(additional[3].Parts) == 0) != (len(additional[4].Parts) == 0) {
		return sqlError("42P13", "must specify both or neither of serialization and deserialization functions")
	}
	if len(additional[3].Parts) != 0 && inputs[0].Type.ID() != 2281 {
		return sqlError("42P13", "serialization functions may be specified only when the aggregate transition data type is internal")
	}
	signature := make([]string, len(s.Arguments))
	variadic := false
	for i, argument := range s.Arguments {
		if argument.Name.Text == "" {
			continue
		}
		for _, prior := range s.Arguments[:i] {
			if prior.Name.Text == argument.Name.Text {
				return sqlError("42P13", fmt.Sprintf("parameter name %q used more than once", argument.Name.Text))
			}
		}
	}
	for i, arg := range s.Arguments {
		if arg.Mode != "" && arg.Mode != "in" && arg.Mode != "variadic" {
			return expr.ErrorAt("0A000", "aggregates cannot have output arguments", arg.Pos)
		}
		if variadic {
			return expr.ErrorAt("42P13", "VARIADIC parameter must be the last input parameter", arg.Pos)
		}
		if !s.Ordered && arg.Mode == "variadic" {
			return expr.ErrorAt("0A000", "aggregate arguments must be ordinary inputs", arg.Pos)
		}
		declarations[i], err = resolve(arg.Type)
		if err != nil {
			return err
		}
		if arg.Mode == "variadic" {
			variadic = true
			if declarations[i].Type.ID() != 2276 && !declarations[i].Type.IsArray() {
				return expr.ErrorAt("42P13", "VARIADIC parameter must be an array", arg.Pos)
			}
		} else if !declarations[i].Type.Concrete() && declarations[i].Type.ID() != 2281 {
			return expr.ErrorAt("0A000", "aggregate types must be concrete", arg.Pos)
		}
		signature[i] = strconv.FormatUint(uint64(declarations[i].Type.ID()), 10)
	}
	// INTERNAL state is available only to a superuser creating the aggregate.
	// This is not a requirement on its eventual owner or on execution.
	checkCreatorState := func(state sqltype.Usage) error {
		if state.Type.ID() != 2281 {
			return nil
		}
		view := h.AccessView(auth)
		actor, e := view.Roles.Descriptor(view.State.CurrentUser)
		if e != nil {
			return e
		}
		return checkAggregateCreatorState(state, actor.Superuser)
	}
	if err = checkCreatorState(inputs[0]); err != nil {
		return err
	}
	var movingState sqltype.Usage
	if movingSyntax.Name != "" {
		movingState, err = resolve(movingSyntax)
		if err != nil {
			return err
		}
		if err = checkCreatorState(movingState); err != nil {
			return err
		}
	}
	// Function parameter rules precede aggregate-specific variadic restrictions.
	if s.Ordered && variadic && declarations[len(declarations)-1].Type.ID() != 2276 {
		return sqlError("0A000", "a variadic ordered-set aggregate must use VARIADIC type ANY")
	}
	if len(declarations) > 99 {
		return sqlError("54023", "aggregates cannot have more than 99 arguments")
	}
	direct := s.DirectArgs
	if direct < 0 || direct > len(declarations) {
		return sqlError("42P13", "invalid aggregate direct argument count")
	}
	rows := declarations[direct:]
	if variadic && direct == len(declarations) {
		rows = declarations[len(declarations)-1:]
	}
	if definition.Kind == "h" {
		if variadic && direct != len(declarations) {
			return sqlError("42P13", "a hypothetical-set aggregate must have direct arguments matching its aggregated arguments")
		}
		if direct < len(rows) {
			return sqlError("42P13", "a hypothetical-set aggregate must have direct arguments matching its aggregated arguments")
		}
		for i, row := range rows {
			if declarations[direct-len(rows)+i].Type != row.Type {
				return sqlError("42P13", "a hypothetical-set aggregate must have direct arguments matching its aggregated arguments")
			}
		}
	}
	inputs = append(inputs, rows...)
	function, formals, result, err := h.SelectAggregateSupport(auth, support, inputs)
	if err != nil {
		return err
	}
	types := make([]string, len(formals))
	for i, u := range formals {
		types[i] = u.Type.DiagnosticName()
	}
	diagnosticName := catalog.ObjectNameText(support) + "(" + strings.Join(types, ", ") + ")"
	if function.Builtin != nil && function.Builtin.ReturnsSet {
		return sqlError("42804", "function "+diagnosticName+" returns a set")
	}
	view := h.AccessView(auth).Catalog
	var recipe *catalog.AggregateBinaryRecipe
	capture := func(source, target sqltype.Usage) bool {
		if source.Type == target.Type && (source.Type.ID() == 2276 || source.Type.ID() == 2281) {
			return true
		}
		address, ok := catalog.ImplicitBinaryCast(view, source.Type.ID(), target.Type.ID())
		if !ok {
			return false
		}
		if address.ObjectID != 0 && !sqltype.BinaryCoercible(source.Type, target.Type) {
			edge, found := view.Get(address).(*catalog.Cast)
			if !found {
				return false
			}
			if recipe == nil {
				recipe = &catalog.AggregateBinaryRecipe{}
			}
			for _, old := range recipe.Casts {
				if old.Pair() == edge.Pair() {
					return true
				}
			}
			recipe.Casts = append(recipe.Casts, *edge)
		}
		_, err := catalog.AggregateConversion(recipe, source, target)
		return err == nil
	}
	for i, u := range inputs {
		if !capture(u, formals[i]) {
			return sqlError("42804", "function "+diagnosticName+" requires run-time type coercion")
		}
	}
	if result.Type != inputs[0].Type {
		return sqlError("42804", "return type of transition function "+catalog.ObjectNameText(support)+" is not "+inputs[0].Type.DiagnosticName())
	}
	if err = ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, function.Address()), Privileges: authorization.ExecutePrivilege}); err != nil {
		return err
	}
	if function.Strict && !definition.HasInitial && (len(declarations) == 0 || !capture(declarations[0], inputs[0])) {
		return sqlError("42P13", "must not omit initial value when transition function is strict and transition type is not compatible with input type")
	}
	for slot := 2; slot <= 4; slot++ {
		name := additional[slot]
		if len(name.Parts) == 0 {
			continue
		}
		arguments := []sqltype.Usage{inputs[0], inputs[0]}
		expected := inputs[0].Type
		label := "combine"
		if slot == 3 {
			arguments = arguments[:1]
			expected, _ = h.DefinitionType(auth, sqltype.Bytea)
			label = "serialization"
		} else if slot == 4 {
			arguments[0].Type, _ = h.DefinitionType(auth, sqltype.Bytea)
			arguments[0] = arguments[0].Type.Usage()
			label = "deserialization"
		}
		selected, formal, result, e := h.SelectAggregateSupport(auth, name, arguments)
		if e != nil {
			return e
		}
		if result.Type != expected {
			return sqlError("42804", "return type of "+label+" function "+catalog.ObjectNameText(name)+" is not "+expected.DiagnosticName())
		}
		if slot == 2 && inputs[0].Type.ID() == 2281 && selected.Strict {
			return sqlError("42P13", "combine function with transition type internal must not be declared STRICT")
		}
		for i, u := range arguments {
			if !capture(u, formal[i]) {
				return sqlError("42804", "function "+catalog.ObjectNameText(name)+" requires run-time type coercion")
			}
		}
		if err = ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, selected.Address()), Privileges: authorization.ExecutePrivilege}); err != nil {
			return err
		}
		if selected.RoutineDefinition() == nil && (selected.Builtin == nil || selected.Builtin.Providers == 0) && selected.Implementation == nil && !aggregateInternalSupport(selected, slot) {
			return sqlError("0A000", "aggregate "+label+" function has no native implementation")
		}
		definition.Functions[slot] = selected.Address().ObjectID
	}
	if len(sortName.Parts) != 0 {
		if len(s.Arguments) != 1 {
			return sqlError("42P13", "sort operator can only be specified for single-argument aggregates")
		}
		operator, e := findUtilityOperator(h, auth, parser.OperatorSignature{Name: sortName, Left: s.Arguments[0].Type, Right: s.Arguments[0].Type})
		if e != nil {
			return e
		}
		definition.SortOperator = operator.Address().ObjectID
	}
	if definition.HasInitial {
		if err = h.ValidateAggregateInitial(auth, inputs[0], definition.Initial, input); err != nil {
			return err
		}
	}
	aggregateResult := inputs[0]
	if len(finalName.Parts) != 0 {
		finalInputs := aggregateFinalDeclarationInputs(inputs[0], declarations, direct, definition.FinalExtra)
		final, finalFormals, finalResult, e := h.SelectAggregateSupport(auth, finalName, finalInputs)
		if e != nil {
			return e
		}
		finalTypes := make([]string, len(finalFormals))
		for i, formal := range finalFormals {
			finalTypes[i] = formal.Type.DiagnosticName()
		}
		finalDiagnostic := catalog.ObjectNameText(finalName) + "(" + strings.Join(finalTypes, ", ") + ")"
		if final.Builtin != nil && final.Builtin.ReturnsSet {
			return sqlError("42804", "function "+finalDiagnostic+" returns a set")
		}
		for i, actual := range finalInputs {
			if !capture(actual, finalFormals[i]) {
				return sqlError("42804", "function "+finalDiagnostic+" requires run-time type coercion")
			}
		}
		if final.Strict && definition.FinalExtra {
			return sqlError("42P13", "final function with extra arguments must not be declared STRICT")
		}
		if final.RoutineDefinition() == nil && (final.Builtin == nil || final.Builtin.Providers == 0) && final.Implementation == nil {
			return sqlError("0A000", "aggregate final function has no native implementation")
		}
		if err = ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, final.Address()), Privileges: authorization.ExecutePrivilege}); err != nil {
			return err
		}
		if err = checkOperatorArgumentUsage(h, auth, ids.OID(finalResult.Type.ID())); err != nil {
			return err
		}
		definition.Functions[1] = final.Address().ObjectID
		aggregateResult = finalResult
	}
	if aggregateResult.Type.ID() == 2281 {
		safe := false
		for _, u := range declarations {
			safe = safe || u.Type.ID() == 2281
		}
		if !safe {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: `unsafe use of pseudo-type "internal"`, Detail: "A result of type internal requires at least one input of type internal."}
		}
	}
	if inputs[0].Type.ID() == 2281 && !functions.IsAggregateContextSupport(uint32(function.Address().ObjectID)) && !aggregateInternalSupport(function, 0) {
		return sqlError("0A000", "aggregate transition function has no aggregate-context implementation")
	}
	if function.RoutineDefinition() == nil && (function.Builtin == nil || function.Builtin.Providers == 0) && function.Implementation == nil && !aggregateInternalSupport(function, 0) {
		return sqlError("0A000", "aggregate transition function has no native implementation")
	}
	if movingSyntax.Name != "" {
		movingInputs := append([]sqltype.Usage(nil), inputs...)
		movingInputs[0] = movingState
		selectSupport := func(name parser.ObjectName, actual []sqltype.Usage) (*catalog.Procedure, sqltype.Usage, error) {
			selected, declared, result, e := h.SelectAggregateSupport(auth, name, actual)
			if e != nil {
				return nil, sqltype.Usage{}, e
			}
			names := make([]string, len(declared))
			for i, formal := range declared {
				names[i] = formal.Type.DiagnosticName()
			}
			diagnostic := catalog.ObjectNameText(name) + "(" + strings.Join(names, ", ") + ")"
			if selected.Builtin != nil && selected.Builtin.ReturnsSet {
				return nil, result, sqlError("42804", "function "+diagnostic+" returns a set")
			}
			for i, source := range actual {
				if !capture(source, declared[i]) {
					return nil, result, sqlError("42804", "function "+diagnostic+" requires run-time type coercion")
				}
			}
			if selected.RoutineDefinition() == nil && (selected.Builtin == nil || selected.Builtin.Providers == 0) && selected.Implementation == nil {
				return nil, result, sqlError("0A000", "aggregate support function has no native implementation")
			}
			e = ddl.Check(h, auth, access.Requirement{Object: ddl.ObjectRef(h, auth, selected.Address()), Privileges: authorization.ExecutePrivilege})
			return selected, result, e
		}
		moving, movingResult, e := selectSupport(movingName, movingInputs)
		if e != nil {
			return e
		}
		if movingResult.Type != movingInputs[0].Type {
			return sqlError("42804", "return type of transition function "+catalog.ObjectNameText(movingName)+" is not "+movingInputs[0].Type.DiagnosticName())
		}
		if moving.Strict && !definition.HasMovingInitial && (len(declarations) == 0 || !capture(declarations[0], movingInputs[0])) {
			return sqlError("42P13", "must not omit initial value when transition function is strict and transition type is not compatible with input type")
		}
		inverse, inverseResult, e := selectSupport(inverseName, movingInputs)
		if e != nil {
			return e
		}
		if inverseResult.Type != movingInputs[0].Type {
			return sqlError("42804", "return type of inverse transition function "+catalog.ObjectNameText(inverseName)+" is not "+movingInputs[0].Type.DiagnosticName())
		}
		if inverse.Strict != moving.Strict {
			return sqlError("42P13", "strictness of aggregate's forward and inverse transition functions must match")
		}
		definition.Functions[5], definition.Functions[6] = moving.Address().ObjectID, inverse.Address().ObjectID
		definition.MovingType = ids.OID(movingInputs[0].Type.ID())
		if len(movingFinalName.Parts) != 0 {
			finalInputs := aggregateFinalDeclarationInputs(movingInputs[0], declarations, direct, definition.MovingFinalExtra)
			final, result, e := selectSupport(movingFinalName, finalInputs)
			if e != nil {
				return e
			}
			if final.Strict && definition.MovingFinalExtra {
				return sqlError("42P13", "final function with extra arguments must not be declared STRICT")
			}
			definition.Functions[7], movingResult = final.Address().ObjectID, result
		}
		if movingResult.Type != aggregateResult.Type {
			return sqlError("42P13", "moving-aggregate implementation returns type "+movingResult.Type.DiagnosticName()+", but plain implementation returns type "+aggregateResult.Type.DiagnosticName())
		}
		if definition.HasMovingInitial {
			if err = h.ValidateAggregateInitial(auth, movingInputs[0], definition.MovingInitial, input); err != nil {
				return err
			}
		}
	}
	d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID}, ObjectNamespace: ns.Address, ObjectName: s.Name.Parts[len(s.Name.Parts)-1].Text, Signature: catalog.ProcedureSignature(strings.Join(signature, ",")), RoutineKind: catalog.AggregateRoutine, Result: ids.OID(aggregateResult.Type.ID()), Owner: h.AccessView(auth).State.CurrentUser, Volatility: expr.Immutable, Display: catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: "aggregate_dummy"}, Aggregate: &catalog.OwnedAggregate{Definition: *definition}}
	if h.DefinitionLookup(auth, catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
		return sqlError("42723", fmt.Sprintf("function %q already exists with same argument types", d.Name()))
	}
	oid, err := h.AllocateOID()
	if err != nil {
		return err
	}
	d.ObjectAddress.ObjectID = oid
	d.ObjectRevision, err = h.Revision()
	if err != nil {
		return err
	}
	definition.Function, definition.Functions[0], definition.TransitionType = oid, function.Address().ObjectID, ids.OID(inputs[0].Type.ID())
	d.Aggregate.Definition = *definition
	d.Aggregate.Variadic = variadic
	for _, argument := range s.Arguments {
		if argument.Name.Text != "" {
			d.Aggregate.ArgumentNames = make([]string, len(s.Arguments))
			for i, argument := range s.Arguments {
				d.Aggregate.ArgumentNames[i] = argument.Name.Text
			}
			break
		}
	}
	d.Aggregate.BinaryRecipe = recipe
	d.Aggregate.Parallel = parallel
	d.ACL = h.CreationACL(d.Owner, d.Namespace(), authorization.DefaultRoutines)
	if err = h.Context().Err(); err != nil {
		return err
	}
	return ddl.CreateCommand(h.Stage(), "CREATE AGGREGATE", d)
}

// Definition strings preserve PostgreSQL's extracted external text, rather than
// evaluating an expression or resolving a TypeName-shaped seed as a real type.
func aggregateInitialText(option parser.OperatorOption) (string, error) {
	switch option.Kind {
	case parser.OperatorOptionStringArgument:
		return option.Value, nil
	case parser.OperatorOptionFloatArgument:
		return strings.TrimPrefix(option.Value, "+"), nil
	case parser.OperatorOptionIntegerArgument:
		value, err := strconv.ParseInt(option.Value, 10, 32)
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(value, 10), nil
	case parser.OperatorOptionTypeArgument:
		typ := option.Type
		text := typ.Name
		if len(typ.Parts) > 0 {
			text = aggregateDefinitionName(typ.Parts)
		} else if typ.Schema != nil {
			text = typ.Schema.Text + "." + text
		}
		if len(typ.ArrayBounds) > 0 {
			text += "[]"
		}
		return text, nil
	case parser.OperatorOptionNameArgument:
		return aggregateDefinitionName(option.Reference.Parts), nil
	case parser.OperatorOptionNoneArgument:
		return "none", nil
	default:
		return "", sqlError("42601", option.Name+" requires a parameter")
	}
}

func aggregateDefinitionName(parts []parser.Identifier) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0].Text
	}
	length := len(parts) - 1
	for _, part := range parts {
		length += len(part.Text)
	}
	var text strings.Builder
	text.Grow(length)
	for i, part := range parts {
		if i != 0 {
			text.WriteByte('.')
		}
		text.WriteString(part.Text)
	}
	return text.String()
}

func aggregateOptionBoolean(option parser.OperatorOption) (bool, error) {
	if !option.HasValue {
		return true, nil
	}
	text, err := aggregateInitialText(option)
	if err == nil {
		switch strings.ToLower(text) {
		case "true", "on", "1":
			return true, nil
		case "false", "off", "0":
			return false, nil
		}
	}
	return false, sqlError("42601", option.Name+" requires a Boolean value")
}

func aggregateModify(option parser.OperatorOption) (string, error) {
	text, err := aggregateInitialText(option)
	if err != nil {
		return "", err
	}
	switch text {
	case "read_only":
		return "r", nil
	case "shareable":
		return "s", nil
	case "read_write":
		return "w", nil
	}
	return "", sqlError("42601", `parameter "`+option.Name+`" must be READ_ONLY, SHAREABLE, or READ_WRITE`)
}

func aggregateSpace(option parser.OperatorOption) (int32, error) {
	if option.Kind != parser.OperatorOptionIntegerArgument {
		return 0, sqlError("42601", option.Name+" requires an integer value")
	}
	n, err := strconv.ParseInt(strings.TrimLeft(option.Value, "+-"), 10, 32)
	// Generic definition syntax classifies the unsigned magnitude before
	// applying its sign; out-of-range magnitudes are Float, not Integer.
	if err != nil {
		return 0, sqlError("42601", option.Name+" requires an integer value")
	}
	if strings.HasPrefix(option.Value, "-") {
		n = -n
	}
	return int32(n), nil
}

func checkAggregateCreatorState(state sqltype.Usage, superuser bool) error {
	if state.Type.ID() == 2281 && !superuser {
		return sqlError("42P13", "aggregate transition data type cannot be internal")
	}
	return nil
}

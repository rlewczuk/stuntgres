package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// BindRegexpCall binds a regexp_* call. place checks a set-returning call's
// placement, which the engine's analyzer context decides.
func BindRegexpCall(b Binder, raw parser.FunctionExpr, visible bool, place func(RegexpCall) error) (RegexpCall, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var call RegexpCall
	operands := make([]Argument, len(raw.Args))
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: StringTextArgument})
		if err != nil {
			return call, err
		}
		operands[i] = o
	}
	signature := func() error {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = NamedArgumentType(raw, i, DiagnosticNameOrUnknown(o.Usage.Type))
		}
		return ResolutionError(raw.Name, raw.Pos, names, false)
	}
	if len(raw.Args) > 100 {
		return call, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	issue := FunctionLabels(raw)
	switch {
	case issue.Malformed:
		return call, fmt.Errorf("invalid regexp argument labels")
	case issue.Positional:
		return call, expr.ErrorAt("42601", "positional argument cannot follow named argument", issue.Position)
	case issue.Duplicate != "":
		return call, expr.ErrorAt("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	replaceFlags, probe := RegexpReplaceSelection(raw, operands)
	var integer *sqltype.Descriptor
	if probe != nil {
		integer = b.Type(sqltype.Int4)
		replaceFlags = RegexpReplaceFlags(probe.Usage, integer.Usage())
	}
	labels, types, result, valid := RegexpSignature(name, len(operands), replaceFlags)
	if !visible || raw.Variadic || !valid {
		return call, signature()
	}
	call.WrittenSlots, call.Labels, valid = RegexpSlots(raw, labels, len(operands))
	if !valid {
		return call, signature()
	}
	var text *sqltype.Descriptor
	var targets [7]sqltype.Usage
	for i, operand := range operands {
		slot := call.WrittenSlots[i]
		var target *sqltype.Descriptor
		if types[slot] == sqltype.Text {
			if text == nil {
				text = b.Type(sqltype.Text)
			}
			target = text
		} else {
			if integer == nil {
				integer = b.Type(sqltype.Int4)
			}
			target = integer
		}
		targets[slot] = target.Usage()
		if !RegexpCompatible(operand.Usage, targets[slot]) {
			return call, signature()
		}
	}
	var err error
	call.Args, err = CoerceRegexpArguments(b, operands, call.WrittenSlots, targets)
	if err != nil {
		return call, err
	}
	properties := expr.Properties{Volatility: expr.Immutable}
	for _, arg := range call.Args {
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	properties.StageDependent = properties.StageDependent || RegexpStageDependent(name)
	input, err := b.CollatedInfo(b.Info(text.Usage(), raw.Pos, properties), call.Args...)
	if err != nil {
		return call, err
	}
	resultType := text
	if result != sqltype.Text {
		resultType = b.Type(result)
	}
	info := b.Info(resultType.Usage(), raw.Pos, properties)
	if info.Usage.Collation.ID != 0 {
		info.Usage.Collation = input.Usage.Collation
		info.CollationConflict = input.CollationConflict
	}
	call, err = NewRegexpCall(info, name, call.Args, call.WrittenSlots, call.Labels, input.Usage.Collation, input.CollationConflict)
	if err == nil && call.Plan.ReturnsSet() {
		err = place(call)
	}
	if err == nil {
		info, err = b.BuiltinRoutineInfo(info, RegexpRoutineProvider, name, types[:len(operands)]...)
		if err == nil {
			call.Info = info
		}
	}
	return call, err
}

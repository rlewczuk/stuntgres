package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func jsonExtractionName(name string) bool {
	return name == "->" || name == "->>" || name == "#>" || name == "#>>"
}
func (b *expressionBinder) bindJSONExtraction(raw parser.OperatorExpr) (BoundExpr, error) {
	var operands [2]comparisonOperand
	for i, arg := range raw.Args {
		o, err := b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
		if i < 2 {
			operands[i] = o
		}
	}
	left, right := operands[0].descriptor(), operands[1].descriptor()
	signature := func() error { return b.arrayMembershipError(left, right, raw) }
	if len(raw.Args) != 2 {
		return nil, operatorExpressionError(sqlError("42883", "operator does not exist"), left, right, raw)
	}
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, signature()
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	rhs := b.typ(sqltype.Text).Usage()
	if name == "#>" || name == "#>>" {
		a, ok := b.bindingCatalog().LookupArray(b.typ(sqltype.Text))
		if !ok {
			return nil, fmt.Errorf("missing text array")
		}
		rhs = a.Usage()
	} else if right != nil && (right.BaseType().ID() == sqltype.Int2 || right.BaseType().ID() == sqltype.Int4) {
		rhs = b.typ(sqltype.Int4).Usage()
	}
	document := b.typ(sqltype.JSON).Usage()
	if left != nil && left.BaseType().ID() == sqltype.JSONB {
		document = b.typ(sqltype.JSONB).Usage()
	}
	targets := [2]sqltype.Usage{document, rhs}
	for i, o := range operands {
		if o.descriptor() != nil {
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), targets[i], sqltype.Implicit); err != nil {
				return nil, signature()
			}
		}
	}
	if left == nil {
		return nil, b.comparisonError(sqlError("42725", "operator is not unique"), left, right, raw)
	}
	var args [2]BoundExpr
	properties := ExprProperties{Volatility: Immutable}
	for i, o := range operands {
		args[i], err = b.coerceConditional(o, targets[i], name)
		if err != nil {
			return nil, err
		}
		properties = mergeExpressionProperties(properties, args[i].Properties())
	}
	result := document
	if name == "->>" || name == "#>>" {
		result = b.typ(sqltype.Text).Usage()
	}
	info := b.info(result, raw.Pos, properties)
	node, err := functions.NewBinary(info, name, args[:], parser.OrdinaryFunctionSyntax)
	if err != nil {
		return nil, err
	}
	info, err = b.builtinOperatorRoutineInfo(info, name, document.Type.ID(), rhs.Type.ID())
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}
func (b *expressionBinder) bindJSONExtract(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	document := b.typ(sqltype.JSON).Usage()
	if name == "jsonb_extract_path" || name == "jsonb_extract_path_text" {
		document = b.typ(sqltype.JSONB).Usage()
	}
	var local [16]functions.Argument
	operands := local[:min(len(raw.Args), len(local))]
	if len(raw.Args) > len(local) && len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() (BoundExpr, error) {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = functionNamedType(raw, i, datetimeFormattingTypeName(o.Usage.Type))
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return nil, fmt.Errorf("invalid JSON extraction labels")
	}
	failure := functions.AnalyzeLabels(raw.ArgNames, raw.Args)
	switch failure.Kind {
	case functions.PositionalAfterNamed:
		return nil, expressionError("42601", "positional argument cannot follow named argument", failure.Pos)
	case functions.DuplicateLabel:
		return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", failure.Name), failure.Pos)
	}
	if !visible || raw.ResolutionOnly || len(operands) < 2 || raw.Variadic && len(operands) != 2 {
		return signature()
	}
	path := b.typ(sqltype.Text).Usage()
	if raw.Variadic {
		a, ok := b.bindingCatalog().LookupArray(b.typ(sqltype.Text))
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
	e := functions.JSONExtract{Args: make([]BoundExpr, len(operands)), Variadic: raw.Variadic}
	if len(raw.ArgNames) > 0 {
		e.Labels = make([]string, len(operands))
		for i, n := range raw.ArgNames {
			e.Labels[i] = n.Text
		}
	}
	properties := ExprProperties{Volatility: Immutable}
	for i, o := range operands {
		target := path
		if i == 0 {
			target = document
		}
		arg, err := b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.ExactFormalArgument, Target: target})
		if err != nil {
			return nil, err
		}
		e.Args[i] = arg
		properties = mergeExpressionProperties(properties, arg.Properties())
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
		return nil, expressionError("42809", decoration+" specified, but "+objectNameText(raw.Name)+" is "+message, raw.Pos)
	}
	result := document
	if name == "json_extract_path_text" || name == "jsonb_extract_path_text" {
		result = b.typ(sqltype.Text).Usage()
	}
	info := b.info(result, raw.Pos, properties)
	plan, err := sqltype.BindJSONExtract(name, result, e.Args[0].ResultUsage(), path, raw.Variadic)
	if err != nil {
		return nil, err
	}
	// The full released path formal is TEXT[], not expanded TEXT children.
	info, err = b.builtinRoutineInfo(info, jsonRoutineProvider, name, document.Type.ID(), sqltype.ID(1009))
	if err != nil {
		return nil, err
	}
	e.Info = info
	e.Plan = plan
	return e, nil
}
func jsonExtractSQL(e functions.JSONExtract, name string, render func(BoundExpr) (string, error), identifier func(string) string) (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	parts := make([]string, len(e.Args))
	for i, arg := range e.Args {
		text, err := render(arg)
		if err != nil {
			return "", err
		}
		if len(e.Labels) > 0 && e.Labels[i] != "" {
			text = identifier(e.Labels[i]) + " => " + text
		}
		if e.Variadic && i == len(e.Args)-1 {
			text = "VARIADIC " + text
		}
		parts[i] = text
	}
	return name + "(" + strings.Join(parts, ", ") + ")", nil
}

// mapJSONExtractChildren leaves the original immutable child slice untouched and
// only allocates when a child actually changes.
func mapJSONExtractChildren(e functions.JSONExtract, mapChild func(BoundExpr) (BoundExpr, bool, error)) (functions.JSONExtract, bool, error) {
	var args []BoundExpr
	for i, old := range e.Args {
		next, changed, err := mapChild(old)
		if err != nil {
			return e, false, err
		}
		if changed && args == nil {
			args = append([]BoundExpr(nil), e.Args...)
		}
		if args != nil {
			args[i] = next
		}
	}
	if args != nil {
		e.Args = args
	}
	return e, args != nil, nil
}

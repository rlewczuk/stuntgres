package engine

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func versionInformationSQL(e functions.VersionInformation) (string, error) {
	if err := e.Validate(expr.PropertiesMatch(e.Property, e.Kind.IntrinsicRule())); err != nil {
		return "", err
	}
	return e.Kind.Name() + "()", nil
}

func (b *expressionBinder) bindVersionInformation(raw parser.FunctionExpr) (BoundExpr, error) {
	kind := functions.VersionInformationKindForName(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	var names []string
	if len(raw.Args) != 0 {
		var err error
		names, _, err = b.functionArgumentTypeNames(raw)
		if err != nil {
			return nil, err
		}
	}
	if raw.Filter != nil {
		if _, err := b.bindBoolean(raw.Filter, projectionContext, "FILTER"); err != nil {
			return nil, err
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	issue := functions.FunctionLabels(raw)
	switch {
	case issue.Malformed:
		return nil, fmt.Errorf("invalid function argument labels")
	case issue.Positional:
		return nil, expressionError("42601", "positional argument cannot follow named argument", issue.Position)
	case issue.Duplicate != "":
		return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	if !visible || kind == 0 || len(raw.Args) != 0 || raw.Variadic || len(raw.ArgNames) != 0 {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	name := kind.Name()
	if decoration := functions.TransactionTimeDecoration(raw); decoration != "" {
		if decoration == "(*)" {
			return nil, expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
		}
		owner := "an aggregate function"
		if decoration == "OVER" {
			owner = "a window function nor an aggregate function"
		}
		return nil, expressionError("42809", decoration+" specified, but "+name+" is not "+owner, raw.Pos)
	}
	info := b.info(b.typ(sqltype.Text).Usage(), raw.Pos, expr.IntrinsicProperties(kind.IntrinsicRule()))
	info, err = b.builtinRoutineInfo(info, catalogRoutineProvider, name)
	if err != nil {
		return nil, err
	}
	return functions.VersionInformation{Info: functionInfo(info), Kind: kind}, nil
}

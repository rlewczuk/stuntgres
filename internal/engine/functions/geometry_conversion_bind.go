package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindGeometryConversion(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var small [2]Argument
	operands := small[:0]
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	missing := func(ambiguous bool) (expr.Expr, error) {
		names := make([]string, len(operands))
		for i, arg := range operands {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
		return nil, NotedResolutionError(b, raw.Name, raw.Pos, names, ambiguous)
	}
	if !visible || raw.Star || len(raw.ArgNames) != 0 || raw.Variadic {
		return missing(false)
	}
	var selected sqltype.GeometryConversion
	matches, best := 0, -1
	for op := sqltype.BoxCircle; op <= sqltype.PointPolygon; op++ {
		if op.Name() != name || op.Arity() != len(operands) {
			continue
		}
		exact, viable := 0, true
		for i, arg := range operands {
			if arg.Usage.Type == nil {
				continue
			}
			target := b.Type(op.Formal(i)).Usage()
			if arg.Usage == target {
				exact++
				continue
			}
			if _, err := sqltype.PlanCoercion(arg.Usage, target, sqltype.Implicit); err != nil {
				viable = false
				break
			}
		}
		if !viable {
			continue
		}
		if exact > best {
			selected, best, matches = op, exact, 1
		} else if exact == best {
			matches++
		}
	}
	// Function resolution step 3 applies after exact signatures, before best
	// implicit matching. An unresolved external parameter is not a Const.
	if len(operands) == 1 && (name == "box" || name == "circle" || name == "lseg" || name == "path" || name == "point") && best < 1 {
		arg := operands[0]
		var id sqltype.ID
		switch name {
		case "box":
			id = sqltype.Box
		case "circle":
			id = sqltype.Circle
		case "lseg":
			id = sqltype.Lseg
		case "path":
			id = sqltype.Path
		case "point":
			id = sqltype.Point
		}
		target := b.Type(id).Usage()
		cast := arg.Kind == DeferredStringArgument || arg.Kind == DeferredNullArgument
		var plan sqltype.CoercionPlan
		if arg.Usage.Type != nil {
			var err error
			plan, err = sqltype.PlanCoercion(arg.Usage, target, sqltype.Explicit)
			cast = err == nil && plan.FunctionCastShortcut()
		}
		if cast {
			if err := CallDecorationError(raw); err != nil {
				return nil, err
			}
			return b.CoerceArgument(arg, CoercionRequest{Intent: TypeNameCastArgument, Target: target, Plan: plan, Planned: true, TypeNameCallPosition: raw.Pos})
		}
	}
	if matches != 1 {
		return missing(matches > 1)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	var args [2]expr.Expr
	var formals [2]sqltype.ID
	properties := expr.Properties{Volatility: expr.Immutable}
	for i, arg := range operands {
		formals[i] = selected.Formal(i)
		args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: b.Type(formals[i]).Usage(), Label: name})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(selected.ResultID()).Usage(), raw.Pos, properties), GeometryConversionRoutineProvider, name, formals[:selected.Arity()]...)
	if err != nil {
		return nil, err
	}
	return NewGeometryConversion(info, selected, args, "pg_catalog")
}

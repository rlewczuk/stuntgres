package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// WindowSignature is a resolved window-function call: its coerced inputs and
// result usage. Window planning, frames and execution stay in the engine.
type WindowSignature struct {
	Argument, Ordinal, Default expr.Expr
	Usage                      sqltype.Usage
}

// WindowFunction registers a builtin window function with the engine's
// window binding. Signature analyzes the arguments and FILTER (analyzed, not
// kept) and resolves the call; for a call without OVER the engine reports
// "requires an OVER clause" after it succeeds.
type WindowFunction struct {
	Names     []string
	Signature func(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (WindowSignature, error)
	// ProbeCall is pure applicability for this window family's pseudo formals.
	// Window execution and diagnostics stay with Signature and the engine owner.
	ProbeCall CallShape
}

// The static table is the registry: no init() registration. The rank family
// is also registered as hypothetical-set aggregates for WITHIN GROUP calls.
var windowFunctions = [...]WindowFunction{
	{Names: []string{"row_number"}, Signature: rowNumberSignature},
	{Names: []string{"ntile"}, Signature: ntileSignature},
	{Names: []string{"first_value", "last_value", "nth_value"}, Signature: windowValueSignature, ProbeCall: probeWindowValue},
	{Names: []string{"lead", "lag"}, Signature: windowOffsetSignature, ProbeCall: probeWindowOffset},
	{Names: []string{"rank", "dense_rank", "percent_rank", "cume_dist"}, Signature: rankingSignature},
}

// LookupWindowFunction returns the definition that claims a window function
// name; names are matched exactly, as the parser folds them.
func LookupWindowFunction(name string) (WindowFunction, bool) {
	for _, function := range windowFunctions {
		for _, candidate := range function.Names {
			if candidate == name {
				return function, true
			}
		}
	}
	return WindowFunction{}, false
}

// WindowValueName reports first_value, last_value and nth_value.
func WindowValueName(name string) bool {
	return name == "first_value" || name == "last_value" || name == "nth_value"
}

// WindowOffsetName reports lead and lag.
func WindowOffsetName(name string) bool { return name == "lead" || name == "lag" }

func rowNumberSignature(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (WindowSignature, error) {
	types := make([]string, len(raw.Args))
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: BareProjectionArgument})
		if err != nil {
			return WindowSignature{}, err
		}
		types[i] = ArgumentTypeName(arg.Raw, arg.Expression)
	}
	if _, err := b.AnalyzeAggregateFilter(raw); err != nil {
		return WindowSignature{}, err
	}
	if len(raw.Args) > 100 {
		return WindowSignature{}, aggregateLimitError(raw.Pos)
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return WindowSignature{}, err
	}
	if !visible || len(raw.Args) != 0 || raw.Star {
		return WindowSignature{}, ResolutionError(name, raw.Pos, types, false)
	}
	return WindowSignature{Usage: b.Type(sqltype.Int8).Usage()}, nil
}

// rankingSignature resolves rank, dense_rank, percent_rank and cume_dist as
// window functions: their arguments are not analyzed, and a hidden builtin
// reports a signature with no types.
func rankingSignature(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (WindowSignature, error) {
	if _, err := b.AnalyzeAggregateFilter(raw); err != nil {
		return WindowSignature{}, err
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return WindowSignature{}, err
	}
	if !visible {
		return WindowSignature{}, ResolutionError(name, raw.Pos, nil, false)
	}
	usage := b.Type(sqltype.Int8).Usage()
	if raw.Name == "percent_rank" || raw.Name == "cume_dist" {
		usage = b.Type(sqltype.Float8).Usage()
	}
	return WindowSignature{Usage: usage}, nil
}

func ntileSignature(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (WindowSignature, error) {
	argument, err := resolveNtileSignature(b, raw, name)
	return WindowSignature{Argument: argument, Usage: b.Type(sqltype.Int4).Usage()}, err
}

func windowValueSignature(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (WindowSignature, error) {
	value, ordinal, usage, err := resolveWindowValueSignature(b, raw, name)
	return WindowSignature{Argument: value, Ordinal: ordinal, Usage: usage}, err
}

func windowOffsetSignature(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (WindowSignature, error) {
	value, offset, fallback, usage, err := resolveWindowOffsetSignature(b, raw, name)
	return WindowSignature{Argument: value, Ordinal: offset, Default: fallback, Usage: usage}, err
}

func resolveNtileSignature(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	var small [1]Argument
	var names [1]string
	arguments, types := small[:], names[:]
	if len(raw.Args) <= 1 {
		arguments, types = arguments[:len(raw.Args)], types[:len(raw.Args)]
	} else {
		arguments = make([]Argument, len(raw.Args))
		types = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		argument, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		arguments[i] = argument
		types[i] = "unknown"
		if argument.Usage.Type != nil {
			types[i] = b.DiagnosticTypeName(argument.Usage.Type)
		}
	}
	if _, err := b.AnalyzeAggregateFilter(raw); err != nil {
		return nil, err
	}
	if len(raw.Args) > 100 {
		return nil, aggregateLimitError(raw.Pos)
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return nil, err
	}
	var usage sqltype.Usage
	if len(arguments) > 0 {
		usage = arguments[0].Usage
	}
	integer := b.Type(sqltype.Int4).Usage()
	decision, plan := ResolveNtile(visible, raw.Star, len(arguments), usage, integer)
	if decision == Missing {
		return nil, ResolutionError(name, raw.Pos, types, false)
	}
	return b.CoerceArgument(arguments[0], CoercionRequest{Intent: ExactFormalArgument, Target: integer, AtRaw: true, Plan: plan, Planned: usage.Type != nil})
}

func resolveWindowOffsetSignature(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (value, offset, fallback expr.Expr, usage sqltype.Usage, err error) {
	var small [3]Argument
	var names [3]string
	arguments, types := small[:], names[:]
	if len(raw.Args) <= 3 {
		arguments, types = arguments[:len(raw.Args)], types[:len(raw.Args)]
	} else {
		arguments = make([]Argument, len(raw.Args))
		types = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arguments[i], err = b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return
		}
		types[i] = "unknown"
		if typ := arguments[i].Usage.Type; typ != nil {
			types[i] = b.DiagnosticTypeName(typ)
		}
	}
	if _, err = b.AnalyzeAggregateFilter(raw); err != nil {
		return
	}
	if len(raw.Args) > 100 {
		err = aggregateLimitError(raw.Pos)
		return
	}
	visible, failure := b.BuiltinVisible(name)
	if failure != nil {
		err = failure
		return
	}
	integer := b.Type(sqltype.Int4).Usage()
	var decision Resolution
	var offsetPlan sqltype.CoercionPlan
	usage, offsetPlan, decision = windowOffsetTargets(b, arguments, visible, raw.Star)
	if decision == Missing {
		err = ResolutionError(name, raw.Pos, types, false)
		return
	}
	if decision == UnknownPolymorphic {
		err = sqlError("42804", "could not determine polymorphic type because input has type unknown")
		return
	}
	// Coerce in argument order, so reused parameter occurrence diagnostics agree
	// with the formal value, INT4 offset, and common default types.
	if len(arguments) == 3 {
		value, err = b.CoerceArgument(arguments[0], CoercionRequest{Intent: ConditionalArgument, Target: usage})
	} else {
		value = arguments[0].Expression
	}
	if err != nil {
		return
	}
	if len(arguments) > 1 {
		offset, err = b.CoerceArgument(arguments[1], CoercionRequest{Intent: ExactFormalArgument, Target: integer, AtRaw: true, Plan: offsetPlan, Planned: arguments[1].Usage.Type != nil})
		if err != nil {
			return
		}
	}
	if len(arguments) == 3 {
		fallback, err = b.CoerceArgument(arguments[2], CoercionRequest{Intent: ConditionalArgument, Target: usage})
		if err != nil {
			return
		}
	}
	usage.Typmod = -1
	return
}

func resolveWindowValueSignature(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (value, ordinal expr.Expr, usage sqltype.Usage, err error) {
	var small [2]Argument
	var names [2]string
	arguments, types := small[:], names[:]
	if len(raw.Args) <= 2 {
		arguments, types = arguments[:len(raw.Args)], types[:len(raw.Args)]
	} else {
		arguments = make([]Argument, len(raw.Args))
		types = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arguments[i], err = b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return
		}
		types[i] = "unknown"
		if typ := arguments[i].Usage.Type; typ != nil {
			types[i] = b.DiagnosticTypeName(typ)
		}
	}
	if _, err = b.AnalyzeAggregateFilter(raw); err != nil {
		return
	}
	if len(raw.Args) > 100 {
		err = aggregateLimitError(raw.Pos)
		return
	}
	var visible bool
	visible, err = b.BuiltinVisible(name)
	if err != nil {
		return
	}
	var valueUsage, ordinalUsage sqltype.Usage
	if len(arguments) > 0 {
		valueUsage = arguments[0].Usage
	}
	if len(arguments) > 1 {
		ordinalUsage = arguments[1].Usage
	}
	integer := b.Type(sqltype.Int4).Usage()
	decision, plan := ResolveWindowValue(visible, raw.Star, raw.Name == "nth_value", len(arguments), valueUsage, ordinalUsage, integer)
	switch decision {
	case Missing:
		err = ResolutionError(name, raw.Pos, types, false)
		return
	case UnknownPolymorphic:
		err = sqlError("42804", "could not determine polymorphic type because input has type unknown")
		return
	}
	value = arguments[0].Expression
	if raw.Name == "nth_value" {
		ordinal, err = b.CoerceArgument(arguments[1], CoercionRequest{Intent: ExactFormalArgument, Target: integer, AtRaw: true, Plan: plan, Planned: ordinalUsage.Type != nil})
		if err != nil {
			return
		}
	}
	usage = value.ResultUsage()
	usage.Typmod = -1
	return
}

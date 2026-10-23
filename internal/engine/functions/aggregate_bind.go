package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Shared pieces of aggregate bind code. Every family analyzes its arguments,
// then FILTER, then resolves its signature, keeping PostgreSQL's diagnostic
// order; window calls of an aggregate resolve the same signature.

func aggregateLimitError(pos parser.Position) error {
	return expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", pos)
}

// aggregateUnknownError is the 42725 for a lone untyped argument of an
// aggregate whose candidates span several type categories.
func aggregateUnknownError(name parser.ObjectName, pos parser.Position) error {
	return ResolutionError(name, pos, []string{"unknown"}, true)
}

func comparisonOperand(b Binder, raw parser.Expr) (Argument, error) {
	return b.AnalyzeArgument(raw, AnalysisRequest{Intent: ComparisonArgument})
}

// coerceConditional coerces an operand to the selected formal as CASE-style
// unification does, labeling diagnostics with the aggregate's name.
func coerceConditional(b Binder, operand Argument, target sqltype.Usage, label string) (expr.Expr, error) {
	return b.CoerceArgument(operand, CoercionRequest{Intent: ConditionalArgument, Target: target, Label: label})
}

func finalizeOperand(b Binder, operand Argument, typ *sqltype.Descriptor) (expr.Expr, error) {
	return b.CoerceArgument(operand, CoercionRequest{Intent: FinalizeArgument, Target: typ.Usage()})
}

// Type spellings used for an aggregate's signature diagnostics.
func plainTypeName(_ AggregateBinder, operand Argument) (string, error) {
	return DiagnosticNameOrUnknown(operand.Usage.Type), nil
}

func catalogTypeName(b AggregateBinder, operand Argument) (string, error) {
	return b.DiagnosticTypeName(operand.Usage.Type), nil
}

func domainTypeName(b AggregateBinder, operand Argument) (string, error) {
	return b.DomainTypeName(operand.Usage.Type)
}

// windowAggregateOperands is the shared analysis of an aggregate called with
// OVER: comparison operands and, within the argument limit, their type names;
// then FILTER, which window aggregates analyze but do not keep; then the
// argument limit.
func windowAggregateOperands(b AggregateBinder, raw parser.AggregateExpr, typeName func(AggregateBinder, Argument) (string, error)) ([]Argument, []string, error) {
	var small [2]Argument
	var names [2]string
	var operands []Argument
	var types []string
	if len(raw.Args) <= 2 {
		operands, types = small[:len(raw.Args)], names[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
		types = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		operand, err := comparisonOperand(b, child)
		if err != nil {
			return nil, nil, err
		}
		if operands != nil {
			operands[i] = operand
			types[i], err = typeName(b, operand)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	if _, err := b.AnalyzeAggregateFilter(raw); err != nil {
		return nil, nil, err
	}
	if len(raw.Args) > 100 {
		return nil, nil, aggregateLimitError(raw.Pos)
	}
	return operands, types, nil
}

// ResolveWindowAggregate resolves the signature of an aggregate called with
// OVER and returns its coerced argument and result usage. An unregistered
// name gets the shared analysis and the missing-function error.
func ResolveWindowAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	if definition, ok := LookupAggregate(raw.Name); ok && definition.Window != nil {
		return definition.Window(b, raw, name)
	}
	_, types, err := windowAggregateOperands(b, raw, plainTypeName)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	return nil, sqltype.Usage{}, UnnotedResolutionError(b, name, raw.Pos, types, false)
}

func firstOperand(operands []Argument) Argument {
	if len(operands) == 0 {
		return Argument{}
	}
	return operands[0]
}

// AggregateOrdering binds the sort comparison of an aggregate ORDER BY,
// WITHIN GROUP or DISTINCT input, rejecting types without native ordering.
func AggregateOrdering(usage sqltype.Usage, pos parser.Position, distinct bool) (sqltype.Comparison, error) {
	base := usage.Type.BaseType()
	if base.IsArray() || base.IsComposite() || base.ID() == sqltype.PgNodeTree ||
		base.ID() == sqltype.Bit || base.ID() == sqltype.VarBit ||
		base.IsRange() || base.IsMultirange() {
		kind := "ordering"
		if distinct {
			kind = "DISTINCT"
		}
		return sqltype.Comparison{}, expr.ErrorAt("0A000", "aggregate "+kind+" is unsupported for type "+base.DiagnosticName(), pos)
	}
	comparison, err := sqltype.BindOrdering(usage)
	if d, ok := err.(*sqlstate.Error); ok {
		copy := *d
		copy.Position = fmt.Sprint(pos.Character)
		if distinct {
			copy.Hint = ""
			copy.Detail = "Aggregates with DISTINCT must be able to sort their inputs."
		}
		return sqltype.Comparison{}, &copy
	}
	return comparison, err
}

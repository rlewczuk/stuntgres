package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindXMLAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	var small [1]Argument
	var names [1]string
	var operands []Argument
	var types []string
	if len(raw.Args) <= 1 {
		operands, types = small[:len(raw.Args)], names[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands, types = make([]Argument, len(raw.Args)), make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := comparisonOperand(b, child)
		if err != nil { return nil, err }
		if operands != nil { operands[i], types[i] = o, DiagnosticNameOrUnknown(o.Usage.Type) }
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil { return nil, err }
	arg, err := resolveXMLAggregateSignature(b, raw, name, operands, types)
	if err != nil { return nil, err }
	usage := b.Type(sqltype.XML).Usage()
	call := AggregateCall{Provider: NewXMLAggregateProvider(usage), Arguments: []expr.Expr{arg}, Operation: AggregateXML}
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, raw.Name, sqltype.XML)
	if err != nil { return nil, err }
	return b.FinishAggregate(raw, call, filter, b.Info(usage, raw.Pos, expr.Properties{}))
}

func resolveXMLAggregateSignature(b Binder, raw parser.AggregateExpr, name parser.ObjectName, operands []Argument, types []string) (expr.Expr, error) {
	if len(raw.Args) > 100 { return nil, aggregateLimitError(raw.Pos) }
	visible, err := b.BuiltinVisible(name)
	if err != nil { return nil, err }
	typ := b.Type(sqltype.XML)
	if !visible || raw.Star || len(operands) != 1 || operands[0].Usage.Type != nil && operands[0].Usage.Type.BaseType() != typ {
		return nil, ResolutionError(name, raw.Pos, types, false)
	}
	return coerceConditional(b, operands[0], typ.Usage(), raw.Name)
}

func windowXMLAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	operands, types, err := windowAggregateOperands(b, raw, plainTypeName)
	if err != nil { return nil, sqltype.Usage{}, err }
	arg, err := resolveXMLAggregateSignature(b, raw, name, operands, types)
	if err != nil { return nil, sqltype.Usage{}, err }
	return arg, arg.ResultUsage(), nil
}

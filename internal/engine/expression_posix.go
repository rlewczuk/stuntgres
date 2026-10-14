package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindPosix(raw parser.OperatorExpr) (BoundExpr, error) {
	if !functions.PosixShape(raw) {
		return nil, fmt.Errorf("stuntgres: invalid POSIX expression")
	}
	left, err := b.AnalyzeArgument(raw.Args[0], functions.AnalysisRequest{Intent: functions.ComparisonArgument})
	if err != nil {
		return nil, err
	}
	right, err := b.AnalyzeArgument(raw.Args[1], functions.AnalysisRequest{Intent: functions.ComparisonArgument})
	if err != nil {
		return nil, err
	}
	visible, err := b.builtinVisible(raw.Name, operatorLookup)
	if err != nil {
		return nil, err
	}
	if !functions.PatternAllowed(visible, left.Usage, right.Usage) {
		return nil, operatorExpressionError(sqlError("42883", "operator does not exist"), left.Usage.Type, right.Usage.Type, raw)
	}
	l, r, err := functions.CoercePatternArguments(b, left, right, b.typ(sqltype.Text).Usage())
	if err != nil {
		return nil, err
	}
	collation, err := b.collatedInfo(b.info(l.ResultUsage(), raw.Pos, ExprProperties{}), l, r)
	if err != nil {
		return nil, err
	}
	op := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	result := b.typ(sqltype.Bool).Usage()
	info := b.info(result, raw.Pos, likeProperties(l, r))
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	return functions.NewPosix[BoundExpr, *EvalContext](functionInfo(info), l, r, op, namespace, collation.usage.Collation, collation.collationConflict)
}

func (b *expressionBinder) bindPosixSubstring(raw parser.FunctionExpr, operands []functions.Argument[BoundExpr]) (BoundExpr, error) {
	text := b.typ(sqltype.Text).Usage()
	args, err := functions.CoercePosixSubstringArguments(b, operands, text)
	if err != nil {
		return nil, err
	}
	info, err := b.collatedInfo(b.info(text, raw.Pos, likeProperties(args[0], args[1])), args[:]...)
	if err != nil {
		return nil, err
	}
	return functions.NewPosixSubstring[BoundExpr, *EvalContext](functionInfo(info), args, raw.Syntax)
}

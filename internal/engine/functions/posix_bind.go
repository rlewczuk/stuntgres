package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func BindPosix(b Binder, raw parser.OperatorExpr) (expr.Expr, error) {
	if !PosixShape(raw) {
		return nil, fmt.Errorf("stuntgres: invalid POSIX expression")
	}
	left, err := b.AnalyzeArgument(raw.Args[0], AnalysisRequest{Intent: ComparisonArgument})
	if err != nil {
		return nil, err
	}
	right, err := b.AnalyzeArgument(raw.Args[1], AnalysisRequest{Intent: ComparisonArgument})
	if err != nil {
		return nil, err
	}
	visible, err := b.BuiltinOperatorVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	if !PatternAllowed(visible, left.Usage, right.Usage) {
		return nil, OperatorError(sqlError("42883", "operator does not exist"), left.Usage.Type, right.Usage.Type, raw)
	}
	l, r, err := CoercePatternArguments(b, left, right, b.Type(sqltype.Text).Usage())
	if err != nil {
		return nil, err
	}
	collation, err := b.CollatedInfo(b.Info(l.ResultUsage(), raw.Pos, expr.Properties{}), l, r)
	if err != nil {
		return nil, err
	}
	op := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	result := b.Type(sqltype.Bool).Usage()
	info := b.Info(result, raw.Pos, expr.MergeProperties(l.Properties(), r.Properties()))
	namespace := ""
	if len(raw.Name.Parts) == 2 {
		namespace = raw.Name.Parts[0].Text
	}
	node, err := NewPosix(info, l, r, op, namespace, collation.Usage.Collation, collation.CollationConflict)
	if err != nil {
		return nil, err
	}
	info, err = b.BuiltinOperatorRoutineInfo(info, op, l.ResultUsage().Type.BaseType().ID(), sqltype.Text)
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}

func BindPosixSubstring(b Binder, raw parser.FunctionExpr, operands []Argument) (expr.Expr, error) {
	text := b.Type(sqltype.Text).Usage()
	args, err := CoercePosixSubstringArguments(b, operands, text)
	if err != nil {
		return nil, err
	}
	info, err := b.CollatedInfo(b.Info(text, raw.Pos, expr.MergeProperties(args[0].Properties(), args[1].Properties())), args[:]...)
	if err != nil {
		return nil, err
	}
	info, err = b.BuiltinRoutineInfo(info, StringRoutineProvider, raw.Name.Parts[len(raw.Name.Parts)-1].Text, sqltype.Text, sqltype.Text)
	if err != nil {
		return nil, err
	}
	return NewPosixSubstring(info, args, raw.Syntax)
}

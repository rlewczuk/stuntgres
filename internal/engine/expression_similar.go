package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (c *EvalContext) FunctionRegexExecution() *sqltype.PGRegexExecution {
	if scope := c.liveScope(); scope != nil {
		return &scope.pgRegex
	}
	return &c.pgRegex
}

func bindSimilar(b functions.Binder, raw parser.OperatorExpr) (BoundExpr, error) {
	if !functions.SimilarShape(raw) {
		return nil, fmt.Errorf("stuntgres: invalid SQL SIMILAR expression")
	}
	left, err := b.AnalyzeArgument(raw.Args[0], functions.AnalysisRequest{Intent: functions.ComparisonArgument})
	if err != nil {
		return nil, err
	}
	right, err := b.AnalyzeArgument(raw.Args[1], functions.AnalysisRequest{Intent: functions.ComparisonArgument})
	if err != nil {
		return nil, err
	}
	if !functions.PatternAllowed(true, left.Usage, right.Usage) {
		return nil, functions.OperatorError(sqlError("42883", "operator does not exist"), left.Usage.Type, right.Usage.Type, raw)
	}
	l, r, err := functions.CoercePatternArguments(b, left, right, b.Type(sqltype.Text).Usage())
	if err != nil {
		return nil, err
	}
	collation, err := b.CollatedInfo(b.Info(l.ResultUsage(), raw.Pos, ExprProperties{}), l, r)
	if err != nil {
		return nil, err
	}
	op := raw.Name.Parts[0].Text
	result := b.Type(sqltype.Bool).Usage()
	info := b.Info(result, raw.Pos, likeProperties(l, r))
	node, err := functions.NewSimilar(info, l, r, op, collation.Usage.Collation, collation.CollationConflict)
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
func bindSimilarEscape(b functions.Binder, raw parser.FunctionExpr) (BoundExpr, error) {
	valid, omitted := functions.SimilarEscapeShape(raw)
	if !valid {
		return nil, b.CallSignatureError(raw)
	}
	var operands [2]functions.Argument
	names := make([]string, len(raw.Args))
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = o
		names[i] = "unknown"
		if d := o.Usage.Type; d != nil {
			names[i] = d.DiagnosticName()
		}
	}
	for i := range raw.Args {
		if !functions.PatternTextType(operands[i].Usage.Type) {
			return nil, b.SignatureError(raw.Name, raw.Pos, names)
		}
	}
	text := b.Type(sqltype.Text).Usage()
	pattern, err := b.CoerceArgument(operands[0], functions.CoercionRequest{Intent: functions.LikeArgument, Target: text})
	if err != nil {
		return nil, err
	}
	var escape BoundExpr
	if omitted {
		escape = b.Literal(sqltype.String(text.Type, `\`), raw.Pos)
	} else {
		escape, err = b.CoerceArgument(operands[1], functions.CoercionRequest{Intent: functions.LikeArgument, Target: text})
		if err != nil {
			return nil, err
		}
	}
	info, err := b.CollatedInfo(b.Info(text, raw.Pos, likeProperties(pattern, escape)), pattern, escape)
	if err != nil {
		return nil, err
	}
	formal, count := [2]sqltype.ID{sqltype.Text, sqltype.Text}, 2
	if omitted {
		count = 1
	}
	info, err = b.BuiltinRoutineInfo(info, stringRoutineProvider, "similar_to_escape", formal[:count]...)
	if err != nil {
		return nil, err
	}
	return functions.NewSimilarEscape(info, pattern, escape, omitted)
}
func bindSimilarSubstring(b functions.Binder, raw parser.FunctionExpr, operands []functions.Argument) (BoundExpr, error) {
	text := b.Type(sqltype.Text).Usage()
	args, err := functions.CoerceSimilarSubstringArguments(b, operands, text)
	if err != nil {
		return nil, err
	}
	props := ExprProperties{Volatility: Immutable}
	for _, arg := range args {
		p := arg.Properties()
		props.Volatility = max(props.Volatility, p.Volatility)
		props.RowDependent = props.RowDependent || p.RowDependent
		props.ParameterDependent = props.ParameterDependent || p.ParameterDependent
		props.StageDependent = props.StageDependent || p.StageDependent
		props.DomainDependent = props.DomainDependent || p.DomainDependent
		props.RuntimeDomainConversion = props.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	wrapper := raw.Syntax == parser.OrdinaryFunctionSyntax || functions.SimilarSubstringWrapper(raw.Args, b.DefaultConsumer())
	info, err := b.CollatedInfo(b.Info(text, raw.Pos, props), args[:]...)
	if err != nil {
		return nil, err
	}
	info, err = b.BuiltinRoutineInfo(info, stringRoutineProvider, raw.Name.Parts[len(raw.Name.Parts)-1].Text, sqltype.Text, sqltype.Text, sqltype.Text)
	if err != nil {
		return nil, err
	}
	return functions.NewSimilarSubstring(info, args, raw.Syntax, wrapper)
}

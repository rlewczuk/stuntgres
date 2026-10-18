package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// StringNode builds a selected string call or the text || operator over coerced
// arguments, attaching its builtin routine identity. The engine's operator
// binder records catalog-identifier output and polymorphic body casts.
func StringNode(b Binder, name string, result sqltype.Usage, pos parser.Position, args []expr.Expr, textual [4]bool, syntax parser.StringSyntax) (expr.Expr, error) {
	stable, stage := StringIntrinsic(name, textual)
	p := expr.Properties{Volatility: expr.Immutable, StageDependent: stage}
	if stable {
		p.Volatility = expr.Stable
	}
	for _, arg := range args {
		child := arg.Properties()
		p.Volatility = max(p.Volatility, child.Volatility)
		p.RowDependent = p.RowDependent || child.RowDependent
		p.ParameterDependent = p.ParameterDependent || child.ParameterDependent
		p.StageDependent = p.StageDependent || child.StageDependent
		p.DomainDependent = p.DomainDependent || child.DomainDependent
		p.RuntimeDomainConversion = p.RuntimeDomainConversion || child.RuntimeDomainConversion
	}
	info := b.Info(result, pos, p)
	inputInfo := info
	if StringOperationCollation(name) {
		// Search/prefix demand input collation despite a noncollatable result.
		inputInfo = b.Info(b.Type(sqltype.Text).Usage(), pos, p)
	}
	inputInfo, err := b.CollatedInfo(inputInfo, args...)
	if err != nil {
		return nil, err
	}
	if result.Collation.ID != 0 {
		info = inputInfo
	}
	node, err := NewString(info, name, b.Type(sqltype.Text).Usage(), args, textual, syntax, inputInfo.CollationConflict)
	if err != nil {
		return nil, err
	}
	var formal [4]sqltype.ID
	for i := range args {
		formal[i] = node.Plan.ArgumentUsage(i).Type.ID()
	}
	count := len(args)
	if name == "normalize" || name == "is_normalized" {
		// The NFC default changes written arity, not the released declaration.
		formal[1], count = sqltype.Text, 2
	}
	if name == "parse_ident" {
		// Defaults and named actual order do not change the released declaration.
		formal[0], formal[1], count = sqltype.Text, sqltype.Bool, 2
	}
	routineName, provider := name, StringRoutineProvider
	if name == "||" {
		provider = OperatorRoutineProvider
		switch {
		case textual[0] && textual[1]:
			routineName, formal[0], formal[1] = "textcat", sqltype.Text, sqltype.Text
		case textual[0]:
			routineName, formal[0], formal[1] = "textanycat", sqltype.Text, sqltype.ID(2776)
		default:
			routineName, formal[0], formal[1] = "anytextcat", sqltype.ID(2776), sqltype.Text
		}
	}
	info, err = b.BuiltinRoutineInfo(info, provider, routineName, formal[:count]...)
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}

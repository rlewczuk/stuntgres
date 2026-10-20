package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindExtensionPaths(raw parser.FunctionExpr, arg expr.Expr) (expr.Expr, error) {
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{
		{Attribute: 1, Name: "source", Usage: b.typ(sqltype.Text).Usage()},
		{Attribute: 2, Name: "target", Usage: b.typ(sqltype.Text).Usage()},
		{Attribute: 3, Name: "path", Usage: b.typ(sqltype.Text).Usage()},
	})
	if err != nil {
		return nil, err
	}
	properties := arg.Properties()
	properties.Volatility = max(expr.Stable, properties.Volatility)
	info, err := b.builtinRoutineInfo(b.info(shape.Usage(), raw.Pos, properties), functions.CatalogRoutineProvider, "pg_extension_update_paths", sqltype.Name)
	if err != nil {
		return nil, err
	}
	b.consumer.catalogSnapshot, b.prepared.RegclassConcat = true, true
	return plan.ExtensionPathsExpr{Info: info, Arg: arg}, nil
}

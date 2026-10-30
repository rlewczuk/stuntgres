package plan

import (
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/sqltype"
)

// defaultFilterFraction supplies the released DEFAULT_EQ_SEL (1/200) when
// column statistics are unavailable. Measured/imported facts override this
// detached fallback through the existing statistics estimator.
func defaultFilterFraction(predicate expr.Expr)float64{
 comparison,ok:=predicate.(ComparisonExpr)
 if !ok||comparison.Operator!="="||comparison.Namespace!=""&&comparison.Namespace!="pg_catalog"{return .5}
 column,columnOK:=comparison.Left.(ColumnExpr);literal,literalOK:=comparison.Right.(LiteralExpr)
 if !columnOK||!literalOK{column,columnOK=comparison.Right.(ColumnExpr);literal,literalOK=comparison.Left.(LiteralExpr)}
 if !columnOK||!literalOK||column.Virtual!=nil||column.MergeSource||column.Usage.Type==nil||comparison.Usage.Type==nil||literal.Value.Type()!=column.Usage.Type{return .5}
 expected,err:=sqltype.BindComparisonPredicate(sqltype.CompareEqual,column.Usage.Type,column.Usage.Type,comparison.Usage.Type,column.Usage.Collation,false)
 if err!=nil||!expected.Equal(comparison.Predicate){return .5}
 if literal.Value.IsNull(){return 0}
 return 1.0/200
}

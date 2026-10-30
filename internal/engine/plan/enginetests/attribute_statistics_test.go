package enginetests

import (
 "math"
 "testing"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/storage"
 "stuntgres/internal/sqltype"
)
func TestAttributeStatisticsImportedProbabilities(t *testing.T){
 types:=make(map[sqltype.ID]*sqltype.Descriptor);for _,d:=range sqltype.Builtins(){types[d.ID()]=d}
 integer,boolean:=types[sqltype.Int4],types[sqltype.Bool]
 one,_:=sqltype.Integer(integer,1);half,_:=sqltype.Float(types[sqltype.Float4],.5)
 values,err:=sqltype.StatisticArrayValue(types[2277],integer.Usage(),[]sqltype.ArrayDimension{{Lower:1,Length:1}},[]sqltype.Value{one});if err!=nil{t.Fatal(err)}
 numbers,err:=sqltype.ArrayValue(types[1021],[]sqltype.ArrayDimension{{Lower:1,Length:1}},[]sqltype.Value{half});if err!=nil{t.Fatal(err)}
 fact:=storage.AttributeStatisticsCopy(nil,integer.Usage());fact.Imported.NullFraction=.25;fact.Imported.Distinct=10
 if err=fact.Imported.SetSlot(storage.AttributeStatisticsSlot{Kind:1,Values:values,Numbers:numbers});err!=nil{t.Fatal(err)}
 column:=plan.ColumnExpr{Info:expr.Info{Usage:integer.Usage()},Scope:1,Frame:1,Slot:0,QueryOccurrence:1}
 binding,err:=sqltype.BindComparisonPredicate(sqltype.CompareEqual,integer,integer,boolean,integer.Usage().Collation,false);if err!=nil{t.Fatal(err)}
 for _,tc:=range []struct{value int64;want float64}{{1,.5},{2,.25/9}}{
  v,_:=sqltype.Integer(integer,tc.value);predicate:=plan.ComparisonExpr{Info:expr.Info{Usage:boolean.Usage()},Left:column,Right:plan.LiteralExpr{Info:expr.Info{Usage:integer.Usage()},Value:v},Operator:"=",Predicate:binding}
  got,known,err:=plan.EstimateSelectivity(t.Context(),predicate,[]plan.ColumnStatisticInput{{Column:column,Statistics:fact,SourceRows:100}})
  if err!=nil||!known||math.Abs(got-tc.want)>1e-12||fact.Rows!=0||fact.NullCount!=0{t.Fatal(tc,got,known,err)}
 }
}

func TestAttributeStatisticsClearEqualityFallback(t *testing.T){
 types:=make(map[sqltype.ID]*sqltype.Descriptor);for _,d:=range sqltype.Builtins(){types[d.ID()]=d}
 integer,boolean:=types[sqltype.Int4],types[sqltype.Bool]
 column:=plan.ColumnExpr{Info:expr.Info{Usage:integer.Usage()},Scope:1,Frame:1,Slot:0}
 one,_:=sqltype.Integer(integer,1)
 literal:=plan.LiteralExpr{Info:expr.Info{Usage:integer.Usage()},Value:one}
 binding,err:=sqltype.BindComparisonPredicate(sqltype.CompareEqual,integer,integer,boolean,integer.Usage().Collation,false);if err!=nil{t.Fatal(err)}
 comparison:=plan.ComparisonExpr{Info:expr.Info{Usage:boolean.Usage()},Left:column,Right:literal,Operator:"=",Predicate:binding}
 for _,reverse:=range []bool{false,true}{
  predicate:=comparison;if reverse{predicate.Left,predicate.Right=predicate.Right,predicate.Left}
  estimate,err:=plan.EstimateExplainNode(&plan.FilterNode{Predicate:predicate},[]plan.ExplainEstimate{{Rows:100}},0)
  if err!=nil||estimate.Rows!=.5||estimate.RoundedRows()!=1{t.Fatal("DEFAULT_EQ_SEL fallback",estimate,err)}
 }
 comparison.Namespace="public"
 estimate,err:=plan.EstimateExplainNode(&plan.FilterNode{Predicate:comparison},[]plan.ExplainEstimate{{Rows:100}},0)
 if err!=nil||estimate.Rows!=50{t.Fatal("foreign operator inherited builtin fallback",estimate,err)}
}

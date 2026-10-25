package routine

import (
 "testing"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
)
func TestFTSVectorFunctionsRoutineDeclarations(t *testing.T){
 for _,tc:=range []struct{oid,result ids.OID;name,signature,source string}{
  {3711,23,"length","3614","tsvector_length"},
  {3624,3614,"setweight","3614,18","tsvector_setweight"},
  {3320,3614,"setweight","3614,18,1009","tsvector_setweight_by_filter"},
  {3623,3614,"strip","3614","tsvector_strip"},
  {3321,3614,"ts_delete","3614,25","tsvector_delete_str"},
  {3323,3614,"ts_delete","3614,1009","tsvector_delete_arr"},
  {3319,3614,"ts_filter","3614,1002","tsvector_filter"},
  {3326,1009,"tsvector_to_array","3614","tsvector_to_array"},
 }{
  got:=BuiltinRoutine(tc.oid);identity:=catalog.RoutineIdentity{ID:tc.oid,Namespace:ids.PGCatalogNamespaceOID,Name:tc.name,Signature:catalog.ProcedureSignature(tc.signature)}
  if got==nil||got.Identity!=identity||got.Result!=tc.result||got.Kind!=catalog.FunctionRoutine||!got.Strict||got.Volatility!=expr.Immutable||got.Parallel!='s'||got.ReturnsSet||got.Defaults!=0||got.Variadic!=0||got.Providers!=functions.FTSVectorRoutineProvider{t.Fatal(tc,got)}
  if display,ok:=builtinRoutineDisplay[tc.oid];!ok||display!=(catalog.RoutineDisplay{Language:catalog.InternalRoutineLanguage,Source:tc.source}){t.Fatal("display",tc,display)}
 }
 for _,oid:=range []ids.OID{3322,3327}{if BuiltinRoutine(oid)!=nil{t.Fatal("adjacent operation enabled",oid)}}
}

package enginetests

import (
 "context"
 "errors"
 "strings"
 "testing"
 "stuntgres/internal/sqltype"
)

func ltreeComparisonValue(t *testing.T,d *sqltype.Descriptor,text string)sqltype.Value{
 t.Helper();v,err:=d.Input(context.Background(),text,-1);if err!=nil{t.Fatal(err)};return v
}

func TestLtreeComparisonKernel(t *testing.T){
 d:=ltreeUnitType(t,65000)
 p,err:=sqltype.BindComparison(d,d,sqltype.CollationRef{},false);if err!=nil{t.Fatal(err)}
 long:=strings.Repeat("a",1000);deep:=strings.Repeat("a.",65534)+"a"
 for _,tc:=range []struct{a,b string;sign int}{
  {"","a",-1},{"a","a.b",-1},{"a.b","a-b",-1},{"a.z.z","b",-1},{"a.10","a.2",-1},{"A","a",-1},{"a.b","a.b",0},{"a.b","ab",-1},{"a.b","a.bc",-1},
  {long,long,0},{long,long[:999]+"b",-1},{deep,deep,0},{deep,deep[:len(deep)-1]+"b",-1},{deep[:len(deep)-2],deep,-1},
 }{
  a,b:=ltreeComparisonValue(t,d,tc.a),ltreeComparisonValue(t,d,tc.b)
  for _,swap:=range []bool{false,true}{left,right,want:=a,b,tc.sign;if swap{left,right,want=b,a,-want};if n,err:=p.Compare(t.Context(),left,right,sqltype.InputEnvironment{});err!=nil||n!=want{t.Fatalf("typed sign %d want %d: %v",n,want,err)}}
 }
 foreign:=ltreeUnitType(t,d.ID())
 if _,err:=sqltype.BindComparison(d,foreign,sqltype.CollationRef{},false);err==nil{t.Fatal("foreign generation admitted")}
 a:=ltreeComparisonValue(t,d,"a.b");b:=ltreeComparisonValue(t,foreign,"a.b")
 if _,err:=p.Compare(t.Context(),a,b,sqltype.InputEnvironment{});err==nil{t.Fatal("foreign payload admitted")}
 if _,err:=p.Compare(t.Context(),a,sqltype.Value{},sqltype.InputEnvironment{});err==nil{t.Fatal("zero datum admitted")}
 for _,typ:=range sqltype.Builtins(){if typ.ID()!=sqltype.Bool{continue};predicate,err:=sqltype.BindComparisonPredicate(sqltype.CompareEqual,d,d,typ,sqltype.CollationRef{},false);if err!=nil{t.Fatal(err)}
  result,err:=predicate.Evaluate(t.Context(),sqltype.Null(d),a,sqltype.InputEnvironment{});if err!=nil||!result.IsNull(){t.Fatal("strict NULL",err)}
  if _,err:=predicate.Evaluate(t.Context(),sqltype.Null(foreign),a,sqltype.InputEnvironment{});err==nil{t.Fatal("NULL bypassed generation admission")}
 }
}
func TestLtreeComparisonHash(t *testing.T){
 d:=ltreeUnitType(t,65000)
 // Literal words independently derived from REL_18_6 hashfn.c mix/final
 // and ltree_op.c's label fold, not by invoking native hash kernels.
 for _,tc:=range []struct{text string; plain uint32; extended [3]uint64}{
  {"",1,[3]uint64{1,2,0}},
  {"a",1075015888,[3]uint64{3591986179850072272,3325245193489759330,8688456822845412031}},
  {"a.b",3191900368,[3]uint64{17484788869952997584,16875352747571037434,8404158456352029976}},
  {"a-b",107705152,[3]uint64{13160211997610701632,5261849520545203284,3720325633993599315}},
  {"ab",1718550492,[3]uint64{2833316173711343580,6831083245906393189,3639176434904647022}},
  {"A",1656725517,[3]uint64{12183629641134612493,14145940235055443648,7085970626073673033}},
 }{
  v:=ltreeComparisonValue(t,d,tc.text)
  if got,err:=sqltype.LtreeHash(t.Context(),v);err!=nil||got!=tc.plain{t.Fatal("unseeded",tc.text,got,err)}
  for i,seed:=range []uint64{0,1,^uint64(0)}{if got,err:=sqltype.LtreeHashExtended(t.Context(),v,seed);err!=nil||got!=tc.extended[i]{t.Fatal("extended",tc.text,seed,got,err)}}
  if got,err:=sqltype.LtreeHash(t.Context(),ltreeComparisonValue(t,d,tc.text));err!=nil||got!=tc.plain{t.Fatal("independent parse hash",err)}
 }
 for _,v:=range []sqltype.Value{{},sqltype.Null(d)}{if _,err:=sqltype.LtreeHash(t.Context(),v);err==nil{t.Fatal("invalid hash datum")};if _,err:=sqltype.LtreeHashExtended(t.Context(),v,1);err==nil{t.Fatal("invalid seeded datum")}}
}

func TestLtreeComparisonGrouping(t *testing.T){
 d:=ltreeUnitType(t,65000);g,err:=sqltype.BindGrouping(d.Usage());if err!=nil{t.Fatal(err)}
 values:=[]sqltype.Value{ltreeComparisonValue(t,d,"a-b"),ltreeComparisonValue(t,d,"a.b"),ltreeComparisonValue(t,d,"a"),ltreeComparisonValue(t,d,""),sqltype.Null(d),ltreeComparisonValue(t,d,"a.b")}
 for i,a:=range values{for j,b:=range values{
  want:=i==j||i==1&&j==5||i==5&&j==1
  same,err:=g.Equal(t.Context(),a,b);if err!=nil||same!=want{t.Fatal("NOT DISTINCT equality",i,j,same,err)}
  if same{ah,err:=g.Hash(t.Context(),a);if err!=nil{t.Fatal(err)};bh,err:=g.Hash(t.Context(),b);if err!=nil||ah!=bh{t.Fatal("equal grouping hashes",err)}}
 }}
 foreign:=ltreeUnitType(t,d.ID())
 if _,err:=g.Equal(t.Context(),sqltype.Null(d),sqltype.Null(foreign));err==nil{t.Fatal("foreign NULL equality admitted")}
 if _,err:=g.Hash(t.Context(),sqltype.Null(foreign));err==nil{t.Fatal("foreign NULL hash admitted")}
}

func TestLtreeComparisonCancellation(t *testing.T){
 d:=ltreeUnitType(t,65000);p,err:=sqltype.BindComparison(d,d,sqltype.CollationRef{},false);if err!=nil{t.Fatal(err)}
 for _,text:=range []string{strings.Repeat("a",1000),strings.Repeat("a.",4096)+"a"}{
  v:=ltreeComparisonValue(t,d,text)
  for _,at:=range []int{1,3,5}{for _,kind:=range []string{"compare","hash","extended"}{
   base,cancel:=context.WithCancel(context.Background());ctx:=&ltreeCancelContext{Context:base,cancel:cancel,at:at}
   var err error
   switch kind{case "compare":_,err=p.Compare(ctx,v,v,sqltype.InputEnvironment{});case "hash":_,err=sqltype.LtreeHash(ctx,v);case "extended":_,err=sqltype.LtreeHashExtended(ctx,v,1)}
   cancel();if !errors.Is(err,context.Canceled)||ctx.calls!=at{t.Fatal("deterministic kernel cancellation",kind,at,ctx.calls,err)}
   var state interface{SQLState()string};if errors.As(err,&state){t.Fatal("ordinary cancellation acquired SQLSTATE")}
  }}
 }
}

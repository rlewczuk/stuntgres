package enginetests

import (
 "context"
 "errors"
 "reflect"
 "strings"
 "testing"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

type ltreeArrayTypes struct { path,query,text,paths,queries,boolean *sqltype.Descriptor }
func ltreeArrayUnitTypes(t *testing.T) ltreeArrayTypes {
 t.Helper();var d ltreeArrayTypes;var err error
 d.path,err=sqltype.Ltree(65000,"ltree","public.ltree");if err!=nil{t.Fatal(err)}
 d.query,err=sqltype.Lquery(65001,"lquery","public.lquery");if err!=nil{t.Fatal(err)}
 d.text,err=sqltype.Ltxtquery(65002,"ltxtquery","public.ltxtquery");if err!=nil{t.Fatal(err)}
 d.paths,err=sqltype.Array(65003,"_ltree",d.path);if err!=nil{t.Fatal(err)}
 d.queries,err=sqltype.Array(65004,"_lquery",d.query);if err!=nil{t.Fatal(err)}
 for _,typ:=range sqltype.Builtins(){if typ.ID()==sqltype.Bool{d.boolean=typ;break}}
 if d.boolean==nil{t.Fatal("missing bool")};return d
}
func ltreeArrayInput(t *testing.T,d *sqltype.Descriptor,text string) sqltype.Value {t.Helper();v,err:=d.Input(t.Context(),text,-1);if err!=nil{t.Fatal(err)};return v}
func ltreeArrayAssert(t *testing.T,op sqltype.LtreeArrayOperation,a,b sqltype.Value,result *sqltype.Descriptor,want any) {
 t.Helper();v,err:=op.Eval(t.Context(),a,b,a.Type(),b.Type(),result);if err!=nil{t.Fatal(err)}
 if want==nil{if !v.IsNull() || v.Type()!=result{t.Fatal("expected typed NULL",v)};return}
 if v.IsNull(){t.Fatal("unexpected NULL")}
 if expected,ok:=want.(bool);ok {got,err:=v.Boolean();if err!=nil || got!=expected{t.Fatal("boolean",got,err,"want",want)}}else{got,err:=result.Output(t.Context(),v);if err!=nil || got!=want{t.Fatal("path",got,err,"want",want)}}
}
func TestLtreeArrayValidation(t *testing.T) {
 d:=ltreeArrayUnitTypes(t);target:=ltreeArrayInput(t,d.path,"a.b")
 for _,tc:=range []struct{value,code,message string}{{"{{a,NULL}}","2202E","array must be one-dimensional"},{"{a,NULL}","22004","array must not contain nulls"}} {
  a:=ltreeArrayInput(t,d.paths,tc.value)
  value,err:=sqltype.LtreeArrayAncestor.Eval(t.Context(),a,target,d.paths,d.path,d.boolean)
  var diagnostic *sqlstate.Error
  if value.Type()!=nil || !errors.As(err,&diagnostic){t.Fatal("invalid array returned value",err)}
  want:=&sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:tc.code,Message:tc.message}
  if !reflect.DeepEqual(diagnostic,want){t.Fatalf("diagnostic %#v want %#v",diagnostic,want)}
  ltreeArrayAssert(t,sqltype.LtreeArrayAncestor,a,sqltype.Null(d.path),d.boolean,nil)
  for _,extract:=range []struct{op sqltype.LtreeArrayOperation;typ *sqltype.Descriptor}{{sqltype.LtreeArrayFirstQuery,d.query},{sqltype.LtreeArrayFirstTextQuery,d.text}} {
   query:=ltreeArrayInput(t,extract.typ,"a")
   value,err:=extract.op.Eval(t.Context(),a,query,d.paths,extract.typ,d.path)
   if value.Type()!=nil || !errors.As(err,&diagnostic) || !reflect.DeepEqual(diagnostic,want){t.Fatal("extractor validation precedence",value,err)}
   ltreeArrayAssert(t,extract.op,a,sqltype.Null(extract.typ),d.path,nil)
  }
 }
 invalid:=ltreeArrayInput(t,d.paths,"{{a,NULL}}")
 ltreeArrayAssert(t,sqltype.LtreeArraysQuery,invalid,ltreeArrayInput(t,d.queries,"{}"),d.boolean,false)
 ltreeArrayAssert(t,sqltype.LtreeArraysQueryReverse,ltreeArrayInput(t,d.queries,"{}"),invalid,d.boolean,false)
 _,err:=sqltype.LtreeArraysQuery.Eval(t.Context(),ltreeArrayInput(t,d.paths,"{NULL}"),ltreeArrayInput(t,d.queries,"{{a}}"),d.paths,d.queries,d.boolean)
 var diagnostic *sqlstate.Error;if !errors.As(err,&diagnostic) || diagnostic.Code!="2202E"{t.Fatal("query-first validation",err)}
 ltreeArrayAssert(t,sqltype.LtreeArrayFirstAncestor,ltreeArrayInput(t,d.paths,`[7:8]={"",a}`),target,d.path,"")
 ltreeArrayAssert(t,sqltype.LtreeArrayFirstDescendant,ltreeArrayInput(t,d.paths,"[0:1]={a.z,a.b}"),ltreeArrayInput(t,d.path,"a"),d.path,"a.z")
 ltreeArrayAssert(t,sqltype.LtreeArrayFirstAncestor,ltreeArrayInput(t,d.paths,"{}"),target,d.path,nil)
 foreign:=ltreeArrayUnitTypes(t)
 for _,extract:=range []struct{op sqltype.LtreeArrayOperation;typ *sqltype.Descriptor;query string}{{sqltype.LtreeArrayFirstQuery,d.query,"a.*"},{sqltype.LtreeArrayFirstTextQuery,d.text,"a"}} {
  a,q:=ltreeArrayInput(t,d.paths,"{a.z,a.b}"),ltreeArrayInput(t,extract.typ,extract.query)
  for _,wrong:=range []*sqltype.Descriptor{extract.typ,foreign.path,d.boolean} {
   if err:=extract.op.Validate(d.paths,extract.typ,wrong);err==nil{t.Fatal("foreign/non-element result generation accepted")}
  }
  for _,value:=range []sqltype.Value{ltreeArrayInput(t,foreign.paths,"{a}"),sqltype.Null(foreign.paths)} {
   if _,err:=extract.op.Eval(t.Context(),value,q,d.paths,extract.typ,d.path);err==nil{t.Fatal("foreign array generation accepted by extractor")}
  }
  foreignQuery:=foreign.query;if extract.typ==d.text{foreignQuery=foreign.text}
  for _,value:=range []sqltype.Value{ltreeArrayInput(t,foreignQuery,extract.query),sqltype.Null(foreignQuery)} {
   if _,err:=extract.op.Eval(t.Context(),a,value,d.paths,extract.typ,d.path);err==nil{t.Fatal("foreign query generation accepted by extractor")}
  }
 }
 for _,value:=range []sqltype.Value{ltreeArrayInput(t,foreign.paths,"{a}"),sqltype.Null(foreign.paths)} {
  if _,err:=sqltype.LtreeArrayAncestor.Eval(t.Context(),value,target,d.paths,d.path,d.boolean);err==nil{t.Fatal("same-OID foreign array accepted")}
 }
 for _,value:=range []sqltype.Value{ltreeArrayInput(t,foreign.path,"a"),sqltype.Null(foreign.path)} {
  if _,err:=sqltype.ArrayValue(d.paths,[]sqltype.ArrayDimension{{Lower:1,Length:1}},[]sqltype.Value{value});err==nil{t.Fatal("foreign element accepted")}
 }
 elements:=[]sqltype.Value{ltreeArrayInput(t,d.path,"a")};dimensions:=[]sqltype.ArrayDimension{{Lower:7,Length:1}}
 frozen,err:=sqltype.ArrayValue(d.paths,dimensions,elements);if err!=nil{t.Fatal(err)};elements[0]=sqltype.Null(d.path);dimensions[0].Lower=99
 ltreeArrayAssert(t,sqltype.LtreeArrayFirstAncestor,frozen,target,d.path,"a")
 if frozen.ArrayDimension(0).Lower!=7{t.Fatal("array borrowed dimensions")}
}
func TestLtreeArrayMatching(t *testing.T) {
 d:=ltreeArrayUnitTypes(t)
 for _,tc:=range []struct{path,query string;want bool}{
  {"","*",true},{"","a{0}",true},{"","a",false},{"a.a","a{0,2}.a",true},{"a.b","a{2}.b",false},
  {"a.a.b","a{1,2}.b",true},{"a.c","a.!b|d",true},{"a.b","a.!b|d",false},
  {"bar_foo","foo_bar%",true},{"foo","foo_foo%",true},{"a","_%",true},{"foo-bar","foo_bar%",false},
  {"FOO1_BAR2_baz","foo_bar%@*",true},{"foo1_br2_baz","foo_bar%*",false},
  {strings.Repeat("a",1000),strings.Repeat("a",1000),true},{strings.Repeat("a.",65534)+"a","a{65535}",true},
 } {
  path:=ltreeArrayInput(t,d.path,tc.path);array,err:=sqltype.ArrayValue(d.paths,[]sqltype.ArrayDimension{{Lower:1,Length:1}},[]sqltype.Value{path});if err!=nil{t.Fatal(err)}
  query:=ltreeArrayInput(t,d.query,tc.query)
  ltreeArrayAssert(t,sqltype.LtreeArrayQuery,array,query,d.boolean,tc.want)
  ltreeArrayAssert(t,sqltype.LtreeArrayQueryReverse,query,array,d.boolean,tc.want)
  var extracted any;if tc.want{extracted=tc.path}
  ltreeArrayAssert(t,sqltype.LtreeArrayFirstQuery,array,query,d.path,extracted)
  value,err:=sqltype.LtreeArrayFirstQuery.Eval(t.Context(),array,query,d.paths,d.query,d.path)
  if err!=nil || tc.want && value!=array.ArrayElement(0){t.Fatal("extraction copied path carrier",err)}
  remapped,err:=query.RemapLquery(ltreeArrayUnitTypes(t).query);if err!=nil{t.Fatal(err)};back,err:=remapped.RemapLquery(d.query);if err!=nil || back!=query{t.Fatal("compiled query backing copied",err)}
  if _,err:=query.Retag(remapped.Type());err==nil{t.Fatal("foreign query retag")}
 }
 for _,tc:=range []struct{paths,query string;want bool}{{"{Top,Europe}","Top & Europe",false},{"{Europe.Top}","Top & Europe",true},{`{""}`,"!foo",true},{"{Top.Blocked}","Top | Europe & !Blocked",true},{"{Top.Blocked}","(Top | Europe) & !Blocked",false},{"{Top}","!!Top",true}} {
  array,query:=ltreeArrayInput(t,d.paths,tc.paths),ltreeArrayInput(t,d.text,tc.query)
  ltreeArrayAssert(t,sqltype.LtreeArrayTextQuery,array,query,d.boolean,tc.want)
  value,err:=sqltype.LtreeArrayFirstTextQuery.Eval(t.Context(),array,query,d.paths,d.text,d.path)
  if err!=nil || tc.want && value!=array.ArrayElement(0) || !tc.want && (!value.IsNull() || value.Type()!=d.path){t.Fatal("text extraction carrier",value,err)}
 }
 for _,extract:=range []struct{op sqltype.LtreeArrayOperation;typ *sqltype.Descriptor;query string}{{sqltype.LtreeArrayFirstQuery,d.query,"Top.*"},{sqltype.LtreeArrayFirstTextQuery,d.text,"Top"}} {
  q:=ltreeArrayInput(t,extract.typ,extract.query)
  for _,paths:=range []string{"[7:9]={Other,Top.Z,Top.A}","[-3:-1]={Other,Top.Z,Top.A}","{Other,Top.Z,Top.Z}"} {
   a:=ltreeArrayInput(t,d.paths,paths);v,err:=extract.op.Eval(t.Context(),a,q,d.paths,extract.typ,d.path)
   if err!=nil || v!=a.ArrayElement(1) || v.Type()!=d.path{t.Fatal("first physical carrier not retained",v,err)}
  }
  for _,paths:=range []string{"{Other}","{}"}{ltreeArrayAssert(t,extract.op,ltreeArrayInput(t,d.paths,paths),q,d.path,nil)}
 }
 for _,tc:=range []struct{paths,target string;want bool}{{"{a}","a.b",true},{"{ab}","a",false},{`{""}`,"a",true},{"{A}","a.b",false}}{ltreeArrayAssert(t,sqltype.LtreeArrayAncestor,ltreeArrayInput(t,d.paths,tc.paths),ltreeArrayInput(t,d.path,tc.target),d.boolean,tc.want)}
 t.Run("dead-state-work-bound",func(t *testing.T) {
  // Budget comfortably admits one transition, but not the remaining 4095.
  // Inputs are parsed before arming the deterministic work context.
  a:=ltreeArrayInput(t,d.paths,"{"+strings.Repeat("a.",4095)+"a}")
  q:=ltreeArrayInput(t,d.query,strings.Repeat("b.",4095)+"b")
  ctx:=&ltreeWorkContext{Context:context.Background(),remaining:32768}
  value,err:=sqltype.LtreeArrayQuery.Eval(ctx,a,q,d.paths,d.query,d.boolean)
  if err!=nil {t.Fatal("dead-state match exhausted work budget",err)}
  matched,err:=value.Boolean();if err!=nil || matched {t.Fatal("early mismatch",matched,err)}
  if ctx.remaining<=0 {t.Fatal("work budget exhausted")}
  // A subsequent operation must still match independently of the dead state.
  ltreeArrayAssert(t,sqltype.LtreeArrayQuery,a,ltreeArrayInput(t,d.query,"a{4095}.a"),d.boolean,true)
 })
 t.Run("parallel-extractors",func(t *testing.T){
  first:=ltreeArrayInput(t,d.paths,"[7:9]={Other,Top.Z,Top.A}")
  second:=ltreeArrayInput(t,d.paths,"[-3:-1]={Other,Top.A,Top.Z}")
  type outcome struct{value,want sqltype.Value;err error}
  results:=make(chan outcome,2);start:=make(chan struct{})
  for _,extract:=range []struct{op sqltype.LtreeArrayOperation;typ *sqltype.Descriptor;query string}{{sqltype.LtreeArrayFirstQuery,d.query,"Top.*"},{sqltype.LtreeArrayFirstTextQuery,d.text,"Top"}} {
   query:=ltreeArrayInput(t,extract.typ,extract.query)
   go func(){
    <-start
    for range 8{for _,array:=range []sqltype.Value{first,second}{
     value,err:=extract.op.Eval(t.Context(),array,query,d.paths,extract.typ,d.path)
     if err!=nil || value!=array.ArrayElement(1){results<-outcome{value,array.ArrayElement(1),err};return}
    }}
    results<-outcome{}
   }()
  }
  close(start)
  completed:=[]outcome{<-results,<-results}
  for _,result:=range completed{if result.err!=nil || result.value!=result.want{t.Fatal("parallel matching scratch/carrier ownership",result)}}
 })
}

// Cancellation is triggered by real work's context checks, without scheduling
// races or sleeping. Input uses a separate context before the armed evaluation.
type ltreeWorkContext struct {context.Context; remaining int}
func(c *ltreeWorkContext)Err()error{c.remaining--;if c.remaining<=0{return context.Canceled};return nil}
func TestLtreeArrayCancellation(t *testing.T) {
 d:=ltreeArrayUnitTypes(t)
 path:=strings.Repeat("a.",4095)+"a"
 for _,tc:=range []struct{op sqltype.LtreeArrayOperation;paths,query string;typ *sqltype.Descriptor}{
  {sqltype.LtreeArrayQuery,"{"+path+"}","*.*.a{1,4096}",d.query},
  {sqltype.LtreeArrayQuery,"{"+strings.Repeat("a_",499)+"a}","z%",d.query},
  {sqltype.LtreeArrayTextQuery,"{"+path+"}","z | y | x",d.text},
  {sqltype.LtreeArrayFirstQuery,"{"+path+"}","*.*.a{1,4096}",d.query},
  {sqltype.LtreeArrayFirstQuery,"{"+strings.Repeat("a_",499)+"a}","z%",d.query},
  {sqltype.LtreeArrayFirstTextQuery,"{"+path+"}","z | y | x",d.text},
 } {
  a,b:=ltreeArrayInput(t,d.paths,tc.paths),ltreeArrayInput(t,tc.typ,tc.query)
  result:=d.boolean;if tc.op>=sqltype.LtreeArrayFirstAncestor{result=d.path}
  canceled,cancel:=context.WithCancel(context.Background());cancel()
  value,err:=tc.op.Eval(canceled,a,b,d.paths,tc.typ,result)
  var state interface{SQLState()string}
  if !errors.Is(err,context.Canceled) || errors.As(err,&state) || value.Type()!=nil{t.Fatal("pre-canceled extraction contract",value,err)}
  for _,budget:=range []int{1,2,8} {
   ctx:=&ltreeWorkContext{Context:context.Background(),remaining:budget}
   value,err:=tc.op.Eval(ctx,a,b,d.paths,tc.typ,result)
   var state interface{SQLState()string}
   if !errors.Is(err,context.Canceled) || errors.As(err,&state) || value.Type()!=nil{t.Fatal("cancellation contract",value,err)}
  }
  if _,err:=tc.op.Eval(t.Context(),a,b,d.paths,tc.typ,result);err!=nil{t.Fatal("execution scratch leaked cancellation",err)}
  if tc.op>=sqltype.LtreeArrayFirstQuery {
   many:=ltreeArrayInput(t,d.paths,"{"+strings.Repeat("a,",32)+"a}")
   ctx:=&ltreeWorkContext{Context:context.Background(),remaining:8}
   value,err:=tc.op.Eval(ctx,many,b,d.paths,tc.typ,result)
   if !errors.Is(err,context.Canceled) || value.Type()!=nil{t.Fatal("validation cancellation returned partial extraction",value,err)}
  }
 }
}

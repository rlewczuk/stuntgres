package parser

import "testing"

func TestAggregateOrderedDeclaration(t *testing.T) {
 for _,tc:=range []struct{signature string;direct,count int;ordered bool}{
  {"integer",0,1,false},
  {"ORDER BY integer",0,1,true},
  {"IN prefix text, IN descending boolean ORDER BY IN value integer",2,3,true},
  {`VARIADIC "any" ORDER BY VARIADIC "any"`,1,1,true},
  {`text, VARIADIC "any" ORDER BY VARIADIC "any"`,2,2,true},
  {`ORDER BY VARIADIC "any"`,0,1,true},
 }{
  statements,err:=Parse("CREATE AGGREGATE public.a("+tc.signature+") (SFUNC=step,STYPE=integer,FINALFUNC=finish,FINALFUNC_EXTRA,HYPOTHETICAL=false)")
  if err!=nil{t.Fatal(tc.signature,err)}
  declaration:=statements[0].(*CreateAggregate)
  if declaration.Ordered!=tc.ordered||declaration.DirectArgs!=tc.direct||len(declaration.Arguments)!=tc.count{t.Fatalf("%s: %#v",tc.signature,declaration)}
 }
 for _,signature:=range []string{"OUT integer ORDER BY integer","integer ORDER BY INOUT integer",`VARIADIC integer[] ORDER BY integer[]`,`VARIADIC integer[] ORDER BY VARIADIC bigint[]`}{
  statements,err:=Parse("CREATE AGGREGATE a("+signature+") (SFUNC=step,STYPE=integer)")
  if err==nil||len(statements)!=0{t.Fatal("invalid declaration survived whole-input parse",signature,statements,err)}
 }
}

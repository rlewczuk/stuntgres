package functions

import (
 "context"
 "errors"
 "reflect"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
 "testing"
)

func TestXMLTableExpressionDemand(t *testing.T) {
 types:=temporalTestTypes();c:=&temporalTestContext{Context:context.Background()}
 input:=func(id sqltype.ID,text string)sqltype.Value{t.Helper();v,err:=types[id].Input(c.Context,text,-1);if err!=nil {t.Fatal(err)};return v}
 var events []string
 child:=func(label string,value sqltype.Value)expr.Expr{return sqlJSONQueryUnitChild{temporalTestArgument:temporalTestLiteral(value),label:label,events:&events}}
 raw:=&parser.XMLTable{Namespaces:[]parser.XMLNamespace{{Name:parser.Identifier{Text:"p"},URI:parser.ParameterExpr{Number:1}}},Row:parser.ParameterExpr{Number:2},Document:parser.ParameterExpr{Number:3},Columns:[]parser.XMLTableColumn{{Name:parser.Identifier{Text:"n"},Ordinality:true},{Name:parser.Identifier{Text:"v"},Options:[]parser.XMLTableOption{{Kind:"path",Value:parser.ParameterExpr{Number:4}},{Kind:"default",Value:parser.ParameterExpr{Number:5}}}}}}
 members:=[]sqltype.CompositeMember{{Attribute:1,Name:"n",Usage:types[sqltype.Int4].Usage()},{Attribute:2,Name:"v",Usage:types[sqltype.Int4].Usage()}}
 shape,err:=sqltype.RecordShape(members);if err!=nil {t.Fatal(err)}
 args:=[]expr.Expr{child("namespace",input(sqltype.Text,"urn:r")),child("row",input(sqltype.Text,"/p:r/p:v")),child("document",input(sqltype.XML,`<r xmlns="urn:r"><v a="7"/><v/><v/></r>`)),child("path",input(sqltype.Text,"@a")),child("default",input(sqltype.Int4,"42"))}
 fields:=[]XMLTableField{{Usage:members[0].Usage,Path:-1,Default:-1,Ordinality:true},{Usage:members[1].Usage,Path:3,Default:4}}
 node,err:=NewXMLTable(expr.Info{Usage:shape.Usage()},raw,args,fields);if err!=nil {t.Fatal(err)}
 for range 2 {
  events=nil
  source,err:=node.OpenSource(c);if err!=nil {t.Fatal(err)}
  for i,want:=range []int64{7,42,42} {
   value,present,err:=source.Next(c.Context);if err!=nil || !present {t.Fatal(err)}
   ordinal,err:=value.CompositeField(0).Integer();if err!=nil || ordinal!=int64(i+1) {t.Fatal(ordinal,err)}
   actual,err:=value.CompositeField(1).Integer();if err!=nil || actual!=want {t.Fatal(actual,err)}
  }
  source.Close()
  if !reflect.DeepEqual(events,[]string{"document","namespace","row","path","default","default"}) {t.Fatal("incorrect initialization or default demand",events)}
 }
 nullArgs:=append([]expr.Expr(nil),args...);nullArgs[2]=child("document",sqltype.Null(types[sqltype.XML]))
 empty,err:=node.Rebuild(expr.InfoOf(node),nullArgs);if err!=nil {t.Fatal(err)}
 events=nil;source,err:=empty.(XMLTable).OpenSource(c);if err!=nil {t.Fatal(err)}
 _,present,err:=source.Next(c.Context);source.Close()
 if err!=nil || present || !reflect.DeepEqual(events,[]string{"document"}) {t.Fatal("NULL document demanded runtime children",events,present,err)}
 t.Run("default namespace demand",func(t *testing.T){
  accepted:=raw.WithExpressions(raw.Expressions())
  accepted.Namespaces[0].Default=true
  accepted.Namespaces[0].Name=parser.Identifier{}
  for _,tc:=range []struct{name string;document,uri sqltype.Value;code string;want []string}{
   {"unsupported",input(sqltype.XML,`<r/>`),input(sqltype.Text,"urn:r"),"0A000",[]string{"document","namespace"}},
   {"null document",sqltype.Null(types[sqltype.XML]),sqltype.Null(types[sqltype.Text]),"",[]string{"document"}},
   {"fragment before namespace",input(sqltype.XML,`<a/><b/>`),input(sqltype.Text,"urn:r"),"2200M",[]string{"document"}},
   {"null URI before unsupported",input(sqltype.XML,`<r/>`),sqltype.Null(types[sqltype.Text]),"22004",[]string{"document","namespace"}},
  } {
   t.Run(tc.name,func(t *testing.T){
    operands:=append([]expr.Expr(nil),args...)
    operands[0]=child("namespace",tc.uri);operands[2]=child("document",tc.document)
    table,err:=NewXMLTable(expr.InfoOf(node),accepted,operands,fields);if err!=nil {t.Fatal(err)}
    events=nil
    source,err:=table.OpenSource(c)
    if tc.code=="" {
     if err!=nil || source.Type!=shape {t.Fatal("typed empty source lost",err)}
     _,present,err:=source.Next(c.Context);source.Close()
     if err!=nil || present {t.Fatal(present,err)}
    } else {
     var diagnostic *sqlstate.Error
     if !errors.As(err,&diagnostic) || diagnostic.Code!=tc.code {t.Fatal(tc.code,err)}
     if source.Type!=nil || len(source.Values)!=0 || source.State!=nil || source.Advance!=nil {t.Fatal("failed initialization published source state")}
    }
    if !reflect.DeepEqual(events,tc.want) {t.Fatal("namespace error crossed runtime demand boundary",events,tc.want)}
   })
  }
  failure:=errors.New("namespace expression failure")
  failing:=child("namespace",input(sqltype.Text,"urn:r")).(sqlJSONQueryUnitChild);failing.err=failure
  operands:=append([]expr.Expr(nil),args...);operands[0]=failing
  table,err:=NewXMLTable(expr.InfoOf(node),accepted,operands,fields);if err!=nil {t.Fatal(err)}
  events=nil
  source,err:=table.OpenSource(c)
  if !errors.Is(err,failure) || source.Type!=nil || !reflect.DeepEqual(events,[]string{"document","namespace"}) {t.Fatal("unsupported DEFAULT suppressed URI expression failure",events,err)}
  if raw.Namespaces[0].Default || raw.Namespaces[0].Name.Text!="p" {t.Fatal("private demand mutated accepted source syntax")}
 })
 if &node.Args[0]!=&args[0] || &node.Fields[0]!=&fields[0] {t.Fatal("rebuild changed accepted provider backing")}
}

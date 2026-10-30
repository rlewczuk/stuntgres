package parser

import (
 "reflect"
 "strings"
 "testing"
)

func TestXMLProduceGrammar(t *testing.T){
 for _,tc:=range []struct{q string;kind XMLConstructKind;count int;standalone uint8}{
  {`SELECT xmlconcat($1,$2)`,XMLConcat,2,0},
  {`SELECT xmlelement(NAME SELECT,XMLATTRIBUTES($1 AS FROM,$2 AS "a:b"),$3,$4)`,XMLElement,4,0},
  {`SELECT xmlforest(($1) AS a,$2 AS b)`,XMLForest,2,0},
  {`SELECT xmlpi(NAME p)`,XMLPI,0,0},{`SELECT xmlpi(NAME p,NULL)`,XMLPI,1,0},
  {`SELECT xmlroot($1,VERSION $2)`,XMLRoot,2,0},
  {`SELECT xmlroot($1,VERSION NO VALUE,STANDALONE NO VALUE)`,XMLRoot,1,1},
  {`SELECT xmlroot($1,VERSION 42,STANDALONE YES)`,XMLRoot,2,2},
  {`SELECT xmlroot($1,VERSION '1.1',STANDALONE NO)`,XMLRoot,2,3},
 }{
  statements,err:=Parse(tc.q);if err!=nil{t.Fatal(tc.q,err)};raw,ok:=statements[0].(*Select).Targets[0].(XMLConstructExpr);if !ok||raw.Kind!=tc.kind||len(raw.Children())!=tc.count||raw.Standalone!=tc.standalone||raw.Pos.Character!=8{t.Fatal(tc.q,raw)}
 }
 for _,q:=range []string{`SELECT name,version,standalone FROM t`,`SELECT xmlforest((s.a)) FROM t s`,`SELECT xmltext('a')`,`SELECT pg_catalog.xmlcomment('a')`,`SELECT pg_catalog.xmlconcat('<a/>')`}{if _,err:=Parse(q);err!=nil{t.Fatal(q,err)}}
 for _,q:=range []string{`SELECT xmlconcat()`,`SELECT xmlforest()`,`SELECT xmlelement(NAME r,XMLATTRIBUTES())`,`SELECT xmlelement(NAME r,'x',XMLATTRIBUTES(1 AS a))`,`SELECT xmlforest(1 a)`,`SELECT xmlpi(NAME r,1,2)`,`SELECT xmlroot('<a/>')`,`SELECT xmlroot('<a/>',VERSION '1.0',STANDALONE)`,`SELECT xmlroot('<a/>',VERSION '1.0') suffix junk`}{if _,err:=Parse(q);err==nil{t.Fatal("invalid full input",q)}}
}

func TestXMLProduceTraversal(t *testing.T){
 q:=`SELECT xmlelement(NAME r,XMLATTRIBUTES($1 AS a),(SELECT x FROM xml_child),xmlroot($2,VERSION $3))`
 statements,err:=Parse(q);if err!=nil{t.Fatal(err)}
 var parameters []int
 if err:=VisitQueryExpressions(statements[0],func(raw Expr)error{if p,ok:=raw.(ParameterExpr);ok{parameters=append(parameters,p.Number)};return nil});err!=nil||!reflect.DeepEqual(parameters,[]int{1,2,3}){t.Fatal(parameters,err)}
 tables,queries:=0,0
 if err:=VisitQuerySources(statements[0],func(*Select)error{queries++;return nil},func(*TableReference,*Select)error{tables++;return nil});err!=nil||tables!=1||queries!=2{t.Fatal(tables,queries,err)}
 pos:=Position{Offset:17,Character:18};hidden:=FunctionExpr{Name:ObjectName{Parts:[]Identifier{{Text:"pg_index_has_property",Pos:pos}}},Pos:pos}
 for _,raw:=range []XMLConstructExpr{{Kind:XMLRoot,Args:[]Expr{LiteralExpr{Value:Value{Kind:NullValue}}},Version:hidden},{Kind:XMLElement,Named:[]XMLNamedOperand{{Expr:hidden,Name:new(Identifier{Text:"a"})}}},{Kind:XMLConcat,Args:[]Expr{hidden}}}{if got,found:=firstDefaultIndexProperty(raw);!found||got!=pos{t.Fatal(got,found)}}
}

func TestXMLProduceLexicalOwnership(t *testing.T){
 for _,name:=range []string{"content","document","name","version","standalone","preserve","strip","whitespace","indent"}{
  for _,tc:=range []struct{head,tail string;kind XMLConstructKind}{
   {"xmlforest(",")",XMLForest},{"xmlconcat(",")",XMLConcat},{"xmlroot(",",VERSION '1.0')",XMLRoot},
   {"xmlelement(NAME r,",")",XMLElement},{"xmlpi(NAME p,",")",XMLPI},
  }{
   for _,operand:=range []string{name,"s."+name,"("+name+")",`"`+name+`"`}{
    q:="SELECT "+tc.head+operand+tc.tail
    statements,err:=Parse(q);if err!=nil{t.Fatal(q,err)}
    raw:=statements[0].(*Select).Targets[0].(XMLConstructExpr)
    child:=raw.Children()[0];column,ok:=child.(ColumnExpr)
    if raw.Kind!=tc.kind||!ok||column.Name.Parts[len(column.Name.Parts)-1].Text!=name{t.Fatal(q,raw,child)}
    expectedPosition:=strings.Index(q,operand)+1;if strings.HasPrefix(operand,"("){expectedPosition++}
    if column.SourcePosition().Character!=expectedPosition{t.Fatal("column position",q,column)}
   }
  }
 }
 for _,q:=range []string{
  `SELECT xmlforest(xmlparse(CONTENT content PRESERVE WHITESPACE) AS preserve),content`,
  `SELECT xmlroot(xmlconcat(document,xmlparse(CONTENT content STRIP WHITESPACE)),VERSION version),document`,
  `SELECT xmlserialize(CONTENT xmlconcat(document) AS text)`,
  `SELECT xmlparse(CONTENT xmlserialize(CONTENT xmlroot(document,VERSION '1.0') AS text) PRESERVE WHITESPACE)`,
  `SELECT x.v FROM t CROSS JOIN LATERAL XMLTABLE('/r' PASSING xmlelement(NAME r,XMLATTRIBUTES('v' AS content),xmlforest(content)) COLUMNS v text PATH '@content')x`,
  `SELECT xmlpi(NAME content,document),xmlforest(strip AS whitespace);SELECT content,document`,
 }{if _,err:=Parse(q);err!=nil{t.Fatal(q,err)}}
 for _,state:=range []xmlLexical{{Parse:true},{Serialize:true},{Exists:true},{Table:true},{Construct:"xmlconcat"},{Construct:"xmlelement"},{Construct:"xmlforest"},{Construct:"xmlpi"},{Construct:"xmlroot"},{}}{
  for _,word:=range []string{"content","document"}{
   l:=lexer{input:word,previousToken:int('('),xmlDepths:[]xmlLexical{state}};var value sqlSymType
   code:=l.Lex(&value);if (code==XML_MODE)!=(state.Parse||state.Serialize){t.Fatal("mode ownership",state,word,code)}
  }
  for _,word:=range []string{"preserve","strip"}{
   l:=lexer{input:word+" whitespace",previousToken:int('('),xmlDepths:[]xmlLexical{state}};var value sqlSymType
   first:=l.Lex(&value);second:=l.Lex(&value)
   if (first==XML_SPACE)!=state.Parse||(second==XML_WHITESPACE)!=state.Parse{t.Fatal("whitespace ownership",state,word,first,second)}
  }
  state.Target=true;l:=lexer{input:"indent",xmlDepths:[]xmlLexical{state}};var value sqlSymType
  if code:=l.Lex(&value);(code==XML_INDENT)!=state.Serialize{t.Fatal("indent ownership",state,code)}
 }
 if _,err:=Parse(`SELECT content,document,preserve,strip,whitespace,indent`);err!=nil{t.Fatal("separate parse",err)}
}


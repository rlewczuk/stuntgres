package parser

import (
 "reflect"
 "testing"
)

func TestXMLTableParser(t *testing.T) {
 statements,err:=Parse(`/*é🙂*/ SELECT * FROM LATERAL XMLTABLE(XMLNAMESPACES($1::text AS "P"), ($2::text) PASSING BY REF ($3::xml) BY VALUE COLUMNS n FOR ORDINALITY,v varchar(3) DEFAULT $4::text NOT NULL PATH $5::text,w int PATH $6::text DEFAULT $7::int) AS x`)
 if err!=nil {t.Fatal(err)}
 table:=statements[0].(*Select).From.(*AliasReference).Input.(*XMLTable)
 if !table.Lateral || len(table.Namespaces)!=1 || table.Namespaces[0].Name.Text!="P" || len(table.Columns)!=3 || table.Pos.Character!=30 {t.Fatal(table)}
 if table.Columns[1].Options[0].Kind!="default" || table.Columns[1].Options[2].Kind!="path" {t.Fatal(table.Columns[1])}
 children:=table.Expressions()
 if len(children)!=7 {t.Fatal(len(children))}
 var parameters []int
 if err:=VisitQueryExpressions(statements[0],func(raw Expr)error{if parameter,ok:=raw.(ParameterExpr);ok {parameters=append(parameters,parameter.Number)};return nil});err!=nil {t.Fatal(err)}
 if !reflect.DeepEqual(parameters,[]int{1,2,3,4,5,6,7}) {t.Fatal(parameters)}
 next:=table.WithExpressions(children)
 next.Namespaces[0].Name.Text="changed";next.Columns[1].Options[0].Kind="changed"
 if table.Namespaces[0].Name.Text!="P" || table.Columns[1].Options[0].Kind!="default" {t.Fatal("replacement aliases original syntax")}
 for _,query:=range []string{
  `SELECT * FROM XMLTABLE('/r'::text PASSING '<r/>' COLUMNS v int)`,
  `SELECT * FROM XMLTABLE('/' || 'r' PASSING '<r/>' COLUMNS v int)`,
  `SELECT * FROM XMLTABLE('/r' PASSING '<r/>'::xml COLUMNS v int)`,
  `SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS(v int))`,
  `SELECT * FROM XMLTABLE('/r' PASSING '<r/>')`,
  `SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS v int); garbage`,
  `SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS v int DEFAULT true AND false)`,
 } {if _,err:=Parse(query);err==nil {t.Fatal("invalid source admitted",query)}}
 for _,query:=range []string{
  `SELECT * FROM XMLTABLE(('/' || 'r') PASSING ('<r/>'::xml) COLUMNS v int PATH ('v' || '') DEFAULT (1+2))`,
  `SELECT * FROM XMLTABLE(XMLNAMESPACES('urn:r' AS p), '/p:r' PASSING '<r/>' COLUMNS v public.my_domain[] DEFAULT NULL PATH 'v')`,
  `SELECT * FROM XMLTABLE('/r' PASSING '<r/>' COLUMNS path text,columns int,passing int)`,
  `SELECT document,passing,value,ref,columns,path,xmlnamespaces FROM t`,
  `SELECT * FROM XMLTABLE('/r' PASSING columns COLUMNS v int)`,
  `SELECT * FROM XMLTABLE('/r' PASSING BY REF columns BY VALUE COLUMNS v int)`,
  `SELECT * FROM XMLTABLE('/r' PASSING BY VALUE columns BY REF COLUMNS v int)`,
  `SELECT XMLEXISTS('/r' PASSING BY VALUE ('<r/>'::xml) BY REF)`,
 } {if _,err:=Parse(query);err!=nil {t.Fatal(query,err)}}
}

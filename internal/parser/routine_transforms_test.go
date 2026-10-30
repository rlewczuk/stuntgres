package parser

import (
 "strings"
 "testing"
 "unicode/utf8"
)

func TestRoutineTransformSyntax(t *testing.T) {
 for _,kind:=range []string{"FUNCTION f(v hstore) RETURNS hstore","PROCEDURE f(INOUT v hstore)"} {
  for _,options:=range []string{
   "TRANSFORM FOR TYPE public.\"hstore\", FOR TYPE hstore[] LANGUAGE go_host AS 'program'",
   "AS 'program' TRANSFORM FOR TYPE public.\"hstore\", FOR TYPE hstore[] LANGUAGE go_host",
   "LANGUAGE go_host AS 'program' TRANSFORM FOR TYPE public.\"hstore\", FOR TYPE hstore[]",
  } {
   q:="/*é*/ CREATE OR REPLACE "+kind+" "+options
   statements,err:=Parse(q);if err!=nil{t.Fatal(q,err)}
   d:=statements[0].(*CreateFunction)
   if !d.Replace||d.Procedure!=strings.HasPrefix(kind,"PROCEDURE")||d.Language.Text!="go_host"||d.Body!="program"{t.Fatal("declaration identity",d)}
   if len(d.Attributes)!=1{t.Fatal("CREATE attribute count",d.Attributes)}
   a:=d.Attributes[0]
   offset:=strings.Index(q,"TRANSFORM")
   if a.Kind!="transform"||a.Pos!=(Position{Offset:offset,Character:utf8.RuneCountInString(q[:offset])+1})||a.Transform==nil||len(a.Transform.Types)!=2{t.Fatal("ordered positioned transform clause",a)}
   types:=a.Transform.Types
   if types[0].Schema==nil||types[0].Schema.Text!="public"||types[0].Name!="hstore"||!types[0].Quoted||types[1].Name!="hstore"{t.Fatal("qualified and array type syntax",types)}
   for _,typ:=range types {if typ.Pos.Offset<=offset||typ.Pos.Character<=a.Pos.Character{t.Fatal("missing individual type position",typ)}}
  }
 }
 q:="CREATE FUNCTION f(v hstore) RETURNS hstore LANGUAGE go_host TRANSFORM FOR TYPE hstore TRANSFORM FOR TYPE hstore AS 'program'"
 statements,err:=Parse(q);if err!=nil{t.Fatal("duplicate clauses belong to admission",err)}
 attrs:=statements[0].(*CreateFunction).Attributes
 if len(attrs)!=2||attrs[1].Pos.Offset!=strings.LastIndex(q,"TRANSFORM"){t.Fatal("duplicate clause order/position",attrs)}
 // Whole-input syntax, and especially ALTER, must not inherit CREATE-only options.
 for _,q:=range []string{
  "ALTER FUNCTION f(hstore) TRANSFORM FOR TYPE hstore",
  "ALTER PROCEDURE f(hstore) TRANSFORM FOR TYPE hstore",
  "CREATE FUNCTION f(v hstore) RETURNS hstore LANGUAGE go_host TRANSFORM AS 'program'",
  "CREATE FUNCTION f(v hstore) RETURNS hstore LANGUAGE go_host TRANSFORM FOR TYPE hstore, AS 'program'",
  "CREATE FUNCTION f(v hstore) RETURNS hstore LANGUAGE go_host TRANSFORM FOR TYPE hstore AS 'program' trailing",
 }{if statements,err:=Parse(q);err==nil||len(statements)!=0{t.Fatal("accepted invalid declaration",q,statements,err)}}
}

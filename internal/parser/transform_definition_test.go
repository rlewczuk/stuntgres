package parser

import (
 "strings"
 "testing"
 "unicode/utf8"
)

func TestTransformDefinitionSyntax(t *testing.T) {
 q:=`/*é*/ CREATE OR REPLACE TRANSFORM FOR "Type Schema"."Type Name" LANGUAGE "Language Name" (TO SQL WITH FUNCTION "Callback Schema"."To Name"(pg_catalog.internal), FROM SQL WITH FUNCTION "From Name")`
 statements,err:=Parse(q)
 if err!=nil || len(statements)!=1 { t.Fatal(statements,err) }
 s,ok:=statements[0].(*CreateTransform)
 if !ok || !s.Replace || s.Type.Name!="Type Name" || s.Type.Schema==nil || s.Type.Schema.Text!="Type Schema" || s.Language.Text!="Language Name" || s.From==nil || s.To==nil { t.Fatalf("transform AST %#v",statements[0]) }
 if s.From.SignatureSpecified || !s.To.SignatureSpecified || len(s.To.Arguments)!=1 || s.To.Arguments[0].Type.Name!="internal" { t.Fatalf("callback identity distinction %#v %#v",s.From,s.To) }
 if len(s.To.Name.Parts)!=2 || s.To.Name.Parts[0].Text!="Callback Schema" || s.To.Name.Parts[1].Text!="To Name" || s.From.Name.Parts[0].Text!="From Name" { t.Fatal("callback names") }
 for _,p:=range []Position{s.Pos,s.Language.Pos,s.FromPos,s.ToPos,s.To.Name.Parts[0].Pos,s.From.Name.Parts[0].Pos} {
  if p.Offset<0 || p.Character!=utf8.RuneCountInString(q[:p.Offset])+1 { t.Fatalf("origin %#v",p) }
 }
 if s.FromPos.Offset!=strings.LastIndex(q,"FROM") || s.ToPos.Offset!=strings.Index(q,"TO SQL") { t.Fatal("direction origins") }
 for _,source:=range []string{
  "CREATE TRANSFORM FOR hstore LANGUAGE l (FROM SQL WITH FUNCTION f(internal))",
  "CREATE TRANSFORM FOR hstore LANGUAGE l (TO SQL WITH FUNCTION t)",
  "DROP TRANSFORM IF EXISTS FOR hstore LANGUAGE l CASCADE",
  "DROP TRANSFORM FOR hstore LANGUAGE l RESTRICT",
 } {
  parsed,err:=Parse(source); if err!=nil || len(parsed)!=1 { t.Fatal(source,parsed,err) }
  if drop,ok:=parsed[0].(*DropTransform); ok {
   if drop.Type.Name!="hstore" || drop.Language.Text!="l" || drop.IfExists!=strings.Contains(source,"IF EXISTS") || (drop.Mode==DropCascade)!=strings.Contains(source,"CASCADE") { t.Fatal(drop) }
  }
 }
 for _,source:=range []string{
  "CREATE TRANSFORM FOR hstore LANGUAGE l ()",
  "CREATE TRANSFORM FOR hstore LANGUAGE l (FROM SQL WITH FUNCTION f, FROM SQL WITH FUNCTION f)",
  "CREATE TRANSFORM FOR hstore LANGUAGE l (TO SQL WITH FUNCTION t, TO SQL WITH FUNCTION t)",
  "CREATE TRANSFORM FOR hstore LANGUAGE l (FROM SQL WITH FUNCTION f, TO SQL WITH FUNCTION t, FROM SQL WITH FUNCTION f)",
  "CREATE TRANSFORM FOR hstore LANGUAGE l (FROM SQL WITH FUNCTION f) trailing",
  "DROP TRANSFORM FOR hstore LANGUAGE l trailing",
 } { if _,err:=Parse(source); err==nil { t.Fatalf("accepted trailing/redundant syntax %q",source) } }
 // A later parse may not retain direction options or mutate accepted backing.
 parsed,err:=Parse("CREATE TRANSFORM FOR hstore LANGUAGE l (TO SQL WITH FUNCTION t)")
 if err!=nil { t.Fatal(err) }
 later:=parsed[0].(*CreateTransform)
 if later.From!=nil || later.To==nil || later.Replace || later.FromPos!=(Position{}) { t.Fatal("per-call state leaked",later) }
 later.To.Name.Parts[0].Text="changed"
 if s.To.Name.Parts[1].Text!="To Name" || s.From.Name.Parts[0].Text!="From Name" { t.Fatal("parser calls share callback backing") }
}

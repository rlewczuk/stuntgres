package parser

import (
 "strings"
 "testing"
)

func TestSecurityLabelParser(t *testing.T) {
 for _, target := range []string{"ROLE", "DATABASE", "TABLESPACE", "SUBSCRIPTION", "TABLE"} {
  for _, tc := range []struct{ clause, provider string; present bool }{
   {"", "", false}, {" FOR MiSsInG", "missing", true}, {" FOR \"MiSsInG\"", "MiSsInG", true},
   {" FOR 'MiSsInG'", "MiSsInG", true}, {" FOR ''", "", true}, {" FOR 'quo''te'", "quo'te", true},
   {` FOR "quo""te"`, `quo"te`, true}, {` FOR E'back\\slash'`, `back\slash`, true},
  } {
   for _, label := range []string{"'value'", "NULL"} {
    q := "SECURITY LABEL"+tc.clause+" ON "+target+" target IS "+label
    stmts, err := Parse(q)
    if err != nil || len(stmts)!=1 { t.Fatal(q, stmts, err) }
    s, ok := stmts[0].(*SecurityLabel)
    if !ok || s.Kind!=strings.ToLower(target) || len(s.Name.Parts)!=1 || s.Name.Parts[0].Text!="target" || (s.Provider!=nil)!=tc.present || s.Provider!=nil && s.Provider.Text!=tc.provider || (s.Label.Kind==NullValue)!=(label=="NULL") || s.Label.Kind!=NullValue && (s.Label.Kind!=StringValue || s.Label.Text!="value") || s.Pos.Character!=1 { t.Fatal(q,s) }
   }
  }
 }
 for _, q := range []string{"SECURITY LABEL ON ROLE r IS 'x' trailing", "SECURITY LABEL FOR ON ROLE r IS 'x'", "SECURITY LABEL ON TABLE r IS $1", "SECURITY LABEL ON ROLE r IS $1", "SECURITY LABEL ON ROLE r IS", "SECURITY other ON ROLE r IS NULL"} {
  if _, err := Parse(q); err==nil { t.Fatal("accepted malformed syntax",q) }
 }
 for _, q := range []string{"CREATE TABLE label(label text)", "CREATE FUNCTION label() RETURNS int LANGUAGE SQL SECURITY DEFINER AS 'SELECT 1'", "ALTER TABLE label ENABLE ROW LEVEL SECURITY"} {
  if _, err := Parse(q); err!=nil { t.Fatal("existing security/label syntax",q,err) }
 }
}

func TestSecurityLabelEmbeddedParser(t *testing.T) {
 provider,labelText:="MiSsInG","label"
 for _,tc:=range []struct{
  command string
  provider,label *string
 }{
  {command:`SECURITY LABEL ON ROLE missing IS NULL`},
  {command:`SECURITY LABEL FOR "MiSsInG" ON ROLE missing IS 'label'`,provider:&provider,label:&labelText},
 } {
  source:="DECLARE security int; BEGIN security := 1; "+tc.command+"; security := 2; END"
  block,err:=ParsePLpgSQL(source,PLReturnPolicy{})
  if err!=nil || block==nil{t.Fatal(source,err)}
  if len(block.Declarations)!=1 || block.Declarations[0].Name.Text!="security" || len(block.Statements)!=3{t.Fatal("procedural declarations/statements",block)}
  command:=block.Statements[1]
  raw:=command.SQL
  if raw==nil || !ValidPLSQLStatement(raw) || raw.Kind!=PLCommandSQL || raw.Text!=tc.command || command.Target!=nil || command.Expression!=nil || command.Into!=nil{t.Fatal("security label not captured as ordinary utility",command)}
  label,ok:=raw.Query.(*SecurityLabel)
  if !ok || label.Kind!="role" || len(label.Name.Parts)!=1 || label.Name.Parts[0].Text!="missing" || (label.Provider==nil)!=(tc.provider==nil) || label.Provider!=nil && label.Provider.Text!=*tc.provider || (label.Label.Kind==NullValue)!=(tc.label==nil) || tc.label!=nil && (label.Label.Kind!=StringValue || label.Label.Text!=*tc.label){t.Fatal("embedded security label fields",raw.Query)}
  for _,index:=range []int{0,2}{
   assignment:=block.Statements[index]
   target,ok:=assignment.Target.(ColumnExpr)
   if !ok || len(target.Name.Parts)!=1 || target.Name.Parts[0].Text!="security" || assignment.SQL!=nil || assignment.Into!=nil{t.Fatal("security assignment captured as utility",assignment)}
   literal,ok:=assignment.Expression.(LiteralExpr)
   want:=int64(1);text:="1";if index==2{want,text=2,"2"}
   if !ok || literal.Value.Kind!=IntegerValue || literal.Value.Integer!=want || PLExpressionSource(source,assignment.ExpressionStart,assignment.ExpressionEnd)!=text{t.Fatal("security assignment literal changed",assignment)}
  }
 }
}

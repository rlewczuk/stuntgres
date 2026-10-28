package reference_test

import (
 "database/sql"
 "reflect"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

type ltreeArrayCase struct { name, op, leftType, rightType, left, right, missLeft, missRight string; want any }
var ltreeArrayCases = []ltreeArrayCase{
 {"_ltree_isparent","@>","ltree[]","ltree","{Other,Top.Science}","Top.Science.Astro","{Other}","Top.Science.Astro",true},
 {"_ltree_r_isparent","<@","ltree","ltree[]","Top.Science.Astro","{Other,Top.Science}","Top.Science.Astro","{Other}",true},
 {"_ltree_risparent","<@","ltree[]","ltree","{Other,Top.Science.Astro}","Top.Science","{Other}","Top.Science",true},
 {"_ltree_r_risparent","@>","ltree","ltree[]","Top.Science","{Other,Top.Science.Astro}","Top.Science","{Other}",true},
 {"_ltq_regex","~","ltree[]","lquery","{a.b}","*.b","{a.b}","a",true},
 {"_ltq_rregex","~","lquery","ltree[]","*.b","{a.b}","a","{a.b}",true},
 {"lt_q_regex","?","ltree","lquery[]","Top.Astro","{Other,Top.*}","Top.Astro","{Other}",true},
 {"lt_q_rregex","?","lquery[]","ltree","{Other,Top.*}","Top.Astro","{Other}","Top.Astro",true},
 {"_lt_q_regex","?","ltree[]","lquery[]","{Other,Top.Astro}","{Nope,*.Astro}","{Other}","{Nope}",true},
 {"_lt_q_rregex","?","lquery[]","ltree[]","{Nope,*.Astro}","{Other,Top.Astro}","{Nope}","{Other}",true},
 {"_ltxtq_exec","@","ltree[]","ltxtquery","{Europe.Top}","Top & Europe","{Top,Europe}","Top & Europe",true},
 {"_ltxtq_rexec","@","ltxtquery","ltree[]","Top & Europe","{Europe.Top}","Top & Europe","{Top,Europe}",true},
 {"_ltree_extract_isparent","?@>","ltree[]","ltree","[7:8]={Top.Science,Top}","Top.Science.Astro","{}","Top","Top.Science"},
 {"_ltree_extract_risparent","?<@","ltree[]","ltree","[0:1]={Top.Z,Top.A}","Top","{}","Top","Top.Z"},
}
func ltreeArrayLiteral(text, typ string) string { return "'"+strings.ReplaceAll(text,"'","''")+"'::"+typ }
func (tc ltreeArrayCase) expression(left, right string, direct bool) string {
 if direct { return tc.name+"("+left+","+right+")" }; return left+" "+tc.op+" "+right
}
func ltreeArraySelect(b fixture.Backend, query string, columns, families []string, rows [][]any, args ...any) fixture.Step {
 types := append([]string(nil), families...)
 if b.PG { for i, family := range types { if family != "BOOL" && family != "INT4" && family != "TEXT" && family != "NAME" { types[i] = "" } } }
 s := fixture.Select(query, columns, types, rows, args...)
 s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
  if value == nil { return true }
  switch families[col] {
  case "BOOL": if _, ok := value.(bool); !ok { t.Fatalf("BOOL raw %T",value) }; return true
  case "LTREE", "_LTREE", "_LQUERY":
   if b.PG { if _, ok := value.([]byte); !ok { t.Fatalf("pq custom raw %T",value) } } else if _, ok := value.(string); !ok { t.Fatalf("native custom raw %T",value) }; return true
  }
  return false
 }
 return s
}
func ltreeArrayResult(b fixture.Backend, query string, value any, args ...any) fixture.Step {
 family := "BOOL"; if _, ok := value.(string); ok { family = "LTREE" }
 return ltreeArraySelect(b,query,[]string{"v"},[]string{family},[][]any{{value}},args...)
}
func ltreeArrayMetadata(t *testing.T, b fixture.Backend, rows *sql.Rows, families []string, nullable []bool) []*sql.ColumnType {
 t.Helper(); columns, err := rows.ColumnTypes(); if err != nil { t.Fatal(err) }
 ltreeArrayColumnMetadata(t,b,columns,families,nullable)
 return columns
}
func ltreeArrayColumnMetadata(t *testing.T,b fixture.Backend,columns []*sql.ColumnType,families []string,nullable []bool) {
 t.Helper()
 if len(columns)!=len(families) || nullable!=nil && len(nullable)!=len(families) {t.Fatal("metadata expectation count")}
 for i, col := range columns {
  name, scan := families[i], reflect.TypeFor[string]()
  if name == "BOOL" { scan = reflect.TypeFor[bool]() } else if b.PG { name,scan = "",reflect.TypeFor[any]() }
  if col.DatabaseTypeName()!=name || col.ScanType()!=scan { t.Fatalf("column %d type %q/%v want %q/%v",i,col.DatabaseTypeName(),col.ScanType(),name,scan) }
  if n,ok:=col.Length(); n!=0 || ok { t.Fatal("length",n,ok) }; if p,s,ok:=col.DecimalSize(); p!=0 || s!=0 || ok { t.Fatal("decimal",p,s,ok) }
  wantKnown:=!b.PG && nullable!=nil
  wantNullable:=wantKnown && nullable[i]
  if value,known:=col.Nullable(); value!=wantNullable || known!=wantKnown {t.Fatalf("column %d Nullable=%v/%v want %v/%v",i,value,known,wantNullable,wantKnown)}
 }
}

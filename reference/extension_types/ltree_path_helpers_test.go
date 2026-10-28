package reference_test

import (
 "database/sql"
 "fmt"
 "math"
 "reflect"
 "testing"
 "stuntgres/reference/fixture"
)

type ltreePathCase struct { query, family string; args []any; want any }
func ltreePathResult(b fixture.Backend, query,family string,want any,args ...any) fixture.Step {
 return ltreeArraySelect(b,query,[]string{"v"},[]string{family},[][]any{{want}},args...)
}
func ltreePathMatrix(t *testing.T,cases []ltreePathCase) {
 t.Helper()
 ltreeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  ltreeInstall(t,p,b,c)
  for index,tc := range cases { t.Run(fmt.Sprintf("case_%03d",index),func(t *testing.T) {
   s := ltreePathResult(b,tc.query,tc.family,tc.want,tc.args...)
   fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
   fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))
   stmt,err := c.PrepareContext(p.Ctx,tc.query); if err != nil { t.Fatal(err) }
   defer stmt.Close()
   held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:tc.query}
   fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
  }) }
 })
}
func ltreePathColumnMetadata(t *testing.T,b fixture.Backend,columns []*sql.ColumnType,names,families []string,nullable []bool) {
 t.Helper()
 if len(columns) != len(families) { t.Fatal("metadata width",len(columns),len(families)) }
 for i,col := range columns {
  name,scan := families[i],reflect.TypeFor[string]()
  switch name {
  case "INT4": scan = reflect.TypeFor[int64](); if b.PG { scan = reflect.TypeFor[int32]() }
  case "LTREE": if b.PG { name,scan = "",reflect.TypeFor[any]() }
  }
  if col.Name() != names[i] || col.DatabaseTypeName() != name || col.ScanType() != scan { t.Fatalf("column %d = %s/%s/%v want %s/%s/%v",i,col.Name(),col.DatabaseTypeName(),col.ScanType(),names[i],name,scan) }
  known := !b.PG && (nullable != nil || names[i] == "id"); value := known && (nullable == nil || nullable[i])
  if n,k := col.Nullable(); n != value || k != known { t.Fatalf("nullability %v/%v want %v/%v",n,k,value,known) }
  length,lengthKnown:=int64(0),false
  if families[i]=="TEXT"{length,lengthKnown=math.MaxInt64,true}
  if n,k := col.Length(); n != length || k != lengthKnown { t.Fatal("length",n,k,"want",length,lengthKnown) }
  if p,s,k := col.DecimalSize(); p != 0 || s != 0 || k { t.Fatal("decimal",p,s,k) }
 }
}

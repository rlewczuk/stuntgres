package reference_test

import (
 "strconv"
 "strings"
 "stuntgres/reference/fixture"
 "testing"
 "unicode/utf8"
)

func timestampRangeStagedFailures() []rangeStagedFailure {
 var failures []rangeStagedFailure
 for _, tc := range timestampRangeFailures() {
  script := "CREATE TABLE staged(v tsrange); INSERT INTO staged VALUES ('[epoch,epoch]'); INSERT INTO staged VALUES ("+rangeLiteral(tc.input)+");"
  want := fixture.ControlsReferenceDiagnostic(tc.code,tc.message)
  want.Detail,want.Hint = tc.detail,tc.hint
  want.Position = strconv.Itoa(utf8.RuneCountInString(script[:strings.LastIndex(script,"('")+1])+1)
  failures = append(failures,rangeStagedFailure{script,want})
 }
 return failures
}

func TestReferenceTimestampRangeControls(t *testing.T) {
 p := fixture.NewScenario(t)
 for _, b := range p.Backends() {
  run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
  run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,v tsrange)",0))
  run(fixture.Exec("CREATE TABLE vacant(v tsrange NOT NULL)",0))
  run(fixture.Exec("INSERT INTO r VALUES (1,'(2000-01-01 00:00:00,2000-01-01 00:00:00.000001)'),(2,'[epoch,epoch]'),(3,'empty'),(4,'[,]'),(5,'[-infinity,infinity]'),(6,'[epoch,infinity)'),(7,'[epoch,infinity]'),(8,'[epoch,)'),(9,'[0001-12-31 23:59:59 BC,0001-01-01)'),(10,'(4714-11-24 00:00:00 BC,294276-12-31 23:59:59.999999]'),(11,NULL)",11))
  want := [][]any{
   {int64(1),`("2000-01-01 00:00:00","2000-01-01 00:00:00.000001")`},
   {int64(2),`["1970-01-01 00:00:00","1970-01-01 00:00:00"]`},
   {int64(3),"empty"},{int64(4),"(,)"},{int64(5),"[-infinity,infinity]"},
   {int64(6),`["1970-01-01 00:00:00",infinity)`},{int64(7),`["1970-01-01 00:00:00",infinity]`},{int64(8),`["1970-01-01 00:00:00",)`},
   {int64(9),`["0001-12-31 23:59:59 BC","0001-01-01 00:00:00")`},
   {int64(10),`("4714-11-24 00:00:00 BC","294276-12-31 23:59:59.999999"]`},{int64(11),nil},
  }
  checks := []fixture.Step{
   rangeSelect(b,"SELECT k,v FROM r ORDER BY k",[]string{"k","v"},[]string{"INT4","TSRANGE"},want,[]bool{false,true}),
   rangeSelect(b,"SELECT v FROM vacant",[]string{"v"},[]string{"TSRANGE"},[][]any{},[]bool{false}),
  }
  for _, s := range checks { run(s); run(fixture.Prepared(s)) }
  rangeControlCycle(t,p,b,checks,[][]bool{{false,true},{false}},timestampRangeStagedFailures())
 }
}

func TestReferenceTimestampRangeFeatureSmoke(t *testing.T) {
 p := fixture.NewScenario(t)
 for _, b := range p.Backends() {
  run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
  run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,v tsrange)",0))
  run(fixture.Exec("CREATE TABLE vacant(v tsrange)",0))
  q := "INSERT INTO r VALUES ($1,$2)"
  stmt,err := b.DB.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
  held := fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q}
  inputs := []any{"(2000-01-01 00:00:00,2000-01-01 00:00:00.000001)","[-infinity,infinity]","[,]","empty",nil,[]byte(nil),[]byte("[epoch,epoch]")}
  want := [][]any{{int64(1),`("2000-01-01 00:00:00","2000-01-01 00:00:00.000001")`},{int64(2),"[-infinity,infinity]"},{int64(3),"(,)"},{int64(4),"empty"},{int64(5),nil},{int64(6),nil},{int64(7),`["1970-01-01 00:00:00","1970-01-01 00:00:00"]`}}
  for i,input := range inputs { fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Exec(q,1,i+1,input)) }
  for _, tc := range []timestampRangeFailure{
   {input:"[bad,epoch)",code:"22007",message:`invalid input syntax for type timestamp: "bad"`},
   {input:"[infinity,-infinity]",code:"22000",message:"range lower bound must be less than or equal to range upper bound"},
  } {
   d := fixture.ControlsReferenceDiagnostic(tc.code,tc.message)
   d.Context = `unnamed portal parameter $2 = '...'`
   s := fixture.ErrorDiagnostic("INSERT INTO r VALUES (0,$1),(99,$2)",d,"[epoch,epoch]",tc.input)
   s.ErrorStage = "execute"; run(s); run(fixture.Prepared(s))
  }
  checks := []fixture.Step{
   rangeSelect(b,"SELECT k,v FROM r ORDER BY k",[]string{"k","v"},[]string{"INT4","TSRANGE"},want,[]bool{false,true}),
   rangeSelect(b,"SELECT v FROM vacant",[]string{"v"},[]string{"TSRANGE"},[][]any{},[]bool{true}),
  }
  for _, s := range checks { run(s); run(fixture.Prepared(s)) }
  if err := stmt.Close(); err != nil { t.Fatal(err) }
  rangeControlCycle(t,p,b,checks,[][]bool{{false,true},{true}},timestampRangeStagedFailures())
 }
}

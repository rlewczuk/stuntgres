package reference_test

import (
 "fmt"
 "stuntgres/reference/fixture"
 "testing"
)

// PG18.0 confirms continuous timestamp bounds have no successor canonicalizer.
func TestReferenceTimestampRangeValues(t *testing.T) {
 cases := []struct{ input, output string }{
  {"[2010-01-01 14:30,2010-01-01 15:30)", `["2010-01-01 14:30:00","2010-01-01 15:30:00")`},
  {"(2000-01-01,2000-01-02]", `("2000-01-01 00:00:00","2000-01-02 00:00:00"]`},
  {"(2000-01-01 00:00:00,2000-01-01 00:00:00.000001)", `("2000-01-01 00:00:00","2000-01-01 00:00:00.000001")`},
  {"[epoch,epoch]", `["1970-01-01 00:00:00","1970-01-01 00:00:00"]`},
  {"[epoch,epoch)", "empty"}, {"(epoch,epoch]", "empty"}, {"(epoch,epoch)", "empty"},
  {" \tEmPtY\n", "empty"}, {"[,]", "(,)"}, {"(,)", "(,)"}, {"[,)", "(,)"}, {"(,]", "(,)"},
  {"[,2000-01-01]", `(,"2000-01-01 00:00:00"]`},
  {"[2000-01-01,]", `["2000-01-01 00:00:00",)`},
  {"[2004-10-19T10:23:54+02,2004-10-19T11:23:54-08)", `["2004-10-19 10:23:54","2004-10-19 11:23:54")`},
  {"[2000-01-01 00:00:00.1234567,2000-01-01 00:00:01.120000]", `["2000-01-01 00:00:00.123457","2000-01-01 00:00:01.12"]`},
  {"[-infinity,infinity]", "[-infinity,infinity]"}, {"(-infinity,infinity)", "(-infinity,infinity)"},
  {"[2000-01-01,infinity)", `["2000-01-01 00:00:00",infinity)`},
  {"[2000-01-01,infinity]", `["2000-01-01 00:00:00",infinity]`},
  {"[2000-01-01,)", `["2000-01-01 00:00:00",)`},
  {"[infinity,infinity]", "[infinity,infinity]"}, {"[-infinity,-infinity]", "[-infinity,-infinity]"},
  {"[infinity,infinity)", "empty"}, {"(infinity,)", "(infinity,)"}, {"(,-infinity)", "(,-infinity)"},
  {"[0001-12-31 23:59:59 BC,0001-01-01)", `["0001-12-31 23:59:59 BC","0001-01-01 00:00:00")`},
  {"(4714-11-24 00:00:00 BC,294276-12-31 23:59:59.999999]", `("4714-11-24 00:00:00 BC","294276-12-31 23:59:59.999999"]`},
  {`["2000-01-01","2000-01-02")`, `["2000-01-01 00:00:00","2000-01-02 00:00:00")`},
  {`[2000\-01-01,2000-01-02)`, `["2000-01-01 00:00:00","2000-01-02 00:00:00")`},
 }
 for i, tc := range cases {
  t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
   p := fixture.NewScenario(t)
   for _, b := range p.Backends() {
    run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
    run(fixture.Exec(`CREATE TABLE r(k integer NOT NULL,a tsrange,b pg_catalog.tsrange,c "tsrange")`, 0))
    run(fixture.Exec("INSERT INTO r VALUES (1,"+rangeLiteral(tc.input)+","+rangeLiteral(tc.input)+","+rangeLiteral(tc.input)+")", 1))
    run(fixture.Prepared(fixture.Exec("INSERT INTO r VALUES ($1,$2,$2,$2)", 1, 2, tc.input)))
    run(fixture.Exec("INSERT INTO r VALUES (3,NULL,NULL,NULL)", 1))
    for _, prepared := range []bool{false,true} {
     s := rangeSelect(b, "SELECT k,a,b,c FROM r ORDER BY k", []string{"k","a","b","c"}, []string{"INT4","TSRANGE","TSRANGE","TSRANGE"}, [][]any{{int64(1),tc.output,tc.output,tc.output},{int64(2),tc.output,tc.output,tc.output},{int64(3),nil,nil,nil}}, []bool{false,true,true,true})
     s.Prepared = prepared
     run(s)
    }
    run(fixture.Exec("CREATE TABLE numeric_guard(v numrange)", 0))
    run(fixture.Exec("INSERT INTO numeric_guard VALUES ('(4,5)')", 1))
    run(rangeSelect(b,"SELECT v FROM numeric_guard",[]string{"v"},[]string{"NUMRANGE"},[][]any{{"(4,5)"}},[]bool{true}))
   }
  })
 }
 // PG18.0 rejects fractional rounding to the exclusive external end for both
 // the scalar owner and the range endpoint with the same complete diagnostic.
 t.Run("upper_end_rounding", func(t *testing.T) {
  p := fixture.NewScenario(t)
  for _, b := range p.Backends() {
   conn, err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
   for _, typ := range []string{"timestamp","tsrange"} {
    fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Exec("CREATE TABLE boundary_"+typ+"(v "+typ+")",0))
    endpoint := "294276-12-31 23:59:59.9999995"
    input := endpoint; if typ == "tsrange" { input = "["+endpoint+",infinity)" }
    want := fixture.ControlsReferenceDiagnostic("22008", `timestamp out of range: "`+endpoint+`"`)
    q := "INSERT INTO boundary_"+typ+" VALUES ("+rangeLiteral(input)+")"
    want.Position = fixture.CompositeReferenceLiteralPosition(q)
    fixture.CompositeReferenceError(t,p,conn,q,"prepare",want)
   }
  }
 })
}

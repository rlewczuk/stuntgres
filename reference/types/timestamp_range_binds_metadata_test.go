package reference_test

import (
 "context"
 "database/sql"
 "errors"
 "stuntgres/reference/fixture"
 "testing"
)

func TestReferenceTimestampRangeBinds(t *testing.T) {
 p := fixture.NewScenario(t)
 for _, b := range p.Backends() {
  run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
  run(fixture.Exec("CREATE TABLE r(k integer NOT NULL,a tsrange,b tsrange)",0))
  q := "INSERT INTO r VALUES ($1,$2,$2)"
  stmt,err := b.DB.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
  held := fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:q}
  buf := []byte("[epoch,epoch]")
  var nilBytes []byte
  inputs := []any{"(2000-01-01,2000-01-02]",buf,nil,nilBytes,"empty","[,]","[-infinity,infinity]","[epoch,infinity)","[epoch,infinity]","[epoch,)"}
  outputs := []any{`("2000-01-01 00:00:00","2000-01-02 00:00:00"]`,`["1970-01-01 00:00:00","1970-01-01 00:00:00"]`,nil,nil,"empty","(,)","[-infinity,infinity]",`["1970-01-01 00:00:00",infinity)`,`["1970-01-01 00:00:00",infinity]`,`["1970-01-01 00:00:00",)`}
  var want [][]any
  for i,input := range inputs {
   fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Exec(q,1,i,input))
   want = append(want,[]any{int64(i),outputs[i],outputs[i]})
   if i == 1 { for j := range buf { buf[j] = 'x' } }
  }
  // The actual held statement, not a fresh Prepared wrapper, rejects a nonnil
  // empty byte input with parameter $2 context and leaves prior rows untouched.
  d := fixture.ControlsReferenceDiagnostic("22P02",`malformed range literal: ""`)
  d.Detail,d.Context = "Missing left parenthesis or bracket.",`unnamed portal parameter $2 = ''`
  s := fixture.ErrorDiagnostic(q,d,99,[]byte{})
  s.ErrorStage = "execute"
  fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
  conversion := errors.New("tsrange argument conversion failed")
  for _, execute := range []func()(sql.Result,error){
   func()(sql.Result,error){ return b.DB.ExecContext(p.Ctx,q,99,integerRangeFailingValuer{conversion}) },
   func()(sql.Result,error){ return stmt.ExecContext(p.Ctx,99,integerRangeFailingValuer{conversion}) },
  } {
   result,err := execute(); var state interface{ SQLState() string }
   if result != nil || !errors.Is(err,conversion) || errors.As(err,&state) { t.Fatal("Valuer crossed SQL boundary",result,err) }
  }
  ctx,cancel := context.WithCancel(p.Ctx); cancel()
  for _, execute := range []func()(sql.Result,error){
   func()(sql.Result,error){ return b.DB.ExecContext(ctx,q,99,"empty") },
   func()(sql.Result,error){ return stmt.ExecContext(ctx,99,"empty") },
  } { result,err := execute(); if result != nil || !errors.Is(err,context.Canceled) { t.Fatal("precanceled bind",result,err) } }
  for _, prepared := range []bool{false,true} {
   check := rangeSelect(b,"SELECT k,a,b FROM r ORDER BY k",[]string{"k","a","b"},[]string{"INT4","TSRANGE","TSRANGE"},want,[]bool{false,true,true})
   check.Prepared = prepared; run(check)
  }
 }
}

func TestReferenceTimestampRangeMetadata(t *testing.T) {
 p := fixture.NewScenario(t)
 for _, b := range p.Backends() {
  run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }
  for _, q := range []string{"CREATE DOMAIN span AS tsrange","CREATE DOMAIN chain AS span","CREATE TABLE r(k integer NOT NULL,a tsrange NOT NULL,b span,c chain)","CREATE TABLE vacant(a tsrange NOT NULL,b span,c chain)","CREATE TABLE nulls(a tsrange,b span,c chain)"} { run(fixture.Exec(q,0)) }
  run(fixture.Exec("INSERT INTO r VALUES (1,'[epoch,epoch]','empty','[,]')",1))
  run(fixture.Exec("INSERT INTO nulls VALUES (NULL,NULL,NULL)",1))
  checks := []fixture.Step{
   rangeSelect(b,"SELECT a,b,c FROM r",[]string{"a","b","c"},[]string{"TSRANGE","TSRANGE","TSRANGE"},[][]any{{`["1970-01-01 00:00:00","1970-01-01 00:00:00"]`,"empty","(,)"}},[]bool{false,true,true}),
   rangeSelect(b,"SELECT a,b,c FROM vacant",[]string{"a","b","c"},[]string{"TSRANGE","TSRANGE","TSRANGE"},[][]any{},[]bool{false,true,true}),
   rangeSelect(b,"SELECT a,b,c FROM nulls",[]string{"a","b","c"},[]string{"TSRANGE","TSRANGE","TSRANGE"},[][]any{{nil,nil,nil}},[]bool{true,true,true}),
  }
  for _, check := range checks {
   for _, prepared := range []bool{false,true} { s := check; s.Prepared = prepared; run(s) }
   rows,err := b.DB.QueryContext(p.Ctx,check.Query); if err != nil { t.Fatal(err) }
   rangeConsumeHeld(t,rows,check)
  }
 }
}

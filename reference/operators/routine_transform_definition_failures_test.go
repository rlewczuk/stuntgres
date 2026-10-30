package reference_test

import (
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionFailures(t *testing.T) {
 for _,mode:=range []string{"program","to"} {
  t.Run(mode,func(t *testing.T) {
   definitionScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
    definitionAnnotate(t,p,b,c)
    hostExec(t,p,b,c,definitionCreate("td_a_from","td_b_to",false))
    program,python:="selection-v1",transformsSelectionPython
    bad,nativeMessage:="a=>bad","invalid syntax"
    want:=fixture.ControlsReferenceDiagnostic("38000","ValueError: invalid literal for int() with base 10: 'bad'")
    want.Context="Traceback (most recent call last):\n  PL/Python function \"td_failing\", line 4, in <module>\n    v['a'] = str(int(v.get('a','0'))+1)\nPL/Python function \"td_failing\"\nPL/pgSQL function td_prefix(hstore) line 1 at RETURN"
    if mode=="to" {
     program="definition-bad-to"
     python="if 'bad' in v:\n    return 7\n"+hostIncrementPython
     bad,nativeMessage="bad=>1","host: result is not a nonnil map"
     want=fixture.ControlsReferenceDiagnostic("42809","not a Python mapping")
     want.Context="while creating return value\nPL/Python function \"td_failing\"\nPL/pgSQL function td_prefix(hstore) line 1 at RETURN"
    }
    definitionFunction(t,p,b,c,"td_failing",program,python)
    hostExec(t,p,b,c,"CREATE TABLE td_effects(v integer)")
    fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO td_effects VALUES (11)",1))
    hostExec(t,p,b,c,"CREATE FUNCTION td_prefix(v hstore) RETURNS hstore LANGUAGE plpgsql AS $$ BEGIN INSERT INTO td_effects VALUES (22); RETURN td_failing(v); END $$")
    q:="SELECT td_prefix($1::hstore)"
    held:=transformsHeld(t,p,c,q)
    for _,executor:=range []fixture.Executor{c,held} {
     rows,err:=executor.QueryContext(p.Ctx,q,bad); if rows!=nil { rows.Close() }
     if b.PG { fixture.AssertDiagnostic(t,err,&want) } else { definitionOrdinaryError(t,err,nativeMessage) }
     hostRows(t,p,b,c,"SELECT v FROM td_effects ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(11)}})
    }
    if !b.PG {
     for _,failure:=range []struct{input,message string}{{"from_error=>1","host: FROM SQL failed"},{"handler_error=>1","host: handler failed"},{"a=>1,to_error=>1","host: TO SQL failed"}} {
      rows,err:=held.QueryContext(p.Ctx,q,failure.input); if rows!=nil { rows.Close() }
      definitionOrdinaryError(t,err,failure.message)
      hostRows(t,p,b,c,"SELECT v FROM td_effects ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(11)}})
     }
    }
    rows,err:=held.QueryContext(p.Ctx,q,"a=>7,b=>NULL"); if err!=nil { t.Fatal(err) }
    if !rows.Next() { rows.Close(); t.Fatal("same held wrapper did not recover") }
    var value string; if err=rows.Scan(&value); err!=nil { rows.Close(); t.Fatal(err) }
    if rows.Next() { t.Error("scalar wrapper returned extra row") }; if err=rows.Err(); err!=nil { t.Error(err) }; rows.Close()
    hostBag(t,p,b,c,"$1::hstore",transformsMapRows,value)
    hostRows(t,p,b,c,"SELECT v FROM td_effects ORDER BY v",[]string{"v"},[]string{"INT4"},[][]any{{int64(11)},{int64(22)}})
   })
  })
 }
}

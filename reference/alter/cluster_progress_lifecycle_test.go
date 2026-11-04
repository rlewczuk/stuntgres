package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceClusterProgressLifecycle(t *testing.T) {
	for _, outcome := range []string{"success","cancel","failure"} {
		for _, route := range []string{"direct","fresh","held"} {
			t.Run(outcome+"/"+route,func(t *testing.T){
				persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
					f := clusterProgressSetup(t,p,b,c)
					f.idle()
					f.replace(outcome=="failure")
					_,join,finished := f.start(route)
					first := f.wait(finished)
					before := f.stable(0)
					switch outcome {
					case "success":
						f.unlock(first)
						second := f.wait(finished)
						if second==first { t.Fatal("revisited released gate") }
						after := f.stable(1)
						if after.indexes<=before.indexes { t.Fatal("completed index did not advance",before,after) }
						f.unlock(second)
						o := join()
						fixture.DeleteReferenceCount(t,o.Result,o.Err,0)
					case "cancel":
						var ok bool
						if err := f.observer.QueryRowContext(p.Ctx,"SELECT pg_cancel_backend($1)",f.pid).Scan(&ok); err!=nil || !ok { t.Fatal(ok,err) }
						o := join()
						want := persistenceDiagnostic("57014","canceling statement due to user request")
						if first==761101 { want.Context = `SQL function "cp_key1" statement 1` } else { want.Context = `SQL function "cp_key2" statement 1` }
						fixture.AssertDiagnostic(t,o.Err,&want)
						if o.Result!=nil || o.Rows!=nil { t.Fatal("canceled command exposed outputs") }
					case "failure":
						f.unlock(first)
						o := join()
						want := persistenceDiagnostic("22012","division by zero")
						if first==761101 { want.Context = `SQL function "cp_key1" statement 2` } else { want.Context = `SQL function "cp_key2" statement 2` }
						fixture.AssertDiagnostic(t,o.Err,&want)
						if o.Result!=nil || o.Rows!=nil { t.Fatal("failed command exposed outputs") }
					}
					f.idle()
					if outcome!="success" {
						if got := f.locators(); !reflect.DeepEqual(got,f.baseline) { t.Fatal("failed command published relation/index storage",got,f.baseline) }
					}
					for key,locked := range f.locked { if locked { f.unlock(key) } }
					// Restore executable definitions before inspecting old index/data
					// semantics; the failing replacement must never be evaluated here.
					for _,name := range []string{"cp_key1","cp_key2"} {
						persistenceExec(t,p,b,c,"CREATE OR REPLACE FUNCTION "+name+"(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1$$")
					}
					f.correct(outcome=="success")
					persistenceRun(t,p,b,c,fixture.Prepared(fixture.Exec(clusterProgressCommand,0)))
					f.correct(true)
					f.idle()
					if outcome=="success" && route=="direct" { f.controls() }
				})
			})
		}
	}
}

func TestReferenceClusterProgressFeatureSmoke(t *testing.T) {
	persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		f := clusterProgressSetup(t,p,b,c)
		f.replace(false)
		_,join,finished := f.start("held")
		first := f.wait(finished)
		before := f.stable(0)
		f.unlock(first)
		second := f.wait(finished)
		after := f.stable(1)
		if after.indexes<=before.indexes { t.Fatal("real completed work did not advance") }
		f.unlock(second)
		o := join()
		fixture.DeleteReferenceCount(t,o.Result,o.Err,0)
		f.correct(true)
		f.idle()
	})
}

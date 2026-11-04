package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceClusterProgressPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"cp_observer","cp_member"})
	persistenceScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		f := clusterProgressSetup(t,p,b,c)
		persistenceExec(t,p,b,c,"CREATE ROLE cp_observer")
		persistenceExec(t,p,b,c,"CREATE ROLE cp_member")
		var authenticated string
		if err := c.QueryRowContext(p.Ctx,"SELECT session_user").Scan(&authenticated); err!=nil { t.Fatal(err) }
		f.replace(false)
		_,join,finished := f.start("held")
		first := f.wait(finished)
		f.stable(0)
		defer f.observer.ExecContext(p.Ctx,"RESET ROLE")
		// The same prepared read was created before role changes and grants.
		masked := func(){
			t.Helper()
			for _,held := range []bool{false,true} {
				var row *sql.Row
				if held { row = f.read.QueryRowContext(p.Ctx,f.pid) } else { row = f.observer.QueryRowContext(p.Ctx,"SELECT * FROM pg_stat_progress_cluster WHERE pid=$1",f.pid) }
				var pid,datid int64
				var database string
				var relation,index sql.NullInt64
				var command,phase sql.NullString
				var counters [5]sql.NullInt64
				args := []any{&pid,&datid,&database,&relation,&command,&phase,&index}
				for i := range counters { args = append(args,&counters[i]) }
				if err := row.Scan(args...); err!=nil { t.Fatal(err) }
				if pid!=int64(f.pid) || datid!=f.datid || database!=f.database || relation.Valid || command.Valid || phase.Valid || index.Valid { t.Fatal("restricted identity/fields",pid,datid,database,relation,command,phase,index) }
				for _,counter := range counters { if counter.Valid { t.Fatal("restricted completed counter",counter) } }
			}
		}
		persistenceExec(t,p,b,f.observer,"SET ROLE cp_observer")
		masked()
		persistenceExec(t,p,b,f.holder,"GRANT pg_read_all_stats TO cp_observer")
		f.stable(0)
		persistenceExec(t,p,b,f.holder,"REVOKE pg_read_all_stats FROM cp_observer")
		masked()
		persistenceExec(t,p,b,f.observer,"RESET ROLE")
		ownerRole := `"`+strings.ReplaceAll(authenticated,`"`,`""`)+`"`
		persistenceExec(t,p,b,f.holder,"GRANT "+ownerRole+" TO cp_member")
		persistenceExec(t,p,b,f.observer,"SET ROLE cp_member")
		f.stable(0)
		persistenceExec(t,p,b,f.observer,"RESET ROLE")
		persistenceExec(t,p,b,f.holder,"REVOKE "+ownerRole+" FROM cp_member")
		persistenceExec(t,p,b,f.observer,"SET ROLE cp_member")
		masked()
		persistenceExec(t,p,b,f.observer,"RESET ROLE")
		// Native eagerly detaches result backing. Keep a real unconsumed result
		// across worker removal rather than merely compare scalar repetitions.
		var retained *sql.Rows
		if !b.PG {
			var err error
			retained,err = f.read.QueryContext(p.Ctx,f.pid)
			if err!=nil { t.Fatal(err) }
			defer retained.Close()
		}
		f.unlock(first)
		second := f.wait(finished)
		f.stable(1)
		f.unlock(second)
		o := join()
		if b.PG {
			fixture.DeleteReferenceCount(t,o.Result,o.Err,0)
		} else {
			// The grants changed the global authority revision after admission.
			// Disclosure is fresh, but publication must retain its original fence.
			fixture.TransactionOrdinary(t,o.Err,"stuntgres: authorization changed before publication")
			if o.Result!=nil || o.Rows!=nil { t.Fatal("rejected publication exposed outputs") }
			if got := f.locators(); !reflect.DeepEqual(got,f.baseline) { t.Fatal("rejected publication changed relation/index storage",got,f.baseline) }
		}
		if retained!=nil {
			if !retained.Next() { t.Fatal("detached row lost",retained.Err()) }
			values := make([]any,12)
			args := make([]any,12)
			for i := range values { args[i] = &values[i] }
			if err := retained.Scan(args...); err!=nil { t.Fatal(err) }
			if values[4]!="CLUSTER" || values[5]!="rebuilding index" || values[11]!=int64(0) { t.Fatal("detached backing changed",values) }
			if retained.Next() || retained.Err()!=nil { t.Fatal("detached result tail",retained.Err()) }
			retained.Close()
		}
		f.idle()
		f.correct(b.PG)
		// Prove successful terminal publication independently, with unchanged
		// authority throughout another real two-build command. Do not replace
		// the live grant/revoke checks with preconfigured observer privileges.
		f.replace(false)
		for key := range f.locked {
			persistenceRun(t,p,b,f.holder,fixture.Select("SELECT pg_advisory_lock($1)",[]string{"pg_advisory_lock"},[]string{"VOID"},[][]any{{""}},key))
			f.locked[key] = true
		}
		_,join,finished = f.start("held")
		first = f.wait(finished)
		before := f.stable(0)
		f.unlock(first)
		second = f.wait(finished)
		if second==first { t.Fatal("revisited released stable-authority gate") }
		after := f.stable(1)
		if after.indexes<=before.indexes { t.Fatal("stable-authority completed index did not advance",before,after) }
		f.unlock(second)
		o = join()
		fixture.DeleteReferenceCount(t,o.Result,o.Err,0)
		f.idle()
		f.correct(true)
		persistenceExec(t,p,b,c,"DROP ROLE cp_observer")
		persistenceExec(t,p,b,c,"DROP ROLE cp_member")
	})
}

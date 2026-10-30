package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceWALPositionsPrivileges(t *testing.T) {
	const ordinary = "wal_positions_ordinary"
	const replicator = "wal_positions_replicator"
	const member = "wal_positions_member"
	fixture.RoleReferenceCleanup(t,[]string{ordinary,replicator,member})
	recoveryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		for _,q := range []string{"CREATE ROLE "+ordinary,"CREATE ROLE "+replicator+" REPLICATION","CREATE ROLE "+member,"GRANT "+ordinary+" TO "+member} { run(fixture.Exec(q,0)) }
		defer walPositionRestore(t,p,c,b.PG,ordinary,replicator,member)()
		run(fixture.Exec("GRANT "+replicator+" TO "+member,0))
		for i,call := range walPositionCalls {
			q,name,identity := "SELECT "+call,walPositionNames[i],walPositionIdentities[i]
			stmt,err := c.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }
			defer stmt.Close()
			held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
			check := func(allowed bool) {
				if allowed { walPositionBoundaryRoutes(t,p,b,c,q,name); walPositionBoundary(t,p,b,held,q,name) } else {
					s := recoveryControlDenied(q,name); replicationMessageRoutes(t,p,b,c,s); fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
				}
				run(fixture.Select("SELECT has_function_privilege(current_user,'"+identity+"','EXECUTE') AS allowed",[]string{"allowed"},[]string{"BOOL"},[][]any{{allowed}}))
			}
			for _,role := range []string{ordinary,replicator} {
				run(fixture.Exec("SET SESSION AUTHORIZATION "+role,0)); check(i<3); run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
			}
			if i<3 { run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+identity+" FROM PUBLIC",0)) }
			for _,granted := range []bool{true,false,true} {
				action := "REVOKE EXECUTE ON FUNCTION "+identity+" FROM "+ordinary
				if granted { action = "GRANT EXECUTE ON FUNCTION "+identity+" TO "+ordinary }
				run(fixture.Exec(action,0))
				run(fixture.Exec("SET SESSION AUTHORIZATION "+ordinary,0)); check(granted)
				if i==4 && !granted {
					replicationMessageRoutes(t,p,b,c,recoveryControlDenied("SELECT pg_create_restore_point('"+strings.Repeat("a",64)+"')","pg_create_restore_point"))
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
			}
			// Inherited ACLs admit the member; SET ROLE changes the live actor
			// of the same already-prepared handle, not its selected declaration.
			run(fixture.Exec("SET SESSION AUTHORIZATION "+member,0)); check(true)
			run(fixture.Exec("SET ROLE "+ordinary,0)); check(true)
			run(fixture.Exec("RESET ROLE",0)); check(true)
			run(fixture.Exec("SET ROLE "+replicator,0)); check(false)
			run(fixture.Exec("RESET ROLE",0)); check(true)
			run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+identity+" FROM "+ordinary,0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+member,0)); check(false); run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
			for _,public := range []bool{true,false} {
				action := "REVOKE EXECUTE ON FUNCTION "+identity+" FROM PUBLIC"; if public { action = "GRANT EXECUTE ON FUNCTION "+identity+" TO PUBLIC" }
				run(fixture.Exec(action,0)); run(fixture.Exec("SET SESSION AUTHORIZATION "+ordinary,0)); check(public); run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
			}
			stmt.Close()
			// Raw ACLs are independently visible; denial cannot be mistaken for
			// admission merely because physical execution also fails natively.
			run(fixture.Select("SELECT proacl IS NULL AS absent FROM pg_proc WHERE oid='"+identity+"'::regprocedure",[]string{"absent"},[]string{"BOOL"},[][]any{{false}}))
		}
	})
}

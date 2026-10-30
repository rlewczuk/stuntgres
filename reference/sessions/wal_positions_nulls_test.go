package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceWALPositionsNullsMetadata(t *testing.T) {
	const role = "wal_positions_null_reader"
	fixture.RoleReferenceCleanup(t,[]string{role})
	recoveryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		run(fixture.Exec("CREATE ROLE "+role,0))
		defer walPositionRestore(t,p,c,b.PG,role)()
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role,0))
		run(fixture.Exec("CREATE TABLE wal_positions_null_input(v text)",0))
		run(fixture.Exec("INSERT INTO wal_positions_null_input VALUES(NULL)",1))
		run(fixture.Exec("GRANT SELECT ON wal_positions_null_input TO "+role,0))
		for _,aliased := range []bool{false,true} {
			names := append([]string(nil),walPositionNames...)
			calls := append([]string(nil),walPositionCalls...)
			if aliased { for i := range calls { names[i] = "result_"+walPositionNames[i]; calls[i] += " AS "+names[i] } }
			s := fixture.Select("SELECT "+strings.Join(calls,",")+" WHERE FALSE",names,[]string{"PG_LSN","PG_LSN","PG_LSN","PG_LSN","PG_LSN","PG_LSN"},nil)
			s.CheckMetadata = walPositionMetadata(b.PG,names)
			replicationMessageRoutes(t,p,b,c,s)
		}
		q := "SELECT pg_create_restore_point($1::text) AS result"
		stmt,err := c.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		for _,granted := range []bool{false,true} {
			if granted { run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_create_restore_point(text) TO "+role,0)) }
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role,0))
			for _,call := range []string{"pg_create_restore_point(NULL)","pg_create_restore_point(NULL::text)"} {
				replicationMessageRoutes(t,p,b,c,fixture.Select("SELECT "+call+" AS result",[]string{"result"},[]string{"PG_LSN"},[][]any{{nil}}))
				replicationMessageRoutes(t,p,b,c,fixture.Select("SELECT * FROM "+call,[]string{"pg_create_restore_point"},[]string{"PG_LSN"},[][]any{{nil}}))
			}
			nullStep := fixture.Select(q,[]string{"result"},[]string{"PG_LSN"},[][]any{{nil}},nil)
			replicationMessageRoutes(t,p,b,c,nullStep)
			for _,arg := range []any{nil,"wal_positions_parameter",nil} {
				if arg == nil { s := nullStep; fixture.RunStep(t,p.Ctx,held,b.Recorder,s) } else if granted { walPositionBoundary(t,p,b,held,q,"pg_create_restore_point",arg) } else { s := recoveryControlDenied(q,"pg_create_restore_point"); s.Args = []any{arg}; fixture.RunStep(t,p.Ctx,held,b.Recorder,s) }
			}
			for _,query := range []string{"SELECT pg_create_restore_point(v) AS result FROM wal_positions_null_input","SELECT x.result FROM wal_positions_null_input,LATERAL pg_create_restore_point(v) AS x(result)"} {
				s := fixture.Select(query,[]string{"result"},[]string{"PG_LSN"},[][]any{{nil}})
				if !granted { s = recoveryControlDenied(query,"pg_create_restore_point") }
				replicationMessageRoutes(t,p,b,c,s)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		}
		run(fixture.Exec("INSERT INTO wal_positions_null_input VALUES('wal_positions_runtime')",1))
		walPositionBoundaryRoutes(t,p,b,c,"SELECT pg_create_restore_point(v) FROM wal_positions_null_input WHERE v='wal_positions_runtime'","pg_create_restore_point")
		for i,call := range walPositionCalls {
			s := fixture.Select("SELECT * FROM "+call+" WHERE FALSE",[]string{walPositionNames[i]},[]string{"PG_LSN"},nil)
			replicationMessageRoutes(t,p,b,c,s)
		}
	})
}

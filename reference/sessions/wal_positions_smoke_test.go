package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceWALPositionsFeatureSmoke(t *testing.T) {
	const role = "wal_positions_smoke_reader"
	fixture.RoleReferenceCleanup(t,[]string{role})
	recoveryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		run(fixture.Exec("CREATE ROLE "+role,0))
		defer walPositionRestore(t,p,c,b.PG,role)()
		run(fixture.Exec("CREATE TABLE wal_positions_smoke_sentinel(v integer)",0))
		run(fixture.Exec("INSERT INTO wal_positions_smoke_sentinel VALUES(7)",1))
		s := fixture.Select("SELECT "+strings.Join(walPositionCalls,",")+" WHERE FALSE",walPositionNames,[]string{"PG_LSN","PG_LSN","PG_LSN","PG_LSN","PG_LSN","PG_LSN"},nil)
		s.CheckMetadata = walPositionMetadata(b.PG,walPositionNames)
		replicationMessageRoutes(t,p,b,c,s)
		for i,call := range walPositionCalls { walPositionBoundaryRoutes(t,p,b,c,"SELECT "+call,walPositionNames[i]) }
		q := "SELECT pg_create_restore_point($1::text) AS marker"
		stmt,err := c.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role,0))
		for i:=3;i<len(walPositionCalls);i++ { replicationMessageRoutes(t,p,b,c,recoveryControlDenied("SELECT "+walPositionCalls[i],walPositionNames[i])) }
		run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_create_restore_point(text) TO "+role,0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role,0))
		walPositionBoundary(t,p,b,held,q,"pg_create_restore_point","wal_positions_smoke_marker")
		walPositionBoundary(t,p,b,held,q,"pg_create_restore_point",strings.Repeat("a",63))
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,[]string{"marker"},[]string{"PG_LSN"},[][]any{{nil}},nil))
		tooLong := replicationMessageError(q,"22023","value too long for restore point (maximum 63 characters)",""); tooLong.Args=[]any{strings.Repeat("a",64)}
		fixture.RunStep(t,p.Ctx,held,b.Recorder,tooLong)
		run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_create_restore_point(text) FROM "+role,0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role,0))
		denied := recoveryControlDenied(q,"pg_create_restore_point"); denied.Args=[]any{"wal_positions_revoked"}
		fixture.RunStep(t,p.Ctx,held,b.Recorder,denied)
		run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		run(fixture.Select("SELECT v FROM wal_positions_smoke_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
	})
}

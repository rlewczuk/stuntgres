package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceWALPositionsPhysicalBoundary(t *testing.T) {
	recoveryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
		run(fixture.Exec("CREATE TABLE wal_positions_sentinel(v integer)",0))
		run(fixture.Exec("INSERT INTO wal_positions_sentinel VALUES(7)",1))
		run(fixture.Exec("SET application_name='wal_positions_boundary'",0))
		var session,current string
		if err := c.QueryRowContext(p.Ctx,"SELECT session_user,current_user").Scan(&session,&current); err != nil { t.Fatal(err) }
		check := func() {
			run(fixture.Select("SELECT v FROM wal_positions_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
			run(fixture.Select("SELECT session_user,current_user",[]string{"session_user","current_user"},[]string{"NAME","NAME"},[][]any{{session,current}}))
			run(fixture.Select("SHOW application_name",[]string{"application_name"},[]string{"TEXT"},[][]any{{"wal_positions_boundary"}}))
		}
		for i,call := range walPositionCalls {
			walPositionBoundaryRoutes(t,p,b,c,"SELECT pg_catalog."+call,walPositionNames[i])
			upper := strings.ToUpper(walPositionNames[i])+call[len(walPositionNames[i]):]
			walPositionBoundaryRoutes(t,p,b,c,"SELECT "+upper,walPositionNames[i])
			for _,query := range []string{"SELECT "+call,"SELECT * FROM "+call} {
				walPositionBoundaryRoutes(t,p,b,c,query,walPositionNames[i]); check()
				if !b.PG {
					stmt,err := c.PrepareContext(p.Ctx,query); if err != nil { t.Fatal(err) }
					defer stmt.Close()
					ctx,cancel := context.WithCancel(p.Ctx); cancel()
					for _,executor := range []fixture.Executor{c,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:query}} {
						rows,err := executor.QueryContext(ctx,query)
						if rows != nil { rows.Close(); t.Fatal("cancellation published rows") }
						if !errors.Is(err,context.Canceled) { t.Fatal("context hidden",err) }
					}
					stmt.Close(); check()
				}
			}
		}
		s := fixture.Select("SELECT "+strings.Join(walPositionCalls,",")+" WHERE FALSE",walPositionNames,[]string{"PG_LSN","PG_LSN","PG_LSN","PG_LSN","PG_LSN","PG_LSN"},nil)
		s.CheckMetadata = walPositionMetadata(b.PG,walPositionNames)
		replicationMessageRoutes(t,p,b,c,s)
	})
}

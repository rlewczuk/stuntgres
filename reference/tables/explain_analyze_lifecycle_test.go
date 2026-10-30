package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExplainAnalyzeForeignCancellation(t *testing.T) {
	for _,prepared:=range []bool{false,true}{
		for _,insert:=range []bool{false,true}{
			t.Run(fmt.Sprintf("prepared=%t/insert=%t",prepared,insert),func(t *testing.T){
				fdwScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fdwSQLReader,run func(fixture.Step)){
					fdwPushdownLive(t,p,b,r,run)
					run(fixture.Exec("CREATE TABLE fdw_push_target(k integer,v integer)",0))
					run(fixture.Exec("INSERT INTO fdw_push_target VALUES(99,99)",1))
					fdwPushdownCancel(t,p,b,c,r,insert,prepared,true)
					run(fixture.Select("SELECT k,v FROM fdw_push_target",[]string{"k","v"},[]string{"INT4","INT4"},[][]any{{int64(99),int64(99)}}))
				})
			})
		}
	}
	t.Run("registered-provider-close-error",func(t *testing.T){
		fdwScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fdwSQLReader,run func(fixture.Step)){
			fdwLive(t,p,b,r,run)
			run(fixture.Exec("CREATE TABLE ea_close(id integer)",0))
			run(fixture.Exec("INSERT INTO ea_close VALUES(99)",1))
			// Provider errors have no PostgreSQL SQLSTATE counterpart. This
			// native control phase complements the shared cancellation scenario.
			if b.PG {return}
			for _,prepared:=range []bool{false,true}{
				before:=r.snapshot()
				failure:=errors.New("actual source close refused analyzed completion")
				r.setHook(func(ctx context.Context,method string)error{if method=="EndForeignScan" {return failure};return ctx.Err()})
				query:="EXPLAIN ANALYZE INSERT INTO ea_close SELECT id FROM fdw_data RETURNING id"
				var rows *sql.Rows
				var err error
				if prepared {
					statement,prepareErr:=c.PrepareContext(p.Ctx,query)
					if prepareErr!=nil {r.setHook(nil);t.Fatal(prepareErr)}
					rows,err=statement.QueryContext(p.Ctx)
					statement.Close()
				}else{rows,err=c.QueryContext(p.Ctx,query)}
				r.setHook(nil)
				if rows!=nil {rows.Close();t.Fatal("source-close error published plan rows")}
				if !errors.Is(err,failure){t.Fatal(err)}
				fdwReleased(t,r,before,1)
				run(fixture.Select("SELECT id FROM ea_close",[]string{"id"},[]string{"INT4"},[][]any{{int64(99)}}))
			}
		})
	})
}

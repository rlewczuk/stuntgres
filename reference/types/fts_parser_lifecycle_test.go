package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSParserLifecycle(t *testing.T) {
	for _, prepared := range []bool{false,true} {
		for _, queryMode := range []bool{false,true} {
			t.Run(fmt.Sprintf("prepared_%t_query_%t",prepared,queryMode),func(t *testing.T) {
				ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
					conn,err := b.DB.Conn(p.Ctx)
					if err!=nil { t.Fatal(err) }
					defer conn.Close()
					fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Exec("CREATE TABLE fts_parser_cancel(tokid integer,token text)",0))
					fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Exec("INSERT INTO fts_parser_cancel VALUES(1,'sentinel')",1))
					// The admitted sleep is a deterministic argument barrier before
					// parser execution and mutation. Interior tokenizer cancellation
					// is independently exercised by the leaf's counted-context units.
					query := "INSERT INTO fts_parser_cancel SELECT * FROM ts_parse('default',(SELECT 'Word'::text WHERE pg_sleep(600) IS NOT NULL)) RETURNING tokid,token"
					var stmt *sql.Stmt
					if prepared {
						stmt,err=conn.PrepareContext(p.Ctx,query)
						if err!=nil { t.Fatal(err) }
						defer stmt.Close()
					}
					operation := func(ctx context.Context) fixture.RelationOutcome {
						if queryMode {
							if stmt!=nil { rows,err:=stmt.QueryContext(ctx); return fixture.RelationOutcome{Rows:rows,Err:err} }
							rows,err:=conn.QueryContext(ctx,query)
							return fixture.RelationOutcome{Rows:rows,Err:err}
						}
						if stmt!=nil { result,err:=stmt.ExecContext(ctx); return fixture.RelationOutcome{Result:result,Err:err} }
						result,err:=conn.ExecContext(ctx,query)
						return fixture.RelationOutcome{Result:result,Err:err}
					}
					if !b.PG {
						ctx,cancel:=context.WithCancel(p.Ctx)
						cancel()
						out:=operation(ctx)
						if out.Rows!=nil { out.Rows.Close(); t.Fatal("pre-canceled operation exposed rows") }
						if out.Result!=nil { t.Fatal("pre-canceled operation exposed result") }
						fixture.LexicalOrdinary(t,out.Err,context.Canceled)
					}
					fixture.SleepActiveCancel(t,p,b,conn,query,nil,operation)
					fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Select("SELECT * FROM fts_parser_cancel",[]string{"tokid","token"},[]string{"INT4","TEXT"},[][]any{{int64(1),"sentinel"}}))
					fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Select("SELECT * FROM ts_parse('default','Recovered')",[]string{"tokid","token"},[]string{"INT4","TEXT"},[][]any{{int64(1),"Recovered"}}))
				})
			})
		}
	}
}

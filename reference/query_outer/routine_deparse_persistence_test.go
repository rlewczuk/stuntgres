package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func routineDeparseSmokeCheck(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	routineDeparseFour(t,p,b,c,"rd_f(integer)",[4]any{routineDeparseDefault,"a integer DEFAULT 7","a integer","integer"})
	routineDeparseFour(t,p,b,c,"rd_p(integer)",[4]any{routineDeparseProcedure,"IN x integer DEFAULT 7","IN x integer",nil})
	resultsBoth(t,p,b,c,"SELECT rd_f() AS omitted,rd_f(9) AS supplied",[]string{"omitted","supplied"},[]string{"INT4","INT4"},[][]any{{int64(7),int64(9)}})
	resultsExec(t,p,b,c,"CALL rd_p()")
	want := fixture.ControlsReferenceDiagnostic("42809",`"count" is an aggregate function`)
	for _,prepared := range []bool{false,true} { routineDeparseFailure(t,p,b,c,"SELECT pg_get_functiondef(2803::oid)",want,prepared,true,nil) }
}

func routineDeparsePersistence(t *testing.T,smoke bool) {
	t.Helper()
	p := fixture.NewScenario(t)
	backends := p.Backends()
	// Observe the ordinary PG branch before replaying unchanged native SQL there.
	for i := len(backends)-1;i >= 0;i-- {
		b := backends[i]
		t.Run(b.Name,func(t *testing.T){
			c,err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) };defer c.Close()
			routineDeparseSeed(t,p,b,c)
			if smoke { resultsExec(t,p,b,c,"CREATE PROCEDURE rd_p(IN x integer DEFAULT 7) LANGUAGE SQL AS 'SELECT x'") } else { routineDeparseModesSeed(t,p,b,c) }
			check := func(conn *sql.Conn,backend fixture.Backend){
				routineDeparseSmokeCheck(t,p,backend,conn)
				if !smoke { routineDeparseModesCheck(t,p,backend,conn) }
			}
			check(c,b)
			definition := fixture.DeparseRelationsRead(t,p,b,c,"SELECT pg_get_functiondef('rd_f(integer)'::regprocedure) AS definition")
			resultsExec(t,p,b,c,"DROP FUNCTION rd_f(integer)",definition)
			check(c,b)
			if b.PG { return } // No PostgreSQL counterpart to native control APIs.
			var emitted []byte
			for range 2 {
				emitted = resultsNativeReplayRound(t,p,b,c,check)
				if p.Postgres != nil {
					func(){
						pg,err := p.Postgres.Conn(p.Ctx)
						if err != nil { t.Fatal(err) };defer pg.Close()
						if _,err := pg.ExecContext(p.Ctx,"DROP SCHEMA public CASCADE;CREATE SCHEMA public;GRANT USAGE ON SCHEMA public TO PUBLIC");err != nil { t.Fatal(err) }
						if _,err := pg.ExecContext(p.Ctx,string(emitted));err != nil { t.Fatal("unchanged native routine declaration replay",err) }
						if _,err := pg.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION;SET check_function_bodies=true");err != nil { t.Fatal(err) }
						check(pg,fixture.Backend{PG:true,Recorder:p.PostgresRecord})
						if _,err := pg.ExecContext(p.Ctx,"DROP SCHEMA public CASCADE;CREATE SCHEMA public");err != nil { t.Fatal(err) }
					}()
				}
			}
			connector,err := stuntgres.NewDriver().OpenConnector(t.Name()+"/fresh")
			if err != nil { t.Fatal(err) }
			raw := sql.OpenDB(connector);defer raw.Close()
			recorded,recorder := fixture.RecordedNativePool(t,connector)
			fixture.RunStep(t,p.Ctx,recorded,recorder,fixture.Exec("CREATE TYPE rd_pressure AS (x integer)",0))
			if err := stuntgres.Restore(p.Ctx,raw,bytes.NewReader(emitted));err != nil { t.Fatal(err) }
			clone,err := recorded.Conn(p.Ctx)
			if err != nil { t.Fatal(err) };defer clone.Close()
			check(clone,fixture.Backend{Recorder:recorder})
			retained,err := c.QueryContext(p.Ctx,"SELECT pg_get_functiondef('rd_f(integer)'::regprocedure) AS definition")
			if err != nil { t.Fatal(err) };defer retained.Close()
			if err := stuntgres.Restore(p.Ctx,p.Native,resultsFailReader{err:io.ErrUnexpectedEOF});!errors.Is(err,io.ErrUnexpectedEOF) { t.Fatal(err) }
			check(c,b)
			ctx,cancel := context.WithCancel(p.Ctx);defer cancel()
			half := len(emitted)/2
			reader := &attributesCancelRestoreReader{prefix:emitted[:half],rest:bytes.NewReader(emitted[half:]),cancel:cancel}
			if err := stuntgres.Restore(ctx,p.Native,reader);!errors.Is(err,context.Canceled) { t.Fatal(err) }
			if !reader.entered { t.Fatal("restore never consumed a stream prefix") }
			check(c,b)
			fixture.DeparseRelationsMetadata(t,false,retained)
			if !retained.Next() { t.Fatal("lost retained definition",retained.Err()) }
			var old string
			if err := retained.Scan(&old);err != nil || old != routineDeparseDefault { t.Fatal(old,err) }
			if retained.Next() || retained.Err() != nil { t.Fatal("extra retained definition",retained.Err()) }
		})
	}
}

func TestReferenceRoutineDeparsePersistence(t *testing.T) { routineDeparsePersistence(t,false) }
func TestReferenceRoutineDeparseFeatureSmoke(t *testing.T) { routineDeparsePersistence(t,true) }

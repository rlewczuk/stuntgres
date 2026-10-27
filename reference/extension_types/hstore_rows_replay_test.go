package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var hstoreRowsCurrentNames = []string{"keys_now","values_now","pairs_now"}

func hstoreRowsViewSeed(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	hstoreRowsSeed(t,p,b,c)
	for _,q:=range []string{
		`CREATE VIEW hs_rows_keys AS SELECT id,skeys(v) AS k FROM hs_rows`,
		`CREATE VIEW hs_rows_values AS SELECT id,svals(v) AS v FROM hs_rows`,
		`CREATE VIEW hs_rows_pairs AS SELECT h.id,e.key,e.value FROM hs_rows h CROSS JOIN LATERAL each(hs => h.v) e`,
		`CREATE VIEW hs_rows_nulls AS SELECT h.id,e.key FROM hs_rows h CROSS JOIN LATERAL each(h.v) e WHERE e.value IS NULL`,
		`CREATE VIEW hs_rows_fields AS SELECT id,(each(v)).key AS key,(each(v)).value AS value FROM hs_rows`,
	} {hstoreRun(t,p,b,c,fixture.Exec(q,0))}
}

func hstoreRowsMove(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	hstoreRun(t,p,b,c,fixture.Exec("CREATE SCHEMA hs_rows_replay",0))
	for i,name:=range []string{"skeys","svals","each"} {
		var before,after int64
		if err:=c.QueryRowContext(p.Ctx,"SELECT $1::regprocedure::oid",name+"(hstore)").Scan(&before);err!=nil{t.Fatal(err)}
		hstoreRun(t,p,b,c,fixture.Exec("ALTER FUNCTION "+name+"(hstore) RENAME TO "+hstoreRowsCurrentNames[i],0))
		hstoreRun(t,p,b,c,fixture.Exec("ALTER FUNCTION "+hstoreRowsCurrentNames[i]+"(hstore) SET SCHEMA hs_rows_replay",0))
		if err:=c.QueryRowContext(p.Ctx,"SELECT $1::regprocedure::oid","hs_rows_replay."+hstoreRowsCurrentNames[i]+"(hstore)").Scan(&after);err!=nil{t.Fatal(err)}
		if before!=after {t.Fatalf("rename/move changed %s identity %d -> %d",name,before,after)}
	}
}

func hstoreRowsReplayVerify(t *testing.T,p *fixture.Pair,b fixture.Backend,db fixture.Executor) {
	t.Helper()
	hstoreRowsCatalog(t,p,b,db,"hs_rows_replay",hstoreRowsCurrentNames)
	for i,name:=range []string{"skeys","svals","each"} {
		q:="SELECT * FROM hs_rows_replay."+hstoreRowsCurrentNames[i]+"($1::hstore)"
		// Scalar FROM labels follow current names, whereas fixed OUT labels do not.
		labels:=[]string{hstoreRowsCurrentNames[i]};if name=="each"{labels=hstoreRowsNames(name)}
		stmt,err:=db.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
		func(){defer stmt.Close();held:=fixture.HeldStatement{Executor:db,Statement:stmt,SQL:q}
			for _,input:=range []struct{v any;rows [][]any}{{hstoreRowsInput,hstoreRowsValues(name)},{"",[][]any{}},{nil,[][]any{}}}{
				s:=hstoreRowsSelect(q,labels,input.rows,input.v)
				hstoreRowsBoth(t,p,b,db,s);hstoreRun(t,p,b,held,s)
			}
		}()
	}
	hstoreRowsBoth(t,p,b,db,hstoreRowsSelect(`SELECT * FROM hs_rows_replay.pairs_now(hs => $1::hstore) WHERE value IS NULL`,[]string{"key","value"},[][]any{{"b",nil}},hstoreRowsInput))
	checks:=[]fixture.Step{
		hstoreRowsSelect(`SELECT k FROM hs_rows_keys WHERE id=1`,[]string{"k"},hstoreRowsValues("skeys")),
		hstoreRowsSelect(`SELECT v FROM hs_rows_values WHERE id=1`,[]string{"v"},hstoreRowsValues("svals")),
		hstoreRowsSelect(`SELECT key,value FROM hs_rows_pairs WHERE id=1`,[]string{"key","value"},hstoreRowsPairs()),
		hstoreRowsSelect(`SELECT key,value FROM hs_rows_fields WHERE id=1`,[]string{"key","value"},hstoreRowsPairs()),
		hstoreRowsSelect(`SELECT key,value FROM hs_rows_pairs WHERE id=5`,[]string{"key","value"},[][]any{{"é\\\"","雪\\\""}}),
	}
	for _,s:=range checks {
		// Stored view columns have adapter-specific declared nullability, unlike
		// direct function output. The step still asserts exact labels and types.
		s.CheckMetadata=nil
		hstoreRowsBoth(t,p,b,db,s)
	}
	s:=fixture.Select(`SELECT id,key FROM hs_rows_nulls`,[]string{"id","key"},[]string{"INT4","TEXT"},[][]any{{int64(1),"b"},{int64(4),"a"}});s.Bag=true
	hstoreRowsBoth(t,p,b,db,s)
	for _,id:=range []string{"2","3"} {
		hstoreRowsBoth(t,p,b,db,fixture.Select(`SELECT key,value FROM hs_rows_pairs WHERE id=`+id,[]string{"key","value"},[]string{"TEXT","TEXT"},[][]any{}))
	}
}

func hstoreRowsRetain(t *testing.T,p *fixture.Pair,db fixture.Executor) (*sql.Rows,[]*sql.ColumnType) {
	t.Helper()
	rows,err:=db.QueryContext(p.Ctx,`SELECT key,value FROM hs_rows_replay.pairs_now((SELECT v FROM hs_rows WHERE id=1))`);if err!=nil{t.Fatal(err)}
	t.Cleanup(func(){rows.Close()})
	columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)}
	hstoreRowsTextMetadata(t,columns,[]string{"key","value"})
	return rows,columns
}

func hstoreRowsConsume(t *testing.T,rows *sql.Rows,columns []*sql.ColumnType) {
	t.Helper()
	var got [][]any
	for rows.Next(){var key string;var value sql.NullString;if err:=rows.Scan(&key,&value);err!=nil{t.Fatal(err)};var v any;if value.Valid{v=value.String};got=append(got,[]any{key,v})}
	if err:=rows.Err();err!=nil{t.Fatal(err)};if err:=rows.Close();err!=nil{t.Fatal(err)}
	hstoreRowsAssertBag(t,got,hstoreRowsPairs());hstoreRowsTextMetadata(t,columns,[]string{"key","value"})
}

func hstoreRowsEmittedReplay(t *testing.T,p *fixture.Pair,dump []byte) {
	t.Helper()
	if p.Postgres==nil{return} // Only the real-oracle phase needs a PostgreSQL pool.
	c,err:=p.Postgres.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer c.Close()
	clean:=`DROP SCHEMA IF EXISTS hs_rows_replay CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public`
	if _,err:=c.ExecContext(p.Ctx,clean);err!=nil{t.Fatal(err)}
	defer func(){ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel();if _,err:=c.ExecContext(ctx,clean);err!=nil{t.Error(err)}}()
	if _,err:=c.ExecContext(p.Ctx,string(dump));err!=nil{t.Fatalf("actual emitted dump replay: %v",err)}
	pg:=fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord}
	hstoreRowsReplayVerify(t,p,pg,c)
}

func hstoreRowsReplayPhase(t *testing.T,phase string) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t,"hs_rows_replay")
	hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		hstoreRowsViewSeed(t,p,b,c)
		// Keep the prepared SQL valid when PostgreSQL reparses it after DDL.
		// Stored views retain the routine identities; original function spellings
		// in prepared SQL do not remain resolvable after a rename/schema move.
		held:=make([]fixture.HeldStatement,0,3)
		queries:=[]string{
			"SELECT k AS skeys FROM hs_rows_keys WHERE id=1",
			"SELECT v AS svals FROM hs_rows_values WHERE id=1",
			"SELECT key,value FROM hs_rows_pairs WHERE id=1",
		}
		for i,name:=range []string{"skeys","svals","each"} {
			q:=queries[i]
			stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
			h:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
			s:=hstoreRowsSelect(q,hstoreRowsNames(name),hstoreRowsValues(name))
			s.CheckMetadata=nil // Stored-view nullability follows the adapter convention.
			hstoreRun(t,p,b,h,s)
			held=append(held,h)
		}
		hstoreRowsMove(t,p,b,c)
		for i,name:=range []string{"skeys","svals","each"} {
			s:=hstoreRowsSelect(held[i].SQL,hstoreRowsNames(name),hstoreRowsValues(name))
			s.CheckMetadata=nil // Exact labels/types remain checked by the step.
			hstoreRun(t,p,b,held[i],s)
		}
		hstoreRowsReplayVerify(t,p,b,c)
		if phase=="recreate" {
			for _,h:=range held{if err:=h.Statement.Close();err!=nil{t.Fatal(err)}}
			for _,q:=range []string{"DROP VIEW hs_rows_keys","DROP VIEW hs_rows_values","DROP VIEW hs_rows_pairs","DROP VIEW hs_rows_nulls","DROP VIEW hs_rows_fields","DROP TABLE hs_rows","DROP EXTENSION hstore"}{hstoreRun(t,p,b,c,fixture.Exec(q,0))}
			hstoreInstall(t,p,b,c);hstoreRowsCatalog(t,p,b,c,"public",[]string{"skeys","svals","each"});hstoreRowsPrepared(t,p,b,c)
			return
		}
		if b.PG{return} // Native controls are explicit phases, not SQL emulations.
		dump:=hstoreDump(t,p,p.Native)
		if phase=="same" {
			current:=make([]fixture.HeldStatement,0,3)
			typed:=make([]fixture.HeldStatement,0,3)
			for _,name:=range hstoreRowsCurrentNames {
				// A builtin public parameter survives replacement; conversion and
				// the selected SRF must bind to the restored hstore installation.
				q:="SELECT * FROM hs_rows_replay."+name+"($1::text::hstore)"
				stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
				current=append(current,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q})
				q="SELECT * FROM hs_rows_replay."+name+"($1::hstore)"
				stmt,err=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
				typed=append(typed,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q})
			}
			old,columns:=hstoreRowsRetain(t,p,b.DB)
			if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
			if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
			hstoreRowsReplayVerify(t,p,b,c);hstoreRowsConsume(t,old,columns)
			for _,h:=range typed {
				// A genuine catalog-owned parameter is intentionally fenced
				// across Reset/Restore, independently of the TEXT result type.
				rows,err:=h.Statement.QueryContext(p.Ctx,hstoreRowsInput)
				if rows!=nil{rows.Close()}
				want:=fixture.ControlsReferenceDiagnostic("0A000","cached plan must not change parameter type")
				fixture.AssertDiagnostic(t,err,&want)
			}
			for i,name:=range []string{"skeys","svals","each"} {
				labels:=[]string{hstoreRowsCurrentNames[i]};if name=="each"{labels=hstoreRowsNames(name)}
				for _,input:=range []struct{v any;rows [][]any}{{hstoreRowsInput,hstoreRowsValues(name)},{"",[][]any{}},{nil,[][]any{}}} {
					hstoreRun(t,p,b,current[i],hstoreRowsSelect(current[i].SQL,labels,input.rows,input.v))
				}
			}
			if after:=hstoreDump(t,p,p.Native);!bytes.Equal(dump,after){t.Fatalf("same-target Restore changed deterministic dump\nbefore=%s\nafter=%s",dump,after)}
		} else {
			destination:=fixture.NewFixture(t,false,true)
			target:=fixture.Backend{DB:destination.Memory,Recorder:destination.MemoryRecord}
			hstoreRun(t,p,target,destination.Memory,fixture.Exec("CREATE TABLE hs_rows_displaced(v integer)",0))
			hstoreRun(t,p,target,destination.Memory,fixture.Exec("CREATE EXTENSION hstore",0))
			if err:=stuntgres.Restore(p.Ctx,destination.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
			hstoreRowsReplayVerify(t,p,target,destination.Memory)
			hstoreRun(t,p,target,destination.Memory,fixture.Select(`SELECT relname FROM pg_class WHERE relname='hs_rows_displaced' AND relnamespace='public'::regnamespace`,[]string{"relname"},[]string{"NAME"},[][]any{}))
			if phase=="failed" || phase=="canceled" {
				old,columns:=hstoreRowsRetain(t,p,target.DB)
				var before,after [4]int64
				identity:=`SELECT 'public.hstore'::regtype::oid,'hs_rows_replay.keys_now(hstore)'::regprocedure::oid,'hs_rows_replay.values_now(hstore)'::regprocedure::oid,'hs_rows_replay.pairs_now(hstore)'::regprocedure::oid`
				if err:=target.DB.QueryRowContext(p.Ctx,identity).Scan(&before[0],&before[1],&before[2],&before[3]);err!=nil{t.Fatal(err)}
				if phase=="failed" {q,want:=hstoreStagedSQL();fixture.AssertDiagnostic(t,stuntgres.Restore(p.Ctx,destination.Native,strings.NewReader(q)),&want)} else {ctx,cancel:=context.WithCancel(p.Ctx);cancel();if err:=stuntgres.Restore(ctx,destination.Native,bytes.NewReader(dump));!errors.Is(err,context.Canceled){t.Fatalf("canceled Restore: %v",err)}}
				hstoreRowsReplayVerify(t,p,target,destination.Memory)
				if err:=target.DB.QueryRowContext(p.Ctx,identity).Scan(&after[0],&after[1],&after[2],&after[3]);err!=nil{t.Fatal(err)};if before!=after{t.Fatal("failed Restore changed current type/procedure identities")}
				hstoreRowsConsume(t,old,columns)
			}
			if after:=hstoreDump(t,p,destination.Native);!bytes.Equal(dump,after){t.Fatalf("Restore changed deterministic dump\nbefore=%s\nafter=%s",dump,after)}
			if phase=="fresh" {hstoreRowsEmittedReplay(t,p,dump)}
		}
	})
}

func TestReferenceHstoreRowsReplay(t *testing.T) {
	for _,phase:=range []string{"same","fresh","failed","canceled","recreate"}{t.Run(phase,func(t *testing.T){hstoreRowsReplayPhase(t,phase)})}
}

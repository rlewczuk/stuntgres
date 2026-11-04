package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"testing"

	"stuntgres/reference/fixture"
)

const clusterProgressCommand = "CLUSTER public.cp_heap USING cp_plain"

// The gates belong to genuinely changed executable recipes, not unchanged
// indexes or a synthetic progress producer. Index traversal order is immaterial.
type clusterProgressCase struct {
	t *testing.T
	p *fixture.Pair
	b fixture.Backend
	worker, holder, observer *sql.Conn
	command, read *sql.Stmt
	pid int
	datid, relation, plain int64
	database string
	locked map[int64]bool
	baseline map[string][2]int64
}

func clusterProgressSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, worker *sql.Conn) *clusterProgressCase {
	t.Helper()
	f := &clusterProgressCase{t:t, p:p, b:b, worker:worker, locked:map[int64]bool{761101:true,761102:true}}
	conn := func() *sql.Conn {
		c, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		t.Cleanup(func(){ c.Close() })
		return c
	}
	f.holder, f.observer = conn(), conn()
	for _, q := range []string{
		"CREATE TABLE cp_heap(id integer)",
		"CREATE INDEX cp_plain ON cp_heap(id)",
		"CREATE FUNCTION cp_key1(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1$$",
		"CREATE FUNCTION cp_key2(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1$$",
		"CREATE INDEX cp_expr1 ON cp_heap(cp_key1(id))",
		"CREATE INDEX cp_expr2 ON cp_heap(cp_key2(id))",
	} { persistenceExec(t,p,b,worker,q) }
	persistenceRun(t,p,b,worker,fixture.Exec("INSERT INTO cp_heap VALUES(3),(1),(2)",3))
	f.command = clusterCommandHeld(t,p,b,worker,clusterProgressCommand)
	f.read = clusterCommandHeld(t,p,b,f.observer,"SELECT * FROM pg_catalog.pg_stat_progress_cluster WHERE pid=$1")
	f.pid = fixture.LockPID(t,p.Ctx,worker)
	if err := worker.QueryRowContext(p.Ctx,"SELECT oid::bigint,datname FROM pg_database WHERE datname=current_database()").Scan(&f.datid,&f.database); err != nil { t.Fatal(err) }
	if err := worker.QueryRowContext(p.Ctx,"SELECT 'cp_heap'::regclass::bigint,'cp_plain'::regclass::bigint").Scan(&f.relation,&f.plain); err != nil { t.Fatal(err) }
	f.baseline = f.locators()
	for key := range f.locked {
		persistenceRun(t,p,b,f.holder,fixture.Select("SELECT pg_advisory_lock($1)",[]string{"pg_advisory_lock"},[]string{"VOID"},[][]any{{""}},key))
	}
	t.Cleanup(func(){
		for key, locked := range f.locked {
			if locked { f.holder.ExecContext(p.Ctx,"SELECT pg_advisory_unlock($1)",key) }
		}
	})
	return f
}

func (f *clusterProgressCase) replace(failing bool) {
	f.t.Helper()
	for i,key := range []int64{761101,761102} {
		value := "$1"
		if failing { value = "$1 / CASE WHEN pg_catalog.current_setting('application_name') IS NOT NULL THEN 0 ELSE 1 END" }
		persistenceExec(f.t,f.p,f.b,f.worker,fmt.Sprintf("CREATE OR REPLACE FUNCTION cp_key%d(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT pg_catalog.pg_advisory_xact_lock(%d); SELECT %s$$",i+1,key,value))
	}
}

func (f *clusterProgressCase) start(route string) (context.CancelFunc,func() fixture.RelationOutcome,<-chan struct{}) {
	f.t.Helper()
	ctx,cancel := context.WithCancel(f.p.Ctx)
	f.t.Cleanup(cancel)
	statement := f.command
	if route == "fresh" { statement = clusterCommandHeld(f.t,f.p,f.b,f.worker,clusterProgressCommand) }
	finished := make(chan struct{})
	join := fixture.LockOperation(f.t,ctx,func(ctx context.Context) fixture.RelationOutcome {
		defer close(finished)
		var result sql.Result
		var err error
		if route == "direct" { result,err = f.worker.ExecContext(ctx,clusterProgressCommand) } else { result,err = statement.ExecContext(ctx) }
		return fixture.RelationOutcome{Result:result,Err:err}
	})
	return cancel,join,finished
}

func (f *clusterProgressCase) wait(finished <-chan struct{}) int64 {
	f.t.Helper()
	for {
		rows,err := f.observer.QueryContext(f.p.Ctx,"SELECT objid::bigint FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted",f.pid)
		if err != nil { f.t.Fatal(err) }
		var key int64
		if rows.Next() { if err = rows.Scan(&key); err != nil { rows.Close(); f.t.Fatal(err) } }
		err = rows.Err(); rows.Close()
		if err != nil { f.t.Fatal(err) }
		if key != 0 {
			locked, known := f.locked[key]
			if !known { f.t.Fatal("unexpected expression gate",key) }
			if locked { return key }
		}
		select {
		case <-finished: f.t.Fatal("CLUSTER ended before real recipe wait")
		case <-f.p.Ctx.Done(): f.t.Fatal(f.p.Ctx.Err())
		default: runtime.Gosched()
		}
	}
}

func (f *clusterProgressCase) unlock(key int64) {
	f.t.Helper()
	persistenceRun(f.t,f.p,f.b,f.holder,fixture.Select("SELECT pg_advisory_unlock($1)",[]string{"pg_advisory_unlock"},[]string{"BOOL"},[][]any{{true}},key))
	f.locked[key] = false
}

type clusterProgressRow struct {
	pid, datid int64
	database string
	relation int64
	command, phase string
	index, scanned, written, total, blocks, indexes int64
}

func (f *clusterProgressCase) capture(route string, ordinal int) clusterProgressRow {
	f.t.Helper()
	var r clusterProgressRow
	q := "SELECT * FROM pg_catalog.pg_stat_progress_cluster WHERE pid=$1"
	var rows *sql.Rows
	var err error
	statement := f.read
	if route=="fresh" {
		statement,err = f.observer.PrepareContext(f.p.Ctx,q)
		if err!=nil { f.t.Fatal(err) }
		defer statement.Close()
	}
	if route!="direct" { rows,err = statement.QueryContext(f.p.Ctx,f.pid) } else { rows,err = f.observer.QueryContext(f.p.Ctx,q,f.pid) }
	if err != nil { f.t.Fatal(err) }
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta)!=len(clusterCommandProgressColumns) { f.t.Fatal("progress metadata",meta,err) }
	for i,c := range meta {
		if c.Name()!=clusterCommandProgressColumns[i] || c.DatabaseTypeName()!=clusterCommandProgressTypes[i] { f.t.Fatal("progress declaration",i,c.Name(),c.DatabaseTypeName()) }
	}
	if !rows.Next() { f.t.Fatal("missing admitted CLUSTER",rows.Err()) }
	if err = rows.Scan(&r.pid,&r.datid,&r.database,&r.relation,&r.command,&r.phase,&r.index,&r.scanned,&r.written,&r.total,&r.blocks,&r.indexes); err != nil { f.t.Fatal(err) }
	if rows.Next() || rows.Err()!=nil { f.t.Fatal("duplicate worker or invalid rows",rows.Err()) }
	if r.pid!=int64(f.pid) || r.datid!=f.datid || r.database!=f.database || r.relation!=f.relation || r.command!="CLUSTER" || r.phase!="rebuilding index" { f.t.Fatalf("wrong admitted identity/phase: %+v",r) }
	if f.b.PG {
		if r.scanned!=3 || r.written!=3 || r.indexes<int64(ordinal) || r.indexes>int64(ordinal+1) { f.t.Fatalf("PG completed actual work: %+v",r) }
		if r.index==f.plain {
			if r.total!=0 || r.blocks!=0 { f.t.Fatalf("index scan heap blocks: %+v",r) }
		} else if r.index!=0 || r.total<0 || r.blocks!=r.total { f.t.Fatalf("sequential scan branch: %+v",r) }
	} else if r.index!=0 || r.scanned!=0 || r.written!=0 || r.total!=0 || r.blocks!=0 || r.indexes!=int64(ordinal) { f.t.Fatalf("native actual work: %+v",r) }
	return r
}

func (f *clusterProgressCase) stable(ordinal int) clusterProgressRow {
	f.t.Helper()
	r := f.capture("direct",ordinal)
	if held := f.capture("held",ordinal); held!=r { f.t.Fatal("held capture changed at same actual checkpoint",r,held) }
	if fresh := f.capture("fresh",ordinal); fresh!=r { f.t.Fatal("unstable completed work",r,fresh) }
	persistenceRun(f.t,f.p,f.b,f.observer,fixture.Prepared(fixture.Select("SELECT command,phase,relid::bigint AS target FROM pg_stat_progress_cluster WHERE pid=$1",[]string{"command","phase","target"},[]string{"TEXT","TEXT","INT8"},[][]any{{"CLUSTER","rebuilding index",f.relation}},f.pid)))
	return r
}

func (f *clusterProgressCase) idle() {
	f.t.Helper()
	persistenceRun(f.t,f.p,f.b,f.observer,clusterCommandProgressIdle())
	persistenceRun(f.t,f.p,f.b,fixture.HeldStatement{Executor:f.observer,Statement:f.read,SQL:"SELECT * FROM pg_catalog.pg_stat_progress_cluster WHERE pid=$1"},fixture.Select("SELECT * FROM pg_catalog.pg_stat_progress_cluster WHERE pid=$1",clusterCommandProgressColumns,clusterCommandProgressTypes,[][]any{},f.pid))
}

func (f *clusterProgressCase) correct(clustered bool) {
	f.t.Helper()
	persistenceRun(f.t,f.p,f.b,f.worker,fixture.Select("SELECT id FROM cp_heap ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)},{int64(3)}}))
	persistenceRun(f.t,f.p,f.b,f.worker,clusterState("cp_heap",[][]any{{"cp_expr1",false},{"cp_expr2",false},{"cp_plain",clustered}}))
	persistenceRun(f.t,f.p,f.b,f.worker,fixture.Prepared(fixture.Select("SELECT id FROM cp_heap WHERE cp_key1(id)=2 AND cp_key2(id)=2",[]string{"id"},[]string{"INT4"},[][]any{{int64(2)}})))
	persistenceRun(f.t,f.p,f.b,f.worker,fixture.Select("SELECT bool_and(indisvalid AND indisready AND indislive) AS valid FROM pg_index WHERE indrelid='cp_heap'::regclass",[]string{"valid"},[]string{"BOOL"},[][]any{{true}}))
}

func (f *clusterProgressCase) locators() map[string][2]int64 {
	f.t.Helper()
	rows,err := f.worker.QueryContext(f.p.Ctx,"SELECT relname,oid::bigint,relfilenode::bigint FROM pg_class WHERE relname IN ('cp_heap','cp_plain','cp_expr1','cp_expr2')")
	if err!=nil { f.t.Fatal(err) }
	defer rows.Close()
	result := make(map[string][2]int64)
	for rows.Next() {
		var name string
		var identity [2]int64
		if err := rows.Scan(&name,&identity[0],&identity[1]); err!=nil { f.t.Fatal(err) }
		if _,exists := result[name]; exists || identity[0]<=0 || identity[1]<=0 { f.t.Fatal("invalid relation identity",name,identity) }
		result[name] = identity
	}
	if err := rows.Err(); err!=nil { f.t.Fatal(err) }
	if len(result)!=4 { f.t.Fatal("incomplete identity bag",result) }
	return result
}

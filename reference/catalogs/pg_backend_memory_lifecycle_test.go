package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func backendMemoryNamesInstalled(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,names ...string) {
	t.Helper()
	var want [][]any
	for _,name:=range names{want=append(want,[]any{name,true})}
	s:=fixture.Select("SELECT name::text,from_sql FROM pg_prepared_statements WHERE name LIKE 'backend_memory_%' ORDER BY name",[]string{"name","from_sql"},[]string{"TEXT","BOOL"},want)
	fixture.RunStep(t,p.Ctx,e,b.Recorder,s)
}

func backendMemoryOwnership(t *testing.T,p *fixture.Pair,b fixture.Backend,a,z *sql.Conn,full bool) {
	t.Helper()
	baseline:=backendMemoryRead(t,p,b,a,backendMemoryView,false)
	other:=backendMemoryRead(t,p,b,z,backendMemoryView,false)
	// In-process driver handles are not SQL/protocol installed definitions.
	held:=backendMemoryHeld(t,p,a,backendMemoryFunction)
	withHandle:=backendMemoryRead(t,p,b,a,backendMemoryView,false)
	if !b.PG{backendMemorySameOwn(t,withHandle,baseline)}
	backendMemoryRead(t,p,b,held,backendMemoryFunction,false)
	var retained *sql.Rows
	if !b.PG {
		var err error
		retained,err=a.QueryContext(p.Ctx,backendMemoryView);if err!=nil{t.Fatal(err)};defer retained.Close()
		backendMemoryMetadata(t,retained,false,true)
	}
	pgSequenceExec(t,p,b,a,"PREPARE backend_memory_one AS SELECT 42 AS n")
	after:=backendMemoryRead(t,p,b,a,backendMemoryView,false)
	var delta int64
	if !b.PG{delta=backendMemoryDelta(t,baseline,after,0);backendMemorySameOwn(t,backendMemoryRead(t,p,b,z,backendMemoryView,false),other)}
	backendMemoryNamesInstalled(t,p,b,a,"backend_memory_one")
	backendMemoryNamesInstalled(t,p,b,z)
	if retained!=nil{backendMemorySameOwn(t,backendMemoryConsume(t,retained,false,true),baseline)}
	for range 3 {
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("EXECUTE backend_memory_one",[]string{"n"},[]string{"INT4"},[][]any{{int64(42)}}))
		current:=backendMemoryRead(t,p,b,held,backendMemoryFunction,false)
		if !b.PG{backendMemorySameOwn(t,current,after)}
	}
	if full {
		s:=fixture.ImplicitError("PREPARE backend_memory_one AS SELECT 7",fixture.Diagnostic{Code:"42P05",Message:`prepared statement "backend_memory_one" already exists`})
		fixture.RunStep(t,p.Ctx,a,b.Recorder,s)
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("EXECUTE backend_memory_one",[]string{"n"},[]string{"INT4"},[][]any{{int64(42)}}))
		current:=backendMemoryRead(t,p,b,a,backendMemoryView,false);if !b.PG{backendMemorySameOwn(t,current,after)}
		pgSequenceExec(t,p,b,a,"PREPARE backend_memory_two AS SELECT 'a much longer SQL source must not change the selected struct layout'::text AS payload")
		two:=backendMemoryRead(t,p,b,a,backendMemoryView,false)
		if !b.PG{backendMemoryDelta(t,after,two,delta)}
		pgSequenceExec(t,p,b,a,"DISCARD PLANS")
		backendMemoryNamesInstalled(t,p,b,a,"backend_memory_one","backend_memory_two")
		current=backendMemoryRead(t,p,b,a,backendMemoryView,false);if !b.PG{backendMemorySameOwn(t,current,two)}
		pgSequenceExec(t,p,b,a,"DEALLOCATE backend_memory_two")
		current=backendMemoryRead(t,p,b,a,backendMemoryView,false);if !b.PG{backendMemorySameOwn(t,current,after)}
		// A failed definition must not install an owner or replace old bytes.
		q:="PREPARE backend_memory_bad AS SELECT missing"
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.ImplicitError(q,fixture.Diagnostic{Code:"42703",Message:`column "missing" does not exist`,Position:fixture.Position(38)}))
		backendMemoryNamesInstalled(t,p,b,a,"backend_memory_one")
		current=backendMemoryRead(t,p,b,a,backendMemoryView,false);if !b.PG{backendMemorySameOwn(t,current,after)}
	}
	var deallocatedRows *sql.Rows
	var deallocatedMetadata []*sql.ColumnType
	if !b.PG {
		var err error
		deallocatedRows,err=a.QueryContext(p.Ctx,backendMemoryView);if err!=nil{t.Fatal(err)};defer deallocatedRows.Close()
		deallocatedMetadata,err=deallocatedRows.ColumnTypes();if err!=nil{t.Fatal(err)}
	}
	pgSequenceExec(t,p,b,a,"DEALLOCATE backend_memory_one")
	current:=backendMemoryRead(t,p,b,a,backendMemoryView,false)
	if !b.PG{backendMemorySameOwn(t,current,baseline)}
	if deallocatedRows!=nil {
		backendMemoryRetainedMetadata(t,deallocatedRows,deallocatedMetadata)
		backendMemorySameOwn(t,backendMemoryConsume(t,deallocatedRows,false,true),after)
	}
	if !full{return}
	pgSequenceExec(t,p,b,a,"PREPARE backend_memory_one AS SELECT 7 AS n")
	current=backendMemoryRead(t,p,b,a,backendMemoryView,false);if !b.PG{backendMemoryDelta(t,baseline,current,delta)}
	fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("EXECUTE backend_memory_one",[]string{"n"},[]string{"INT4"},[][]any{{int64(7)}}))
	pgSequenceExec(t,p,b,a,"DEALLOCATE backend_memory_one")
	pgSequenceExec(t,p,b,a,"BEGIN")
	pgSequenceExec(t,p,b,a,"PREPARE backend_memory_rollback AS SELECT 42 AS n")
	pgSequenceExec(t,p,b,a,"ROLLBACK")
	backendMemoryNamesInstalled(t,p,b,a,"backend_memory_rollback")
	current=backendMemoryRead(t,p,b,a,backendMemoryView,false);if !b.PG{backendMemoryDelta(t,baseline,current,delta)}
	fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("EXECUTE backend_memory_rollback",[]string{"n"},[]string{"INT4"},[][]any{{int64(42)}}))
	var discardedRows *sql.Rows
	var discardedMetadata []*sql.ColumnType
	if !b.PG {
		var err error
		discardedRows,err=a.QueryContext(p.Ctx,backendMemoryView);if err!=nil{t.Fatal(err)};defer discardedRows.Close()
		discardedMetadata,err=discardedRows.ColumnTypes();if err!=nil{t.Fatal(err)}
	}
	// lib/pq's Go handle owns a named wire statement. DISCARD ALL removes that
	// server definition, so close the handle before discarding session names,
	// after its repeated execution and DEALLOCATE witnesses are complete.
	if err:=held.Statement.Close();err!=nil{t.Fatal("close held source before DISCARD ALL",err)}
	pgSequenceExec(t,p,b,a,"DISCARD ALL")
	backendMemoryNamesInstalled(t,p,b,a)
	if discardedRows!=nil {
		backendMemoryRetainedMetadata(t,discardedRows,discardedMetadata)
		backendMemorySameOwn(t,backendMemoryConsume(t,discardedRows,false,true),current)
	}
	current=backendMemoryRead(t,p,b,a,backendMemoryView,false);if !b.PG{backendMemorySameOwn(t,current,baseline)}
	backendMemorySentinel(t,p,b,z)
}

func TestReferenceBackendMemoryLifecycle(t *testing.T) {
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){
		a,z:=fixture.LockConnections(t,p.Ctx,b.DB)
		backendMemorySetupSentinel(t,p,b,z)
		backendMemoryOwnership(t,p,b,a,z,true)
		pgSequenceExec(t,p,b,a,"PREPARE backend_memory_closed AS SELECT 42")
		// Returning the last borrower with zero idle capacity really terminates
		// this physical owner; ordinary pool return alone would not prove it.
		b.DB.SetMaxIdleConns(0)
		if err:=a.Close();err!=nil{t.Fatal(err)}
		fresh,err:=b.DB.Conn(p.Ctx);if err!=nil{t.Fatal(err)};defer fresh.Close()
		backendMemoryNamesInstalled(t,p,b,fresh)
		current:=backendMemoryRead(t,p,b,fresh,backendMemoryView,false)
		if !b.PG && backendMemoryOwn(current)["PreparedDefinitions"]!=0{t.Fatal("new physical owner retained old definitions",current)}
		backendMemoryRead(t,p,b,fresh,backendMemoryFunction,true)
		backendMemorySentinel(t,p,b,z)
	})}
}

// Retained public metadata is compared independently of any current descriptor
// pointer; database/sql exposes values, not private ownership inspection hooks.
func backendMemoryRetainedMetadata(t *testing.T,rows *sql.Rows,want []*sql.ColumnType) {
	t.Helper()
	got,err:=rows.ColumnTypes();if err!=nil||len(got)!=len(want){t.Fatal(got,err)}
	for i:=range got{if got[i].Name()!=want[i].Name()||got[i].DatabaseTypeName()!=want[i].DatabaseTypeName()||!reflect.DeepEqual(got[i].ScanType(),want[i].ScanType()){t.Fatal("retained metadata changed",i,got[i],want[i])}}
}


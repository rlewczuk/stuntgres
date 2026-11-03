package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Closing both the loan and its dedicated pool retires the physical writer.
// Returning a loan to the observer's live pool would not publish its last batch.
func statioTableWork(t *testing.T,p *fixture.Pair,b fixture.Backend,settings []string,q string,want [][]any) {
	t.Helper()
	var database string
	if err:=b.DB.QueryRowContext(p.Ctx,"SELECT current_database()").Scan(&database);err!=nil {t.Fatal(err)}
	pool,raw,recorder:=fixture.DatabaseScenarioPool(t,p,b.PG,database,"")
	c,err:=pool.Conn(p.Ctx); if err!=nil {t.Fatal(err)}
	for _,s:=range settings {fixture.RunStep(t,p.Ctx,c,recorder,fixture.Exec(s,0))}
	fixture.RunStep(t,p.Ctx,c,recorder,fixture.Select(q,[]string{"k"},[]string{"INT4"},want))
	if err:=c.Close();err!=nil {t.Fatal(err)}
	if err:=pool.Close();err!=nil {t.Fatal(err)}
	if err:=raw.Close();err!=nil {t.Fatal(err)}
}

func statioTableIndexSum(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,oid int64) statioTableRow {
	t.Helper()
	r:=statioTableOne(t,p,b,e,oid)
	rows,err:=e.QueryContext(p.Ctx,"SELECT sum(idx_blks_read)::bigint,sum(idx_blks_hit)::bigint FROM pg_statio_all_indexes WHERE relid=$1::oid",oid)
	if err!=nil {t.Fatal(err)}; defer rows.Close()
	var read,hit sql.NullInt64
	if !rows.Next() {t.Fatal(rows.Err())}; if err:=rows.Scan(&read,&hit);err!=nil {t.Fatal(err)}
	if rows.Next() || rows.Err()!=nil {t.Fatal(rows.Err())}
	if read!=r.blocks[2] || hit!=r.blocks[3] {t.Fatal("shared index provider SUM",r,read,hit)}
	return r
}

func TestReferencePGStatioAllTablesCounters(t *testing.T) {
	for repeat:=range 2 {t.Run(fmt.Sprint(repeat),func(t *testing.T) {
		p:=fixture.NewScenario(t)
		for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
			a,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
			statioSequenceExec(t,p,b,a,"CREATE TABLE statio_table_counter(k integer,v integer)")
			fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_table_counter VALUES(1,10),(2,20)",2))
			statioSequenceExec(t,p,b,observer,"SET stats_fetch_consistency=none")
			oid:=statioSequenceOID(t,p,a,"statio_table_counter")
			r:=statioTableOne(t,p,b,observer,oid); statioTableHeap(t,b,r,0); statioTableAbsentToast(t,r)
			if r.blocks[2].Valid || r.blocks[3].Valid {t.Fatal("no-index SUM is NULL",r)}
			stmt,err:=a.PrepareContext(p.Ctx,"SELECT k FROM statio_table_counter");if err!=nil {t.Fatal(err)};stmt.Close()
			statioTableHeap(t,b,statioTableOne(t,p,b,observer,oid),0)
			for n:=int64(1);n<=3;n++ {
				statioTableWork(t,p,b,[]string{"SET track_io_timing=off"},"SELECT k FROM statio_table_counter ORDER BY k",[][]any{{int64(1)},{int64(2)}})
				statioTableHeap(t,b,statioTableOne(t,p,b,observer,oid),n)
			}
			if !b.PG {
				statioTableWork(t,p,b,[]string{"SET track_counts=off"},"SELECT k FROM statio_table_counter ORDER BY k",[][]any{{int64(1)},{int64(2)}})
				statioTableHeap(t,b,statioTableOne(t,p,b,observer,oid),3)
				statioTableWork(t,p,b,[]string{"SET track_counts=on"},"SELECT k FROM statio_table_counter ORDER BY k",[][]any{{int64(1)},{int64(2)}})
				statioTableHeap(t,b,statioTableOne(t,p,b,observer,oid),4)
			}
			statioSequenceExec(t,p,b,a,"CREATE TABLE statio_table_empty(k integer)")
			empty:=statioSequenceOID(t,p,a,"statio_table_empty")
			statioTableWork(t,p,b,nil,"SELECT k FROM statio_table_empty",[][]any{})
			statioTableHeap(t,b,statioTableOne(t,p,b,observer,empty),0)
			for _,q:=range []string{"CREATE INDEX statio_table_counter_i ON statio_table_counter USING hash(k)","CREATE INDEX statio_table_counter_unused ON statio_table_counter USING hash(v)"} {statioSequenceExec(t,p,b,a,q)}
			r=statioTableIndexSum(t,p,b,observer,oid)
			statioTableAbsentToast(t,r)
			if !r.blocks[2].Valid || !r.blocks[3].Valid {t.Fatal("existing zero indexes are numeric",r)}
			if !b.PG && (r.blocks[2] != (sql.NullInt64{Valid:true}) || r.blocks[3] != (sql.NullInt64{Valid:true})) {t.Fatal("existing zero-activity indexes yield numeric zero",r)}
			for n:=range int64(3) {
				statioTableWork(t,p,b,[]string{"SET enable_seqscan=off"},"SELECT k FROM statio_table_counter WHERE k=1",[][]any{{int64(1)}})
				r=statioTableIndexSum(t,p,b,observer,oid)
				if !b.PG && (r.blocks[2].Int64!=1 || r.blocks[3].Int64!=n) {t.Fatal("representative index page cold/hit",n,r)}
			}
			statioSequenceExec(t,p,b,a,"DROP INDEX statio_table_counter_unused")
			statioTableIndexSum(t,p,b,observer,oid)
			statioSequenceExec(t,p,b,a,"DROP INDEX statio_table_counter_i")
			r=statioTableIndexSum(t,p,b,observer,oid)
			if r.blocks[2].Valid || r.blocks[3].Valid {t.Fatal("last index drop restores NULL SUM",r)}
		})}
	})}
}

func TestReferencePGStatioAllTablesSnapshots(t *testing.T) {
	for _,mode:=range []string{"none","cache","snapshot"} {t.Run(mode,func(t *testing.T) {
		p:=fixture.NewScenario(t)
		for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
			a,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
			for _,q:=range []string{"CREATE TABLE statio_table_snap_a(k integer)","CREATE TABLE statio_table_snap_b(k integer)","INSERT INTO statio_table_snap_a VALUES(1)","INSERT INTO statio_table_snap_b VALUES(1)","CREATE INDEX statio_table_snap_i ON statio_table_snap_a USING hash(k)"} { if _,err:=a.ExecContext(p.Ctx,q);err!=nil {t.Fatal(err)} }
			aID:=statioSequenceOID(t,p,a,"statio_table_snap_a"); bID:=statioSequenceOID(t,p,a,"statio_table_snap_b")
			statioSequenceExec(t,p,b,observer,"SET stats_fetch_consistency="+mode)
			stmt,err:=observer.PrepareContext(p.Ctx,statioTableProbe);if err!=nil {t.Fatal(err)};defer stmt.Close()
			held:=fixture.HeldStatement{Executor:observer,Statement:stmt,SQL:statioTableProbe}
			statioSequenceExec(t,p,b,observer,"BEGIN");defer observer.ExecContext(context.Background(),"ROLLBACK")
			before:=statioTableOne(t,p,b,held,aID)
			statioTableAbsentToast(t,before)
			statioTableWork(t,p,b,nil,"SELECT k FROM statio_table_snap_a",[][]any{{int64(1)}})
			statioTableWork(t,p,b,nil,"SELECT k FROM statio_table_snap_b",[][]any{{int64(1)}})
			after:=statioTableOne(t,p,b,held,aID)
			statioTableAbsentToast(t,after)
			if mode!="none" && before!=after {t.Fatal("transaction reader retention",before,after)}
			wantA:=before.blocks[1].Int64; if mode=="none" {wantA++}; statioTableHeap(t,b,after,wantA)
			wantB:=int64(1);if mode=="snapshot" {wantB=0};statioTableHeap(t,b,statioTableOne(t,p,b,held,bID),wantB)
			fixture.RunStep(t,p.Ctx,observer,b.Recorder,fixture.Select("SELECT pg_stat_clear_snapshot()",[]string{"pg_stat_clear_snapshot"},[]string{"VOID"},[][]any{{""}}))
			statioTableHeap(t,b,statioTableOne(t,p,b,held,aID),before.blocks[1].Int64+1)
			statioTableHeap(t,b,statioTableOne(t,p,b,held,bID),1)
			statioTableIndexSum(t,p,b,observer,aID)
			statioSequenceExec(t,p,b,observer,"ROLLBACK")
		})}
	})}
}

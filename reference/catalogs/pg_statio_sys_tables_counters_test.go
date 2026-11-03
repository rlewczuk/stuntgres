package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioSysTablesCounters(t *testing.T) {
	var vectors [][]statioTableRow
	for repeat:=range 2 {t.Run(fmt.Sprint(repeat),func(t *testing.T) {
		p:=fixture.NewScenario(t)
		for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
			a,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
			statioSequenceExec(t,p,b,a,"CREATE TABLE statio_sys_counter_user(k integer)")
			fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_sys_counter_user VALUES(1),(2)",2))
			oid:=statioSequenceOID(t,p,a,"statio_sys_counter_user")
			statioSequenceExec(t,p,b,observer,"SET stats_fetch_consistency=none")
			held,stmt:=statioSysTableHeld(t,p,observer);defer stmt.Close()
			vector:=[]statioTableRow{}
			for n:=int64(0);n<3;n++ {
				if n>0 {statioTableWork(t,p,b,nil,"SELECT k FROM statio_sys_counter_user ORDER BY k",[][]any{{int64(1)},{int64(2)}})}
				r:=statioTableOne(t,p,b,observer,oid);statioTableHeap(t,b,r,n);statioTableAbsentToast(t,r)
				if r.schema!="public" || r.name!="statio_sys_counter_user" || r.blocks[2].Valid || r.blocks[3].Valid {t.Fatal("actual no-index user identity",r)}
				for _,e:=range []fixture.Executor{observer,held} {statioSysTableEmpty(t,p,b,e,oid)}
				// Normalize only allocator identity for repeated fresh-driver proof.
				r.oid=0;vector=append(vector,r)
			}
			if !b.PG {vectors=append(vectors,vector)}
			statioSequenceExec(t,p,b,observer,"BEGIN");defer observer.ExecContext(context.Background(),"ROLLBACK")
			statioSequenceExec(t,p,b,observer,"SET LOCAL stats_fetch_consistency=snapshot")
			sys:=statioSysTablePositive(t,p,b,held,1259)
			if all:=statioTableOne(t,p,b,observer,1259);all!=sys {t.Fatal("same shared snapshot counter/NULL vector",all,sys)}
			statioSequenceExec(t,p,b,observer,"ROLLBACK")
		})}
	})}
	if len(vectors)!=2 || !reflect.DeepEqual(vectors[0],vectors[1]) {t.Fatal("deterministic independent driver operation vectors",vectors)}
}

func TestReferencePGStatioSysTablesSnapshots(t *testing.T) {
	for _,mode:=range []string{"none","cache","snapshot"} {t.Run(mode,func(t *testing.T) {
		p:=fixture.NewScenario(t)
		for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
			a,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
			statioSequenceExec(t,p,b,a,"CREATE TABLE statio_sys_snapshot_user(k integer)")
			fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_sys_snapshot_user VALUES(1),(2)",2))
			oid:=statioSequenceOID(t,p,a,"statio_sys_snapshot_user")
			statioSequenceExec(t,p,b,observer,"SET stats_fetch_consistency="+mode)
			held,stmt:=statioSysTableHeld(t,p,observer);defer stmt.Close()
			allStmt,err:=observer.PrepareContext(p.Ctx,statioTableProbe);if err!=nil {t.Fatal(err)};defer allStmt.Close()
			allHeld:=fixture.HeldStatement{Executor:observer,Statement:allStmt,SQL:statioTableProbe}
			statioSequenceExec(t,p,b,observer,"BEGIN");defer observer.ExecContext(context.Background(),"ROLLBACK")
			before:=statioTableOne(t,p,b,allHeld,oid);statioTableHeap(t,b,before,0)
			sysBefore:=statioSysTablePositive(t,p,b,held,1259)
			if mode!="none" {if all:=statioTableOne(t,p,b,observer,1259);all!=sysBefore {t.Fatal("positive ALL/SYS reader equality",all,sysBefore)}}
			statioTableWork(t,p,b,nil,"SELECT k FROM statio_sys_snapshot_user ORDER BY k",[][]any{{int64(1)},{int64(2)}})
			after:=statioTableOne(t,p,b,allHeld,oid)
			if mode!="none" && before!=after {t.Fatal("held statements respect transaction reader",before,after)}
			want:=int64(0);if mode=="none" {want=1};statioTableHeap(t,b,after,want)
			statioSysTableEmpty(t,p,b,held,oid)
			if mode!="none" {if fresh:=statioSysTablePositive(t,p,b,held,1259);fresh!=sysBefore {t.Fatal("positive system snapshot retained",fresh,sysBefore)}}
			fixture.RunStep(t,p.Ctx,observer,b.Recorder,fixture.Select("SELECT pg_stat_clear_snapshot()",[]string{"pg_stat_clear_snapshot"},[]string{"VOID"},[][]any{{""}}))
			fresh:=statioTableOne(t,p,b,allHeld,oid);statioTableHeap(t,b,fresh,1);statioTableAbsentToast(t,fresh)
			statioSysTableEmpty(t,p,b,observer,oid)
			statioSysTablePositive(t,p,b,held,1259)
			statioSequenceExec(t,p,b,observer,"ROLLBACK")
		})}
	})}
}

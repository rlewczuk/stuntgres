package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferencePGStatioUserTablesSnapshots(t *testing.T) {
	for _,mode:=range []string{"none","cache","snapshot"} {t.Run(mode,func(t *testing.T) {
		p:=fixture.NewScenario(t)
		for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
			a,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
			for _,q:=range []string{"CREATE TABLE statio_user_snap_a(k integer)","CREATE TABLE statio_user_snap_b(k integer)","ALTER TABLE statio_user_snap_a SET (autovacuum_enabled=false)","ALTER TABLE statio_user_snap_b SET (autovacuum_enabled=false)","CREATE INDEX statio_user_snap_i ON statio_user_snap_a USING hash(k)"} { if _,err:=a.ExecContext(p.Ctx,q);err!=nil {t.Fatal(err)} }
			aID:=statioSequenceOID(t,p,a,"statio_user_snap_a"); bID:=statioSequenceOID(t,p,a,"statio_user_snap_b")
			// Build the index while empty, then publish each seed before any
			// Reader demand. Setup cannot masquerade as later read activity.
			statioUserTableSeed(t,p,b,aID,"INSERT INTO statio_user_snap_a VALUES(1)",1)
			statioUserTableSeed(t,p,b,bID,"INSERT INTO statio_user_snap_b VALUES(1)",1)
			statioSequenceExec(t,p,b,a,"SET stats_fetch_consistency=none")
			baselineA:=statioUserTableOne(t,p,b,a,aID)
			baselineB:=statioUserTableOne(t,p,b,a,bID)
			statioSequenceExec(t,p,b,observer,"SET stats_fetch_consistency="+mode)
			statioSequenceExec(t,p,b,observer,"BEGIN");defer observer.ExecContext(context.Background(),"ROLLBACK")
			stmt,err:=observer.PrepareContext(p.Ctx,statioUserTableProbe);if err!=nil {t.Fatal(err)};defer stmt.Close()
			held:=fixture.HeldStatement{Executor:observer,Statement:stmt,SQL:statioUserTableProbe}
			var stamp sql.NullTime
			if err:=observer.QueryRowContext(p.Ctx,"SELECT pg_stat_get_snapshot_timestamp()").Scan(&stamp);err!=nil || stamp.Valid {t.Fatal("Prepare initialized statistics snapshot",stamp,err)}
			before:=statioUserTableOne(t,p,b,held,aID)
			statioTableAbsentToast(t,before)
			if before!=baselineA {t.Fatal("initial held snapshot must match published baseline",baselineA,before)}
			if err:=observer.QueryRowContext(p.Ctx,"SELECT pg_stat_get_snapshot_timestamp()").Scan(&stamp);err!=nil || stamp.Valid!=(mode=="snapshot") {t.Fatal("first demand snapshot timestamp",mode,stamp,err)}
			statioTableWork(t,p,b,nil,"SELECT k FROM statio_user_snap_a",[][]any{{int64(1)}})
			statioTableWork(t,p,b,nil,"SELECT k FROM statio_user_snap_b",[][]any{{int64(1)}})
			freshA:=statioUserTablePublished(t,p,b,baselineA)
			freshB:=statioUserTablePublished(t,p,b,baselineB)
			after:=statioUserTableOne(t,p,b,held,aID)
			statioTableAbsentToast(t,after)
			if mode!="none" && before!=after {t.Fatal("transaction reader retention",before,after)}
			wantA:=before.blocks[1].Int64; if mode=="none" {wantA++}; statioTableHeap(t,b,after,wantA)
			expectedA:=before;if mode=="none" {expectedA=freshA}
			if after!=expectedA {t.Fatal("none sees published delta; cache/snapshot retain old vector",mode,before,freshA,after)}
			wantB:=int64(1);if mode=="snapshot" {wantB=0}
			gotB:=statioUserTableOne(t,p,b,held,bID);statioTableHeap(t,b,gotB,wantB)
			expectedB:=freshB;if mode=="snapshot" {expectedB=baselineB}
			if gotB!=expectedB {t.Fatal("later-demanded cache sees fresh vector; snapshot retains whole old set",mode,baselineB,freshB,gotB)}
			fixture.RunStep(t,p.Ctx,observer,b.Recorder,fixture.Select("SELECT pg_stat_clear_snapshot()",[]string{"pg_stat_clear_snapshot"},[]string{"VOID"},[][]any{{""}}))
			clearedA:=statioUserTableOne(t,p,b,held,aID)
			clearedB:=statioUserTableOne(t,p,b,held,bID)
			statioTableHeap(t,b,clearedA,before.blocks[1].Int64+1)
			statioTableHeap(t,b,clearedB,1)
			if clearedA!=freshA || clearedB!=freshB {t.Fatal("clear exposes both published vectors without resetting history",freshA,clearedA,freshB,clearedB)}
			statioUserTableIndexSum(t,p,b,observer,aID)
			// The held statement must recapture the current effective policy,
			// rather than retain Prepare-time or first-execution settings.
			statioSequenceExec(t,p,b,observer,"SET LOCAL stats_fetch_consistency=none")
			statioTableWork(t,p,b,nil,"SELECT k FROM statio_user_snap_a",[][]any{{int64(1)}})
			newA:=statioUserTablePublished(t,p,b,freshA)
			settingsA:=statioUserTableOne(t,p,b,held,aID)
			statioTableHeap(t,b,settingsA,2)
			if settingsA!=newA {t.Fatal("held execution must recapture none and expose new published vector",freshA,newA,settingsA)}
			statioSequenceExec(t,p,b,observer,"SET LOCAL stats_fetch_consistency=snapshot")
			fixture.StatisticsClearSnapshot(t,p,b,observer)
			pinned:=statioUserTableCompanions(t,p,b,observer,aID)
			if pinned!=newA {t.Fatal("new snapshot starts at current published vector",newA,pinned)}
			statioTableWork(t,p,b,nil,"SELECT k FROM statio_user_snap_a",[][]any{{int64(1)}})
			lastA:=statioUserTablePublished(t,p,b,newA)
			if got:=statioUserTableOne(t,p,b,held,aID);got!=pinned {t.Fatal("held policy change did not pin snapshot",got,pinned)}
			fixture.StatisticsClearSnapshot(t,p,b,observer)
			refreshed:=statioUserTableCompanions(t,p,b,observer,aID)
			statioTableHeap(t,b,refreshed,3)
			if refreshed!=lastA {t.Fatal("clear must expose distinguishable latest published vector",pinned,lastA,refreshed)}
			if !b.PG {
				writer,retire:=fixture.StatisticsReportingConn(t,p,b)
				statioSequenceExec(t,p,b,writer,"BEGIN")
				fixture.RunStep(t,p.Ctx,writer,b.Recorder,fixture.Select("SELECT k FROM statio_user_snap_a",[]string{"k"},[]string{"INT4"},[][]any{{int64(1)}}))
				fixture.RunStep(t,p.Ctx,writer,b.Recorder,fixture.Exec("INSERT INTO statio_user_snap_a VALUES(2)",1))
				fixture.StatisticsClearSnapshot(t,p,b,observer)
				statioTableHeap(t,b,statioUserTableOne(t,p,b,held,aID),3)
				// Clear does not disclose a private writer. Completed read work
				// survives tuple rollback and publishes on physical retirement.
				statioSequenceExec(t,p,b,writer,"ROLLBACK")
				retire()
				fixture.StatisticsClearSnapshot(t,p,b,observer)
				statioTableHeap(t,b,statioUserTableCompanions(t,p,b,observer,aID),4)
				fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT k FROM statio_user_snap_a",[]string{"k"},[]string{"INT4"},[][]any{{int64(1)}}))
			}
			statioSequenceExec(t,p,b,observer,"ROLLBACK")
		})}
	})}
}

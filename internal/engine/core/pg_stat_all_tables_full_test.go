package core_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/util"
	"testing"
)

func TestPGStatAllTablesFullRewrite(t *testing.T) {
	t.Run("actual_retry_versus_candidate_reuse",tablesFullRewriteRetry)
	for _,tc:=range []struct{name string;empty,disabled,indexed bool}{
		{"fresh",false,false,false},
		{"empty",true,false,false},
		{"disabled",false,true,false},
		{"index_cache_reconstruction",false,false,true},
	} {
		t.Run(tc.name,func(t *testing.T){
			e:=engine.New()
			setup:=enginetest.StartSession(t,e,nil)
			statsFunctionExec(t,setup,"CREATE TABLE sat_full(n integer)")
			if !tc.empty {statsFunctionExec(t,setup,"INSERT INTO sat_full VALUES(1),(NULL),(3)")}
			if tc.indexed {statsFunctionExec(t,setup,"SET track_counts=off","CREATE INDEX sat_full_index ON sat_full(n)")}
			statsEnd(t,setup)
			key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_full")}
			before:=e.Statistics.Read(key)
			writer:=enginetest.StartSession(t,e,nil)
			if tc.disabled {statsFunctionExec(t,writer,"SET track_counts=off")}
			statsFunctionExec(t,writer,"VACUUM FULL sat_full")
			statsEnd(t,writer)
			scans,tuples:=int64(1),int64(3)
			if tc.empty {tuples=0}
			if tc.disabled {scans,tuples=0,0}
			tablesScanWant(t,e,key,scans,tuples,0)
			after:=e.Statistics.Read(key)
			reader:=enginetest.StartSession(t,e,nil)
			rows:=enginetest.Tuples(enginetest.SessionSQL(t,reader,"SELECT n FROM sat_full ORDER BY n NULLS LAST"))
			wantRows:=3
			if tc.empty {wantRows=0}
			if len(rows)!=wantRows||!tc.empty&&(rows[0][0]!=int64(1)||rows[1][0]!=int64(3)||rows[2][0]!=nil) {t.Fatal("FULL changed physical data",rows)}
			if after.Inserts!=before.Inserts||after.Updates!=before.Updates||after.Deletes!=before.Deletes||after.VacuumCount!=before.VacuumCount||!after.LastVacuum.Equal(before.LastVacuum)||after.VacuumTime!=before.VacuumTime||after.IndexScans!=0 {t.Fatal("rewrite manufactured tuple/ordinary maintenance/index events",before,after)}
		})
	}
	t.Run("prepare_and_rejected_transaction",func(t *testing.T){
		e:=engine.New()
		s:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,s,"CREATE TABLE sat_full_admission(n integer)","INSERT INTO sat_full_admission VALUES(1),(2),(3)")
		_,_=enginetest.PreparedDescription(t,e,s,"VACUUM FULL sat_full_admission")
		statsFunctionExec(t,s,"BEGIN")
		if err:=statsFunctionSQL(t,s,"VACUUM FULL sat_full_admission");err==nil {t.Fatal("FULL inside a transaction was admitted")}
		statsFunctionExec(t,s,"ROLLBACK")
		key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_full_admission")}
		statsEnd(t,s)
		tablesScanWant(t,e,key,0,0,0)
	})
	t.Run("interrupted_primary_source_not_copy_or_completion",func(t *testing.T){
		e:=engine.New()
		setup:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,setup,"CREATE TABLE sat_full_prefix(n integer)","INSERT INTO sat_full_prefix VALUES"+strings.Repeat("(1),",128)+"(1)")
		statsEnd(t,setup)
		key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_full_prefix")}
		writer:=enginetest.StartSession(t,e,nil)
		pause:=util.NewExecutionPause("vacuum.inspect")
		defer pause.Resume()
		ctx,cancel:=context.WithCancel(util.ActivateExecutionPause(util.WithExecutionPause(context.Background(),pause)))
		defer cancel()
		await:=enginetest.ExecuteAsync(t,e,writer,ctx,"VACUUM FULL sat_full_prefix")
		ginCoreAwaitPause(t,pause)
		cancel()
		pause.Resume()
		if _,err:=await();!errors.Is(err,context.Canceled) {t.Fatal("actual FULL interruption",err)}
		statsEnd(t,writer)
		tablesScanWant(t,e,key,1,128,0)
		if f:=e.Statistics.Read(key);f.VacuumCount!=0||!f.LastVacuum.IsZero()||f.VacuumTime!=0 {t.Fatal("failed FULL completed ordinary maintenance",f)}
	})
}

func tablesFullRewriteRetry(t *testing.T) {
	for _,releaseBeforePublication:=range []bool{false,true} {
		name:="reuse_candidate"
		if releaseBeforePublication {name="restart_source"}
		t.Run(name,func(t *testing.T) {
			e:=engine.New()
			setup:=enginetest.StartSession(t,e,nil)
			statsFunctionExec(t,setup,"CREATE TABLE sat_full_anchor(n integer)","INSERT INTO sat_full_anchor VALUES(1)","CREATE TABLE sat_full_retry(n integer)","INSERT INTO sat_full_retry VALUES"+strings.Repeat("(1),",128)+"(1)")
			statsEnd(t,setup)
			key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_full_retry")}
			// A real database-wide repeatable-read capture owns the target's
			// old roots without taking an SQL lock on that target relation.
			reader:=enginetest.StartSession(t,e,nil)
			statsFunctionExec(t,reader,"SET track_counts=off","BEGIN ISOLATION LEVEL REPEATABLE READ","SELECT n FROM sat_full_anchor")
			reader.Transactions.Mu.Lock()
			_,captured:=reader.Transactions.Roots[key.Object.Address]
			reader.Transactions.Mu.Unlock()
			if !captured {t.Fatal("anchor did not capture real target roots")}
			deleter:=enginetest.StartSession(t,e,nil)
			statsFunctionExec(t,deleter,"SET track_counts=off","DELETE FROM sat_full_retry")
			statsEnd(t,deleter)
			writer:=enginetest.StartSession(t,e,nil)
			pause:=util.NewExecutionPause("vacuum.inspect")
			defer pause.Resume()
			ctx:=util.ActivateExecutionPause(util.WithExecutionPause(context.Background(),pause))
			await:=enginetest.ExecuteAsync(t,e,writer,ctx,"VACUUM FULL sat_full_retry")
			ginCoreAwaitPause(t,pause)
			if releaseBeforePublication {
				// Removing the genuine old-snapshot guard changes the captured
				// retirement decision and requires another real source traversal.
				statsFunctionExec(t,reader,"ROLLBACK")
			}
			pause.Resume()
			if _,err:=await();err!=nil {t.Fatal("FULL retry publication",err)}
			if !releaseBeforePublication {statsFunctionExec(t,reader,"ROLLBACK")}
			statsEnd(t,writer)
			scans,tuples:=int64(1),int64(129)
			if releaseBeforePublication {scans,tuples=2,258}
			tablesScanWant(t,e,key,scans,tuples,0)
			if f:=e.Statistics.Read(key);f.VacuumCount!=0||!f.LastVacuum.IsZero() {t.Fatal("retry manufactured ordinary vacuum completion",f)}
		})
	}
}

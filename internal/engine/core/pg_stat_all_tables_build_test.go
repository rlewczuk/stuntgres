package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
	"testing"
)

func TestPGStatAllTablesIndexBuild(t *testing.T) {
	t.Run("prepare_rejection_and_incremental_maintenance",tablesIndexBuildAdmission)
	for _, method := range []string{"hash", "btree", "bloom", "brin"} {
		t.Run(method, func(t *testing.T) {
			e := engine.New()
			setup := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, setup, "CREATE TABLE sat_build(n integer)", "INSERT INTO sat_build VALUES(1),(NULL),(3)")
			if method == "bloom" {
				statsFunctionExec(t, setup, "CREATE EXTENSION bloom")
			}
			statsEnd(t, setup)
			key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "sat_build")}
			writer := enginetest.StartSession(t, e, nil)
			// HASH rejects partial definitions at admission; its NULL key
			// still distinguishes three heap fetches from non-NULL entries.
			// Other admitted methods additionally exclude all predicate members.
			definition:="CREATE INDEX sat_build_index ON sat_build USING "+method+"(n)"
			if method!="hash" {definition+=" WHERE n>99"}
			statsFunctionExec(t, writer, definition)
			statsEnd(t, writer)
			tablesScanWant(t, e, key, 1, 3, 0)
			if f := e.Statistics.Read(key); f.Inserts != 3 || f.AnalyzeCount != 0 || f.VacuumCount != 0 {
				t.Fatal("index build changed unrelated event families", f)
			}
		})
	}
	for _, end := range []string{"ROLLBACK", "COMMIT"} {
		t.Run(end, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s, "CREATE TABLE sat_build_tx(n integer)", "INSERT INTO sat_build_tx VALUES(1),(NULL),(3)", "BEGIN", "CREATE INDEX sat_build_tx_index ON sat_build_tx(n)", end)
			key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "sat_build_tx")}
			statsEnd(t, s)
			tablesScanWant(t, e, key, 1, 3, 0)
		})
	}
	t.Run("empty_and_disabled", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "CREATE TABLE sat_build_empty(n integer)", "CREATE INDEX sat_build_empty_a ON sat_build_empty(n)", "SET track_counts=off", "CREATE INDEX sat_build_empty_b ON sat_build_empty USING hash(n)")
		key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "sat_build_empty")}
		statsEnd(t, s)
		tablesScanWant(t, e, key, 1, 0, 0)
	})
	t.Run("interrupted_actual_build_prefix", func(t *testing.T) {
		b := indexProgressPrepare(t, 3, false)
		key := statistics.Key{Object: b.e.Catalog.Domain.Ref(b.e.CatalogRead().Epoch(), b.parent)}
		done := b.run()
		joined := false
		defer func() {
			close(b.gate.release)
			if !joined {
				b.op.Cancel(context.Canceled)
				<-done
			}
		}()
		cancelled := false
		for !joined {
			select {
			case step := <-b.gate.steps:
				if step.kind == "visited" && step.n == 2 {
					b.op.Cancel(context.Canceled)
					cancelled = true
				}
				close(step.ack)
			case err := <-done:
				joined = true
				if !cancelled || !errors.Is(err, context.Canceled) {
					t.Fatal("actual utility interruption", cancelled, err)
				}
			}
		}
		statsEnd(t, b.worker)
		tablesScanWant(t, b.e, key, 1, 2, 0)
		if _, exists := b.e.CatalogRead().Relation(b.target); exists {
			t.Fatal("interrupted build published index")
		}
	})
}

// Description/admission and incremental index maintenance are not heap builds.
func tablesIndexBuildAdmission(t *testing.T) {
	e:=engine.New()
	s:=enginetest.StartSession(t,e,nil)
	statsFunctionExec(t,s,"CREATE TABLE sat_build_admission(n integer)","INSERT INTO sat_build_admission VALUES(1),(NULL),(3)","SET track_counts=off","CREATE INDEX sat_build_existing ON sat_build_admission(n)","SET track_counts=on")
	_,_=enginetest.PreparedDescription(t,e,s,"CREATE INDEX sat_build_prepared ON sat_build_admission(n)")
	if err:=statsFunctionSQL(t,s,"CREATE INDEX sat_build_existing ON sat_build_admission(n)");err==nil {t.Fatal("duplicate index admission succeeded")}
	key:=statistics.Key{Object:enginetest.RoleTableRef(t,e,"sat_build_admission")}
	statsFunctionExec(t,s,"INSERT INTO sat_build_admission VALUES(4)")
	statsEnd(t,s)
	tablesScanWant(t,e,key,0,0,0)
	if f:=e.Statistics.Read(key);f.Inserts!=4 {t.Fatal("admission or per-row index maintenance lost tuples",f)}
}

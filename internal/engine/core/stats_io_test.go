package core_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
)

// Exercise the actual captured runtime, not a stand-in provider or a second
// reader. Statement admission serializes access to the physical session reader.
func statIOCaptureReset(t *testing.T, e *core.Engine, s *session.Session, table ids.ObjectAddress, tableFirst bool) time.Time {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err!=nil { t.Fatal(err) }
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err!=nil { t.Fatal(err) }
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err!=nil { scope.Finish(err); t.Fatal(err) }
	runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine:e, Auth:auth, Settings:op.EffectiveSettings()})
	if tableFirst {
		e.Mu.Lock()
		runtime.TableStatistics(table)
		e.Mu.Unlock()
	}
	at := runtime.IOStatisticsReset()
	if err:=scope.Finish(nil); err!=nil { t.Fatal(err) }
	return at
}

func TestPGStatIOSharedReader(t *testing.T) {
	e, s, _, _, key, _ := sharedStatsFixture(t)
	first, later := time.Unix(100,0).UTC(), time.Unix(200,0).UTC()
	owned := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
	for _, mode := range []string{"none", "cache", "snapshot"} {
		statsFunctionExec(t, s, "SET stats_fetch_consistency='"+mode+"'", "BEGIN")
		e.Statistics.ResetShared(statistics.SharedIO, first)
		if at:=statIOCaptureReset(t,e,s,key.Object.Address,false); !at.Equal(first) { t.Fatal("initial IO cut",mode,at) }
		if owned.FieldByName("full").Bool()!=(mode=="snapshot") || owned.FieldByName("stamp").IsZero()!=(mode!="snapshot") { t.Fatal("physical snapshot ownership",mode) }
		if mode=="cache" && owned.FieldByName("kinds").Uint()&(1<<statistics.SharedIO)==0 { t.Fatal("IO family not cached by physical reader") }
		e.Statistics.ResetShared(statistics.SharedIO,later)
		e.Statistics.ResetShared(statistics.SharedWAL,time.Unix(300,0).UTC())
		want:=first
		if mode=="none" { want=later }
		if at:=statIOCaptureReset(t,e,s,key.Object.Address,false); !at.Equal(want) { t.Fatal("actual retained family",mode,at,want) }
		resetStatsExec(t,s,"SELECT pg_stat_clear_snapshot()")
		if at:=statIOCaptureReset(t,e,s,key.Object.Address,false); !at.Equal(later) { t.Fatal("clear/current IO not WAL",mode,at) }
		statsFunctionExec(t,s,"ROLLBACK")
		if owned.FieldByName("full").Bool() || owned.FieldByName("kinds").Uint()!=0 || !owned.FieldByName("stamp").IsZero() { t.Fatal("terminal retained physical reader") }
	}
	statsFunctionExec(t,s,"SET stats_fetch_consistency='snapshot'","BEGIN")
	e.Statistics.ResetShared(statistics.SharedIO,first)
	if at:=statIOCaptureReset(t,e,s,key.Object.Address,true); !at.Equal(first) { t.Fatal("table-first IO cut",at) }
	e.Statistics.ResetShared(statistics.SharedIO,later)
	if at:=statIOCaptureReset(t,e,s,key.Object.Address,false); !at.Equal(first) { t.Fatal("table-first snapshot omitted IO family",at) }
	resetStatsExec(t,s,"SELECT pg_stat_clear_snapshot()")
	if at:=statIOCaptureReset(t,e,s,key.Object.Address,false); !at.Equal(later) { t.Fatal("table-first clear",at) }
	statsFunctionExec(t,s,"ROLLBACK")
}

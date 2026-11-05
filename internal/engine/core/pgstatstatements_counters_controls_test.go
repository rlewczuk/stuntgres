package core_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
)

func TestPgStatStatementsCountersControls(t *testing.T) {
	for _, replacement := range []bool{false,true} {
		t.Run(map[bool]string{false:"SQL_reset",true:"native_restore"}[replacement],func(t *testing.T) {
			e := engine.New()
			s,peer := enginetest.StartSession(t,e,nil),enginetest.StartSession(t,e,nil)
			for _, q := range []string{"SET pg_stat_statements.track='none'","CREATE EXTENSION pg_stat_statements","CREATE TABLE counter_control(k integer)","INSERT INTO counter_control VALUES(1),(2)","SET pg_stat_statements.track_utility=off","SET pg_stat_statements.track_planning=off","SET pg_stat_statements.track='top'"} { pgssOwnerExec(t,e,s,q) }
			pgssOwnerExec(t,e,peer,"SET pg_stat_statements.track='none'")
			const query = "SELECT k FROM counter_control AS counter_control_owner"
			pgssResetOwnerDrain(t,e,s,query,2)
			held := pgssPhaseEntry(t,e,"counter_control_owner")
			if held.Counters.SharedRead+held.Counters.SharedHit != 1 { t.Fatal("missing positive pre-control work",held) }
			var dump bytes.Buffer
			if err := engine.Dump(context.Background(),e,&dump); err != nil { t.Fatal(err) }
			portal,command := pgssCounterPortal(t,e,s,query)
			binding := core.PayloadOf(s).PreparedSQL.Binding("counter_portal")
			resource,accepted := binding.Resource,*binding.Folds
			authority := core.CursorAuthority(core.PortalProducer(portal))
			command(func(scope *core.StatementScope) error {
				_,err := scope.ExecuteProtocolPortal("counter_portal",1,func([]session.ProtocolField) error { return nil },nil,nil,core.CopyProtocolHooks{})
				return err
			})
			pending := core.PortalStatementCounters(portal)
			if pending.SharedRead+pending.SharedHit != 1 || !core.PortalStatementDetached(portal) { t.Fatal("actual pending executor/ancestry",pending) }
			before := e.Cluster.StatementStatistics.Snapshot()
			info := e.Cluster.StatementStatistics.Info()
			failure := errors.New("counter replacement reader")
			if err := engine.Restore(context.Background(),e,&pgssCounterReadFailure{err:failure}); !errors.Is(err,failure) { t.Fatal("failed replacement diagnostic",err) }
			after := e.Cluster.StatementStatistics.Snapshot()
			beforeByKey := make(map[statistics.StatementKey]statistics.StatementEntry,len(before))
			for _, entry := range before { beforeByKey[entry.Key] = entry }
			afterByKey := make(map[statistics.StatementKey]statistics.StatementEntry,len(after))
			for _, entry := range after { afterByKey[entry.Key] = entry }
			if !reflect.DeepEqual(beforeByKey,afterByKey) || info != e.Cluster.StatementStatistics.Info() || binding.Resource != resource || binding.Folds.Generation != accepted.Generation || core.PortalStatementCounters(portal) != pending { t.Fatal("failed replacement mutated real history/resource") }
			if replacement {
				if err := engine.Restore(context.Background(),e,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
				if err := core.PortalClose(portal,context.Background()); err != nil { t.Fatal(err) }
				if len(e.Cluster.StatementStatistics.Snapshot()) != 0 { t.Fatal("old frame republished restored history") }
			} else {
				pgssResetOwnerDrain(t,e,peer,"SELECT pg_stat_statements_reset()",1)
				command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("counter_portal") })
				entry := pgssPhaseEntry(t,e,"counter_control_owner")
				if entry.Execution.Count != 1 || entry.Rows != 1 || entry.Counters != pending { t.Fatal("SQL reset lost eligible later actual terminal",entry,pending) }
			}
			if resource.Root != nil || binding.Folds != nil || authority.Host != nil || held.Counters.SharedRead+held.Counters.SharedHit != 1 { t.Fatal("control retained resources or mutated detached snapshot") }
		})
	}
}

type pgssCounterReadFailure struct { err error }
func (r *pgssCounterReadFailure) Read([]byte) (int,error) { return 0,r.err }

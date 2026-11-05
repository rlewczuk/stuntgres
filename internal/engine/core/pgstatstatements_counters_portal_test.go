package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
)

func TestPgStatStatementsCountersPortal(t *testing.T) {
	for _, terminal := range []string{"unused","partial","resumed","exhausted"} {
		t.Run(terminal,func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t,e,nil)
			for _, q := range []string{"SET pg_stat_statements.track='none'","CREATE TABLE counter_portal_data(k integer)","INSERT INTO counter_portal_data VALUES(1),(2)","SET pg_stat_statements.track_utility=off","SET pg_stat_statements.track_planning=on","SET pg_stat_statements.track='top'"} { pgssOwnerExec(t,e,s,q) }
			const query = "SELECT k FROM counter_portal_data AS counter_portal_owner"
			portal,command := pgssCounterPortal(t,e,s,query)
			binding := core.PayloadOf(s).PreparedSQL.Binding("counter_portal")
			definition := core.PayloadOf(s).PreparedSQL.Lookup("counter_definition")
			resource,root,accepted := binding.Resource,binding.Resource.Root,*binding.Folds
			analysis,foldOwner := definition.Description.Analysis,definition.Description.Folds
			producer := core.PortalProducer(portal)
			authority := core.CursorAuthority(producer)
			lease := producer.Source.ReadLease
			if pgssPhaseEntry(t,e,"counter_portal_owner").Planning.Count != 1 || core.PortalStatementCounters(portal) != (statistics.StatementCounters{}) { t.Fatal("Parse/Bind read work or lost actual planning") }
			rows := int64(0)
			execute := func(limit uint32) {
				command(func(scope *core.StatementScope) error {
					_,err := scope.ExecuteProtocolPortal("counter_portal",limit,func([]session.ProtocolField) error { rows++; return nil },nil,nil,core.CopyProtocolHooks{})
					return err
				})
				if binding.Resource != resource || resource.Root != root || binding.Folds.Generation != accepted.Generation || definition.Description.Analysis != analysis || definition.Description.Folds != foldOwner || !core.PortalStatementDetached(portal) { t.Fatal("continuation recomputed accepted graph/folds or retained lexical ancestry") }
			}
			if terminal != "unused" { execute(1) }
			if terminal == "resumed" || terminal == "exhausted" { execute(0) }
			if terminal == "exhausted" { execute(0) }
			entry := pgssPhaseEntry(t,e,"counter_portal_owner")
			if entry.Execution.Count != 0 || entry.Counters != (statistics.StatementCounters{}) { t.Fatal("transport demand prematurely published executor",entry) }
			want := statistics.StatementCounters{}
			if terminal != "unused" { want.SharedRead = 1 }
			if got := core.PortalStatementCounters(portal); got != want { t.Fatal("resume/exhausted output reopened storage",terminal,got,want) }
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("counter_portal") })
			entry = pgssPhaseEntry(t,e,"counter_portal_owner")
			if entry.Execution.Count != 1 || entry.Rows != rows || entry.Counters != want || resource.Root != nil || binding.Folds != nil || authority.Host != nil || !binding.Retired { t.Fatal("normal terminal actual work/resource disposal",entry,rows,want) }
			lease.Open()
			lease.Close()
			lease.Close()
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("counter_definition") })
			if after := pgssPhaseEntry(t,e,"counter_portal_owner"); after != entry || definition.Description.Analysis != analysis || definition.Description.Folds != foldOwner { t.Fatal("definition close/detached capture mutated history/backing",after,entry) }
		})
	}
}

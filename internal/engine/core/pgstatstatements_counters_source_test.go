package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

func pgssCounterPortal(t *testing.T, e *core.Engine, s *session.Session, query string) (*core.ProtocolPortal, func(func(*core.StatementScope) error)) {
	t.Helper()
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		op, _, err := s.AcquireCommand(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartProtocolCommand(op, cycle)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if !scope.Finished {
				scope.Finish(errors.New("counter owner assertion interrupted command"))
			}
		}()
		err = fn(scope)
		if err = scope.Finish(err); err != nil {
			t.Fatal(err)
		}
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("counter_definition",query,nil)
		return err
	})
	command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("counter_portal","counter_definition",&enginetest.ProtocolReader{Data: []byte{0,0,0,0,0,0}})
		return err
	})
	binding := core.PayloadOf(s).PreparedSQL.Binding("counter_portal")
	portal := core.ProtocolPortalOf(binding)
	if portal == nil || core.PortalProducer(portal) == nil || binding.Resource.Root == nil || binding.Folds == nil {
		t.Fatal("missing actual initialized graph/folds/producer")
	}
	return portal,command
}

func TestPgStatStatementsCountersSource(t *testing.T) {
	for _, counts := range []string{"on","off"} {
		t.Run("cumulative_reset_and_counts_"+counts,func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t,e,nil)
			for _, query := range []string{"SET pg_stat_statements.track='none'","CREATE TABLE counter_source(k integer)","INSERT INTO counter_source VALUES(1),(2)","SET track_counts="+counts,"SET pg_stat_statements.track_utility=off","SET pg_stat_statements.track_planning=on","SET pg_stat_statements.track='top'"} {
				pgssOwnerExec(t,e,s,query)
			}
			const query = "SELECT k FROM counter_source AS counter_source_authority"
			portal,command := pgssCounterPortal(t,e,s,query)
			binding := core.PayloadOf(s).PreparedSQL.Binding("counter_portal")
			root,folds := binding.Resource.Root,*binding.Folds
			producer := core.PortalProducer(portal)
			source := producer.Source
			lease,authority := source.ReadLease,core.CursorAuthority(producer)
			if lease == nil || authority == nil || !core.PortalStatementDetached(portal) {
				t.Fatal("capture/ancestry missing")
			}
			generation := core.SourceReadGeneration(lease)
			e.Statistics.Reset(time.Now())
			if e.Statistics.Generation() == generation { t.Fatal("cumulative generation did not advance") }
			if core.PortalStatementCounters(portal) != (statistics.StatementCounters{}) { t.Fatal("Bind invented work") }
			peer := enginetest.StartSession(t,e,nil)
			pgssOwnerExec(t,e,peer,"CREATE TEMP TABLE counter_foreign_temp(k integer)")
			read := e.CatalogRead()
			var tempRef ids.ObjectRef
			var tempNode ids.OID
			for _, descriptor := range read.Descriptors() {
				if relation,ok := descriptor.(*catalog.Relation); ok && relation.Name() == "counter_foreign_temp" {
					tempRef = e.Catalog.Domain.Ref(read.Epoch(),relation.Address())
					tempNode = relation.EffectiveStorageLocator(read)
				}
			}
			if tempNode == 0 { t.Fatal("missing genuine foreign TEMP storage") }
			peerOp,err := peer.Acquire(context.Background())
			if err != nil { t.Fatal(err) }
			defer peerOp.Done()
			peerScope,err := e.StartStatement(peerOp)
			if err != nil { t.Fatal(err) }
			defer peerScope.Finish(nil)
			foreign := engine.New()
			seen := 0
			// Output sinks deliberately have no producer authority. Exercise
			// lease admission at the actual executor's projection frontier.
			originalExpression := root.Expressions[0]
			root.Expressions[0] = enginetest.QueryProbe(originalExpression.ResultUsage(), func(ctx *exec.Context) (sqltype.Value, error) {
					seen++
					want := statistics.StatementCounters{SharedRead: 1}
					if got := core.PortalStatementCounters(portal); got != want { t.Fatal("real first Open suppressed by cumulative reset/counts",got) }
					ref := core.SourceReadRef(lease)
					original := *ref
					for _, bad := range []ids.ObjectRef{foreign.Catalog.Domain.Ref(original.Epoch,original.Address),e.Catalog.Domain.Ref(original.Epoch+1,original.Address),e.Catalog.Domain.Ref(original.Epoch,ids.ObjectAddress{})} {
						*ref = bad
						lease.Open()
						*ref = original
						if got := core.PortalStatementCounters(portal); got != want { t.Fatal("invalid domain/epoch/reference invented work",got) }
					}
					node := core.SourceReadNode(lease)
					originalNode := *node
					*node = originalNode+1
					lease.Open()
					*node = originalNode
					address := core.SourceReadStorageAddress(lease)
					originalAddress := *address
					*address = ids.ObjectAddress{}
					lease.Open()
					*address = originalAddress
					*ref,*node = tempRef,tempNode
					lease.Open()
					*ref,*node = original,originalNode
					host := authority.Host
					authority.Host = nil
					lease.Open()
					authority.Host = core.HostOf(peerScope)
					lease.Open()
					authority.Host = host
					if got := core.PortalStatementCounters(portal); got != want { t.Fatal("incarnation/storage/detached/foreign session invented work",got) }
					lease.Open() // A genuine new invocation of the still-valid lease.
					if got := core.PortalStatementCounters(portal); got.SharedRead != 1 || got.SharedHit != 1 { t.Fatal("valid warm request",got) }
					retained := source.View.Data().GenerationAt(source.Ref.Address)
					// Install an actual successor storage header over the same
					// immutable roots/family, rather than changing the capture.
					e.Mu.Lock()
					current := e.Storage.Relations[source.Ref.Address]
					next := *current
					next.Generation++
					if next.RetainedOrigin == 0 { next.RetainedOrigin = originalNode }
					e.Storage.Relations[source.Ref.Address] = &next
					e.Mu.Unlock()
					if retained == nil || &next == retained { t.Fatal("retained backing identity was not distinguished") }
					e.Mu.Lock()
					boundary := next.TruncateBoundary
					next.TruncateBoundary++
					e.Mu.Unlock()
					lease.Open()
					if got := core.PortalStatementCounters(portal); got.SharedRead != 1 || got.SharedHit != 1 { t.Fatal("incompatible actual storage boundary invented work",got) }
					e.Mu.Lock()
					next.TruncateBoundary = boundary
					origin := next.RetainedOrigin
					next.RetainedOrigin++
					e.Mu.Unlock()
					lease.Open()
					if got := core.PortalStatementCounters(portal); got.SharedRead != 1 || got.SharedHit != 1 { t.Fatal("foreign actual storage family invented work",got) }
					e.Mu.Lock()
					next.RetainedOrigin = origin
					e.Mu.Unlock()
					lease.Open()
					if got := core.PortalStatementCounters(portal); got.SharedRead != 1 || got.SharedHit != 2 { t.Fatal("valid retained storage lineage lost work",got) }
					return originalExpression.Eval(ctx)
			})
			command(func(scope *core.StatementScope) error {
				result,err := scope.ExecuteProtocolPortal("counter_portal",1,func([]session.ProtocolField) error {
					before := core.PortalStatementCounters(portal)
					lease.Open()
					if authority.Host != nil || core.PortalStatementCounters(portal) != before {
						t.Fatal("output sink regained producer authority")
					}
					return nil
				},nil,nil,core.CopyProtocolHooks{})
				if err == nil && !result.Suspended { t.Fatal("actual producer did not suspend") }
				return err
			})
			if seen != 1 || binding.Resource.Root != root || binding.Folds.Generation != folds.Generation || !core.PortalStatementDetached(portal) { t.Fatal("suspension changed accepted owner or retained ancestry") }
			entry := pgssPhaseEntry(t,e,"counter_source_authority")
			if entry.Planning.Count != 1 || entry.Execution.Count != 0 || entry.Counters != (statistics.StatementCounters{}) { t.Fatal("accepted planning/partial execution publication",entry) }
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("counter_portal") })
			entry = pgssPhaseEntry(t,e,"counter_source_authority")
			if entry.Execution.Count != 1 || entry.Rows != 1 || entry.Counters.SharedRead != 1 || entry.Counters.SharedHit != 2 || binding.Resource.Root != nil || binding.Folds != nil || authority.Host != nil { t.Fatal("partial close publication/resource lifetime",entry) }
			lease.Open()
			lease.Close()
			lease.Close()
			if got := pgssPhaseEntry(t,e,"counter_source_authority"); got != entry { t.Fatal("detached Open/Close changed history",got) }
		})
	}
}

func TestPgStatStatementsCountersSourceIncarnation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t,e,nil)
	for _, q := range []string{"SET pg_stat_statements.track='none'","CREATE TABLE counter_incarnation(k integer)","INSERT INTO counter_incarnation VALUES(1)"} { pgssOwnerExec(t,e,s,q) }
	old := bufferUnitCapture(t,e,s,"SELECT k FROM counter_incarnation")
	lease,authority := old.source.ReadLease,core.ScopeAuthority(old.scope)
	oldRef,oldNode := *core.SourceReadRef(lease),*core.SourceReadNode(lease)
	old.close(t)
	for _, q := range []string{"TRUNCATE counter_incarnation","INSERT INTO counter_incarnation VALUES(2)","SET pg_stat_statements.track_utility=off","SET pg_stat_statements.track='top'"} { pgssOwnerExec(t,e,s,q) }
	portal,command := pgssCounterPortal(t,e,s,"SELECT k FROM counter_incarnation AS counter_new_incarnation")
	fresh := core.PortalProducer(portal).Source.ReadLease
	if *core.SourceReadRef(fresh) != oldRef || *core.SourceReadNode(fresh) == oldNode { t.Fatal("TRUNCATE did not preserve identity and replace actual storage incarnation") }
	command(func(scope *core.StatementScope) error {
		_,err := scope.ExecuteProtocolPortal("counter_portal",1,func([]session.ProtocolField) error {
			before := core.PortalStatementCounters(portal)
			authority.Host = core.HostOf(scope)
			lease.Open()
			authority.Host = nil
			if got := core.PortalStatementCounters(portal); got != before { t.Fatal("stale actual storage charged its successor",before,got) }
			return nil
		},nil,nil,core.CopyProtocolHooks{})
		return err
	})
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("counter_portal") })
}

func TestPgStatStatementsCountersSourceResident(t *testing.T) {
	for _, kind := range []string{"TEMP","private","UNLOGGED"} {
		t.Run(kind,func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t,e,nil)
			pgssOwnerExec(t,e,s,"SET pg_stat_statements.track='none'")
			create := "CREATE "+kind+" TABLE counter_resident(k integer)"
			if kind == "private" {
				pgssOwnerExec(t,e,s,"BEGIN")
				create = "CREATE TABLE counter_resident(k integer)"
			}
			for _, q := range []string{create,"INSERT INTO counter_resident VALUES(1)","SET pg_stat_statements.track_utility=off","SET pg_stat_statements.track='top'"} { pgssOwnerExec(t,e,s,q) }
			portal,command := pgssCounterPortal(t,e,s,"SELECT k FROM counter_resident AS counter_resident_owner")
			source := core.PortalProducer(portal).Source
			read := source.View.Data().Catalog
			relation := read.MustRelation(source.Ref.Address)
			namespace := read.MustNamespace(relation.Namespace())
			if kind == "TEMP" && namespace.Temporary != s.SessionToken || kind != "TEMP" && namespace.Temporary != (ids.SessionToken{}) { t.Fatal("actual source persistence authority",namespace) }
			command(func(scope *core.StatementScope) error {
				_,err := scope.ExecuteProtocolPortal("counter_portal",0,func([]session.ProtocolField) error { return nil },nil,nil,core.CopyProtocolHooks{})
				return err
			})
			want := statistics.StatementCounters{SharedHit: 1}
			if kind == "TEMP" { want = statistics.StatementCounters{LocalHit: 1} }
			if kind == "UNLOGGED" { want = statistics.StatementCounters{SharedRead: 1} }
			if got := core.PortalStatementCounters(portal); got != want { t.Fatal("resident-source versus shared-cache branch",kind,got,want) }
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("counter_portal") })
			entry := pgssPhaseEntry(t,e,"counter_resident_owner")
			if entry.Counters != want || entry.Execution.Count != 1 { t.Fatal("resident source terminal",entry) }
			if kind == "private" { pgssOwnerExec(t,e,s,"ROLLBACK") }
		})
	}
}

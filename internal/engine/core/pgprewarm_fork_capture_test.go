package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/contrib"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

func TestPgPrewarmForkDirectedCapture(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		setup []string
		request storage.PrewarmRequest
		count int64
		message string
		temporary bool
	}{
		{"main_default", "prewarm_heap", nil, storage.PrewarmRequest{DefaultFirst:true, DefaultLast:true}, 6, "", false},
		{"main_subrange", "prewarm_heap", nil, storage.PrewarmRequest{First:1, Last:2}, 2, "", false},
		{"maintained_fsm", "prewarm_heap", []string{"VACUUM (FREEZE) prewarm_heap"}, storage.PrewarmRequest{Fork:1, First:0, Last:0}, 1, "", false},
		{"maintained_vm", "prewarm_heap", []string{"VACUUM (FREEZE) prewarm_heap"}, storage.PrewarmRequest{Fork:2, First:0, Last:0}, 1, "", false},
		{"fresh_fsm_absent", "prewarm_heap", nil, storage.PrewarmRequest{Fork:1, First:-1, Last:-1}, 0, "fork \"fsm\" does not exist for this relation", false},
		{"fresh_vm_absent", "prewarm_heap", nil, storage.PrewarmRequest{Fork:2, First:-1, Last:-1}, 0, "fork \"vm\" does not exist for this relation", false},
		{"logged_init_absent", "prewarm_heap", nil, storage.PrewarmRequest{Fork:3, First:-1, Last:-1}, 0, "fork \"init\" does not exist for this relation", false},
		{"unlogged_empty_init", "prewarm_fork_empty", []string{"CREATE UNLOGGED TABLE prewarm_fork_empty(k integer)"}, storage.PrewarmRequest{Fork:3, DefaultFirst:true, DefaultLast:true}, 0, "", false},
		{"unlogged_empty_init_bound", "prewarm_fork_empty", []string{"CREATE UNLOGGED TABLE prewarm_fork_empty(k integer)"}, storage.PrewarmRequest{Fork:3, First:0, DefaultLast:true}, 0, "starting block number must be between 0 and -1", false},
		{"unlogged_index_init", "prewarm_fork_index", []string{"CREATE UNLOGGED TABLE prewarm_fork_empty(k integer)", "CREATE INDEX prewarm_fork_index ON prewarm_fork_empty(k)"}, storage.PrewarmRequest{Fork:3, DefaultFirst:true, DefaultLast:true}, 1, "", false},
		{"unlogged_sequence_init", "prewarm_fork_sequence", []string{"CREATE UNLOGGED SEQUENCE prewarm_fork_sequence"}, storage.PrewarmRequest{Fork:3, DefaultFirst:true, DefaultLast:true}, 1, "", false},
		{"logged_index_vm_absent", "prewarm_fork_index", []string{"CREATE INDEX prewarm_fork_index ON prewarm_heap(k)"}, storage.PrewarmRequest{Fork:2, First:-1, Last:-1}, 0, "fork \"vm\" does not exist for this relation", false},
		{"logged_sequence_fsm_absent", "prewarm_fork_sequence", []string{"CREATE SEQUENCE prewarm_fork_sequence"}, storage.PrewarmRequest{Fork:1, First:-1, Last:-1}, 0, "fork \"fsm\" does not exist for this relation", false},
		{"logged_sequence_init_absent", "prewarm_fork_sequence", []string{"CREATE SEQUENCE prewarm_fork_sequence"}, storage.PrewarmRequest{Fork:3, First:-1, Last:-1}, 0, "fork \"init\" does not exist for this relation", false},
		{"temp_init_absent", "prewarm_fork_temp", []string{"CREATE TEMP TABLE prewarm_fork_temp(k integer)"}, storage.PrewarmRequest{Fork:3, First:-1, Last:-1}, 0, "fork \"init\" does not exist for this relation", true},
		{"catalog_init_absent", "pg_catalog.pg_am", nil, storage.PrewarmRequest{Fork:3, First:-1, Last:-1}, 0, "fork \"init\" does not exist for this relation", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, n := prewarmOwnerEngine(t)
			actor, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			for _, q := range tc.setup { enginetest.SessionSQL(t, actor, q) }
			read := e.CatalogRead()
			var ref ids.ObjectRef
			if tc.temporary {
				address := read.NameAddress(ids.NameKey{ClassID:ids.RelationClassID, Namespace:actor.TemporaryNamespace, Name:tc.target})
				ref = read.Owner().Domain.Ref(read.Epoch(), address)
			} else { ref = enginetest.RoleTableRef(t, e, tc.target) }
			target := read.MustRelation(ref.Address)
			address := ref.Address
			if target.SQLIndex != nil { address = target.SQLIndex.Parent }
			store := e.Storage.Relations[address]
			generator, indexRoot := e.Storage.Sequences[ref.Address], e.Storage.IndexRoots[ref.Address]
			var versions []storage.RowVersion
			var ends *storage.DeletionRoot
			var headers *storage.HeapHeaderRoot
			var generation uint64
			var auxiliary storage.AuxiliaryForks
			if store != nil { versions, ends, headers, generation, auxiliary = store.Versions, store.Ends, store.Headers, store.Generation, store.Auxiliary }
			retained := slices.Clone(versions)
			before := prewarmAdmissionCache(t, e, observer)
			for round := int64(1); round <= 2; round++ {
				scope, finish := prewarmOwnerScope(t, t.Context(), e, actor)
				host := &prewarmAdmissionHost{prewarmCountingHost:&prewarmCountingHost{Host:core.HostOf(scope).(contrib.Host[core.AuthorizationView])}}
				baseline := prewarmAdmissionLock(host, ref)
				parentRef := ref
				parentRef.Address = address
				parentBaseline := prewarmAdmissionLock(host, parentRef)
				host.afterRelease = func() { prewarmUnlocked(t, e, scope) }
				count, err := contrib.PgPrewarm[core.AuthorizationView](host, n, uint32(ref.Address.ObjectID), tc.request)
				if tc.message != "" {
					prewarmAdmissionDiagnostic(t, count, err, "22023", tc.message)
					if host.completed != 0 { t.Fatal("rejected fork/bound admitted blocks") }
				} else if err != nil || count != tc.count || int64(host.completed) != tc.count { t.Fatal("independent fork geometry/count", count, host.completed, err, tc.count) }
				prewarmAdmissionReleased(t, host, ref, baseline)
				if address != ref.Address { prewarmAdmissionReleased(t, host, parentRef, parentBaseline) }
				finish(err)
				for _, borrowed := range host.views { if borrowed.view.Load() != nil { t.Fatal("finished fork call retained sizing handle") } }
				if store != nil && (store != e.Storage.Relations[address] || !storage.SameVersionRoot(versions, store.Versions) || store.Ends != ends || store.Headers != headers || store.Generation != generation || store.Auxiliary != auxiliary || !reflect.DeepEqual(store.Versions, retained)) { t.Fatal("fork inspection changed actual heap/root/fork backing") }
				if e.CatalogRead().MustRelation(ref.Address) != target || e.Storage.Sequences[ref.Address] != generator || e.Storage.IndexRoots[ref.Address] != indexRoot { t.Fatal("fork inspection replaced descriptor/generator/index allocation") }
				if target.Sequence != nil {
					if len(host.generators) == 0 { t.Fatal("fork call did not capture actual generator authority") }
					for _, observed := range host.generators { if observed != generator || observed == nil { t.Fatal("fork call lost exact generator identity") } }
				}
				if target.SQLIndex != nil {
					if len(host.allocations) == 0 { t.Fatal("fork call did not capture actual index allocation authority") }
					for _, observed := range host.allocations {
						if !observed.Allocated || observed.Relation != target || observed.Index != indexRoot || observed.Parent != read.MustRelation(address) || !storage.SameVersionRoot(observed.ParentVersions, versions) { t.Fatalf("fork allocation backing: relation=%d allocated=%t root=%p expected_root=%p parent=%p expected_parent=%p rows=%d expected_rows=%d", ref.Address.ObjectID, observed.Allocated, observed.Index, indexRoot, observed.Parent, read.MustRelation(address), len(observed.ParentVersions), len(versions)) }
					}
				}
				if tc.count > 0 {
					want := make(map[[2]int64][2]int64)
					first := tc.request.First
					if tc.request.DefaultFirst { first = 0 }
					for block := first; block < first+tc.count; block++ { want[[2]int64{int64(tc.request.Fork),block}] = [2]int64{round,0} }
					if got := prewarmOwnerCoordinates(t, e, observer, target.EffectiveStorageLocator(read)); !reflect.DeepEqual(got, want) { t.Fatal("real fork residency/repeat usage/released pins", got, want) }
				} else if !reflect.DeepEqual(before, prewarmAdmissionCache(t, e, observer)) { t.Fatal("zero/rejected fork changed occupied cache") }
				if tc.request.Fork == 0 {
					if len(host.views) != 1 || host.views[0].data.GenerationAt(ref.Address) != store { t.Fatal("main geometry did not borrow actual visible roots") }
				} else {
					// These are actual returned handles, captured immutable roots and
					// real producer bytes, paired above with consumer and ownership
					// outcomes—not a host-method invocation counter or fake failure.
					if len(host.views) != 0 {
						borrowed := host.views[0]
						captured := borrowed.data.GenerationAt(address)
						var capturedGeneration uint64
						if captured != nil { capturedGeneration = captured.Generation }
						t.Fatalf("non-main fork borrowed main visibility storage: case=%s relation=%d captured=%p expected=%p generation=%d rows=%d local=%t", tc.name, address.ObjectID, captured, store, capturedGeneration, len(borrowed.data.Committed[address]), borrowed.local)
					}
					if len(host.catalogPayloads) != 0 { t.Fatal("non-main fork unnecessarily consumed genuine catalog producer payload", tc.name, host.catalogPayloads) }
				}
			}
			enginetest.TransactionRows(t, actor, "SELECT 1", 1)
		})
	}

	for _, boundary := range []string{"empty_init", "reversed_main", "positive_vm"} {
		actions := []string{"descriptor", "select", "routine", "cancel"}
		if boundary != "empty_init" { actions = append(actions, "rows", "ends") }
		// Auxiliary publication is witnessed on a previously unmaintained
		// main capture. An AX visibility truncator cannot run synchronously
		// while any genuine prewarm invocation retains AccessShare.
		if boundary == "reversed_main" { actions = append(actions, "auxiliary") }
		for _, action := range actions {
			t.Run(boundary+"_after_capture_"+action, func(t *testing.T) {
				e, _, n := prewarmOwnerEngine(t)
				actor, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				targetName := "prewarm_heap"
				request := storage.PrewarmRequest{First:2, Last:1}
				if boundary == "empty_init" {
					targetName = "prewarm_fork_empty"
					enginetest.SessionSQL(t, actor, "CREATE UNLOGGED TABLE prewarm_fork_empty(k integer)")
					request = storage.PrewarmRequest{Fork:3, DefaultFirst:true, DefaultLast:true}
				}
				if boundary == "positive_vm" {
					enginetest.SessionSQL(t, actor, "VACUUM (FREEZE) prewarm_heap")
					request = storage.PrewarmRequest{Fork:2, First:0, Last:0}
				}
				if action == "select" || action == "routine" {
					for _, q := range []string{"CREATE ROLE prewarm_fork_reader", "GRANT SELECT ON "+targetName+" TO prewarm_fork_reader", "SET ROLE prewarm_fork_reader"} { enginetest.SessionSQL(t, actor, q) }
				}
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), targetName)
				e.Mu.Lock()
				capturedAuxiliary := e.Storage.Relations[ref.Address].Auxiliary
				e.Mu.Unlock()
				if action == "auxiliary" && (capturedAuxiliary.FSM || capturedAuxiliary.VM) { t.Fatal("auxiliary-fence fixture was already maintained") }
				before := prewarmAdmissionCache(t, e, observer)
				ctx, cancel := context.WithCancel(t.Context()); defer cancel()
				scope, finish := prewarmOwnerScope(t, ctx, e, actor)
				host := &prewarmAdmissionHost{prewarmCountingHost:&prewarmCountingHost{Host:core.HostOf(scope).(contrib.Host[core.AuthorizationView])}}
				baseline := prewarmAdmissionLock(host, ref)
				host.afterRelease = func() {
					prewarmUnlocked(t, e, scope)
					switch action {
					case "descriptor": enginetest.Run(t, e, "GRANT SELECT ON "+targetName+" TO PUBLIC")
					case "select": enginetest.Run(t, e, "REVOKE SELECT ON "+targetName+" FROM prewarm_fork_reader")
					case "routine": enginetest.Run(t, e, "REVOKE EXECUTE ON FUNCTION pg_prewarm(regclass,text,text,bigint,bigint) FROM PUBLIC")
					case "rows": enginetest.Run(t, e, "INSERT INTO "+targetName+" VALUES(2001)")
					case "ends": enginetest.Run(t, e, "DELETE FROM "+targetName+" WHERE k=1")
					case "auxiliary":
						// Ordinary VACUUM's ShareUpdateExclusive is compatible
						// with the live prewarm AccessShare contribution.
						enginetest.Run(t, e, "VACUUM (FREEZE) "+targetName)
						e.Mu.Lock()
						publishedAuxiliary := e.Storage.Relations[ref.Address].Auxiliary
						e.Mu.Unlock()
						if publishedAuxiliary == capturedAuxiliary || !publishedAuxiliary.FSM || !publishedAuxiliary.VM { t.Fatal("real maintenance did not publish distinct auxiliary facts", capturedAuxiliary, publishedAuxiliary) }
					case "cancel": cancel()
					}
					// Preserve actual concurrent DML/maintenance read frontiers;
					// rejected prewarm must add nothing after that operation.
					before = prewarmAdmissionCache(t, e, observer)
				}
				count, err := contrib.PgPrewarm[core.AuthorizationView](host, n, uint32(ref.Address.ObjectID), request)
				if action == "routine" { prewarmAdmissionDiagnostic(t, count, err, "42501", "permission denied for function pg_prewarm") } else {
					want := ids.ErrCatalogReplaced
					if action == "cancel" { want = context.Canceled }
					if count != 0 || !errors.Is(err, want) { t.Fatal("empty/reversed/non-main capture fabricated success after real mutation", boundary, action, count, err) }
				}
				if host.completed != 0 { t.Fatal("stale/canceled fork reached actual admission", host.completed) }
				prewarmAdmissionReleased(t, host, ref, baseline)
				finish(err)
				for _, borrowed := range host.views { if borrowed.view.Load() != nil { t.Fatal("failed capture retained sizing view") } }
				if !reflect.DeepEqual(before, prewarmAdmissionCache(t, e, observer)) { t.Fatal("failed fork capture changed cache") }
				enginetest.SessionSQL(t, actor, "RESET ROLE")
				enginetest.TransactionRows(t, actor, "SELECT 1", 1)
			})
		}
	}

	t.Run("canceled_actual_auxiliary_prefix", func(t *testing.T) {
		e, ref, n := prewarmOwnerEngine(t)
		enginetest.Run(t, e, "VACUUM (FREEZE) prewarm_heap")
		actor, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		ctx, cancel := context.WithCancel(t.Context()); defer cancel()
		scope, finish := prewarmOwnerScope(t, ctx, e, actor)
		host := &prewarmAdmissionHost{prewarmCountingHost:&prewarmCountingHost{Host:core.HostOf(scope).(contrib.Host[core.AuthorizationView]), cancel:cancel, cancelAfter:1}}
		baseline := prewarmAdmissionLock(host, ref)
		count, err := contrib.PgPrewarm[core.AuthorizationView](host, n, uint32(ref.Address.ObjectID), storage.PrewarmRequest{Fork:2, First:0, Last:0})
		// Terminal cancellation retains completed internal progress, but its
		// error prevents the evaluator from publishing any SQL success value.
		if !errors.Is(err, context.Canceled) || count != 1 || host.completed != 1 { t.Fatal("actual terminal auxiliary cancellation lost progress/error ownership", count, host.completed, err) }
		prewarmAdmissionReleased(t, host, ref, baseline)
		finish(err)
		want := map[[2]int64][2]int64{{2,0}:{1,0}}
		if got := prewarmOwnerCoordinates(t, e, observer, bufferUnitNode(t, e, "prewarm_heap")); !reflect.DeepEqual(got, want) { t.Fatal("completed auxiliary prefix/pin ownership", got, want) }
	})
}

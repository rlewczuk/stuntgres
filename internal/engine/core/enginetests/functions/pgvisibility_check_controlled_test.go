package functions_test

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func visibilityCheckFixture(t *testing.T, freeze, wide bool) (*core.Engine, ids.ObjectRef) {
	t.Helper()
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION pg_visibility")
	if wide {
		var columns strings.Builder
		for i := range 1200 {
			if i != 0 {
				columns.WriteByte(',')
			}
			columns.WriteString("1 AS c" + strconv.Itoa(i))
		}
		enginetest.Run(t, e, "CREATE TABLE visibility_heap AS SELECT "+columns.String())
	} else {
		enginetest.Run(t, e, "CREATE TABLE visibility_heap(n integer)")
		enginetest.Run(t, e, "INSERT INTO visibility_heap VALUES(1)")
	}
	if freeze {
		enginetest.Run(t, e, "VACUUM (FREEZE) visibility_heap")
	}
	return e, enginetest.CatalogRef(t, e, e.CatalogRead(), "visibility_heap")
}
func visibilityCheckNode(t *testing.T, e *core.Engine, name string) plan.InstalledPgvisibilityExpr {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "FROM"
	binder.Query.Namespace = &analyze.QueryNamespace{}
	n, err := binder.BindFunctionSource(enginetest.FunctionCall(name, enginetest.StringLiteral("visibility_heap")))
	if err != nil {
		t.Fatal(err)
	}
	return n.(plan.InstalledPgvisibilityExpr)
}

// Publish genuine production VM data against the ACTUAL captured layout basis.
// Only the claim is deliberately inconsistent: owner/MVCC/header truth is never
// replaced with a finding, a fake provider, or a test-only host capability.
func visibilityCheckMark(t *testing.T, e *core.Engine, scope *core.StatementScope, ref ids.ObjectRef, av, af bool) (*storage.VisibilityRoot, []storage.HeapTupleFact) {
	t.Helper()
	h := visibilityHost(scope)
	writes, deletes := h.ColumnProjectionRoots(ref.Address)
	owners, err := h.LockHeapStatisticsCohort()
	if err != nil {
		t.Fatal(err)
	}
	store := h.PrivateStorageLocked(ref.Address)
	snapshot := *store
	facts := h.HeapStatisticsFactsLocked(owners, store, ref.Address, writes, deletes)
	horizon := h.PgvisibilityHorizonLocked(owners)
	h.UnlockHeapStatisticsCohortLocked(owners)
	prepared := storage.PrepareVisibilityFacts(facts, snapshot.Headers)
	facts = prepared
	count := 0
	for _, fact := range facts {
		if !fact.LinePointerOnly {
			count++
		}
	}
	maintenance := make([]lock.TupleMaintenance, count)
	at := 0
	for i := range facts {
		if facts[i].LinePointerOnly {
			continue
		}
		resource, err := exec.TupleResource(ref, facts[i].Version, snapshot.Headers)
		if err != nil {
			t.Fatal(err)
		}
		maintenance[at].Tuple = lock.MultiTuple{Resource: resource, Version: facts[i].Version.Version}
		at++
	}
	if len(maintenance) != 0 {
		if err := e.Locks.CaptureTupleMaintenance(t.Context(), scope.LockTicket, maintenance); err != nil {
			t.Fatal(err)
		}
	}
	at = 0
	for i := range facts {
		if facts[i].LinePointerOnly {
			continue
		}
		raw := maintenance[at]
		at++
		facts[i].LockMaintenance = raw.Present && (raw.Active || raw.NewestFullXID == 0 || raw.NewestFullXID >= snapshot.Maintenance.FrozenXID)
	}
	heap, err := prepared.Capture(t.Context(), horizon, false)
	if err != nil {
		t.Fatal(err)
	}
	claims := heap
	if heap.Blocks != 0 {
		claims.Runs = []storage.VisibilityRun{{End: heap.Blocks, Bits: storage.VisibilityBits{AllVisible: av, AllFrozen: af}}}
	}
	claims.AllVisible, claims.AllFrozen = 0, 0
	if av {
		claims.AllVisible = heap.Blocks
	}
	if af {
		claims.AllFrozen = heap.Blocks
	}
	if heap.Blocks != 0 && (av || af) {
		claims.MappedBlocks, err = storage.VisibilityVMExtent(heap.Blocks)
		if err != nil {
			t.Fatal(err)
		}
	}
	root := &storage.VisibilityRoot{Map: claims, Basis: prepared.Basis(&snapshot), Proof: horizon}
	e.Mu.Lock()
	valid := root.Basis.SameRoots(store)
	if valid {
		store.Visibility = root
	}
	e.Mu.Unlock()
	if !valid {
		t.Fatal("fixture attempted to publish against changed physical roots")
	}
	return root, facts
}

func visibilityCheckFindings(t *testing.T, e *core.Engine, scope *core.StatementScope, ref ids.ObjectRef, name string, want []ids.VersionID) {
	t.Helper()
	n := visibilityCheckNode(t, e, name)
	actual, err := core.HostOf(scope).Contrib().PgvisibilityCheck(n, uint32(ref.Address.ObjectID))
	if err != nil || !reflect.DeepEqual(actual, want) {
		t.Fatal(name, "installed host findings", actual, want, err)
	}
	view := e.CatalogRead()
	e.Mu.Lock()
	auth := enginetest.CatalogAuthorization(e, view)
	e.Mu.Unlock()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context(), Host: core.HostOf(scope), Catalog: view, Input: scope.Input, Output: scope.Output}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}}}
	cursor, err := n.OpenSource(&evaluation)
	if err != nil {
		t.Fatal(name, "genuine source", err)
	}
	defer cursor.Close()
	if cursor.Type == nil || cursor.Type.ID() != sqltype.Tid || cursor.Values != nil || cursor.State == nil {
		t.Fatal("argument-only TID source ownership", cursor.Type, cursor.Values, cursor.State)
	}
	for i := range want {
		value, present, err := n.AdvanceSource(t.Context(), &evaluation, &cursor)
		if err != nil || !present {
			t.Fatal(name, "actual borrowed TID row", i, present, err)
		}
		// Independent bijection, not a value learned from the implementation.
		n := uint64(want[i]) - 1
		wantBlock, wantOffset := uint32(n/65535), uint16(n%65535+1)
		block, offset, err := value.TID()
		if err != nil || block != wantBlock || offset != wantOffset {
			t.Fatal("exact allocated CTID", want[i], block, offset, wantBlock, wantOffset, err)
		}
		b, o, err := storage.LogicalTID(want[i])
		if err != nil || b != wantBlock || o != wantOffset {
			t.Fatal("shared TID bijection", want[i], b, o, err)
		}
	}
	if _, present, err := n.AdvanceSource(t.Context(), &evaluation, &cursor); err != nil || present || !cursor.Exhausted || cursor.State != nil || cursor.Values != nil {
		t.Fatal(name, "consumed DONE must detach findings", present, err, cursor)
	}
}

func TestPgvisibilityCheckControlledMap(t *testing.T) {
	for _, phase := range []string{"healthy_frozen", "old_unfrozen", "private_unmarked", "private_claimed", "pending_delete", "committed_delete", "retained_predecessors", "wide_continuation", "lock_xmax", "unused", "redirect", "retired_dead_pointer"} {
		t.Run(phase, func(t *testing.T) {
			freeze := phase != "old_unfrozen" && phase != "wide_continuation"
			e, ref := visibilityCheckFixture(t, freeze, phase == "wide_continuation")
			store := e.Storage.Relations[ref.Address]
			original := store.Versions[0]
			writer := enginetest.StartSession(t, e, nil)
			var visible, frozen []ids.VersionID
			var private ids.VersionID
			if phase == "old_unfrozen" || phase == "wide_continuation" {
				frozen = []ids.VersionID{original.Version}
			}
			switch phase {
			case "retired_dead_pointer":
				for _, q := range []string{"INSERT INTO visibility_heap VALUES(2)", "CREATE INDEX visibility_dead_index ON visibility_heap(n)", "DELETE FROM visibility_heap WHERE n=1", "VACUUM (FREEZE, INDEX_CLEANUP OFF, TRUNCATE OFF) visibility_heap"} {
					enginetest.Run(t, e, q)
				}
				if store.Headers.Header(original.Version).Slot != storage.HeapSlotDead {
					t.Fatal("indexed keep-dead VACUUM did not retain an actual dead slot")
				}
				for _, row := range store.Versions {
					if row.Version == original.Version {
						t.Fatal("dead pointer fixture retained payload")
					}
				}
				if store.Retained != nil || store.Discarded != nil || len(store.Headers.Additional) != 0 {
					t.Fatal("dead pointer fixture retained historical payload")
				}
				visible = []ids.VersionID{original.Version}
				frozen = []ids.VersionID{original.Version}
			case "private_unmarked", "private_claimed":
				enginetest.SessionSQL(t, writer, "BEGIN")
				enginetest.SessionSQL(t, writer, "INSERT INTO visibility_heap VALUES(2)")
				private = writer.Transactions.Writes[ref.Address][0].Version
				if phase == "private_claimed" {
					visible = []ids.VersionID{private}
					frozen = []ids.VersionID{private}
				}
			case "pending_delete", "committed_delete":
				enginetest.SessionSQL(t, writer, "BEGIN")
				enginetest.SessionSQL(t, writer, "DELETE FROM visibility_heap")
				if phase == "committed_delete" {
					enginetest.SessionSQL(t, writer, "COMMIT")
				}
				visible = []ids.VersionID{original.Version}
				frozen = []ids.VersionID{original.Version}
			case "retained_predecessors":
				enginetest.SessionSQL(t, writer, "BEGIN")
				enginetest.SessionSQL(t, writer, "UPDATE visibility_heap SET n=2")
				middle := writer.Transactions.Writes[ref.Address][0].Version
				enginetest.SessionSQL(t, writer, "UPDATE visibility_heap SET n=3")
				latest := writer.Transactions.Writes[ref.Address][0].Version
				enginetest.SessionSQL(t, writer, "COMMIT")
				visible = []ids.VersionID{original.Version, middle}
				frozen = []ids.VersionID{original.Version, middle, latest}
			case "lock_xmax":
				enginetest.SessionSQL(t, writer, "BEGIN")
				enginetest.SessionSQL(t, writer, "SELECT n FROM visibility_heap FOR SHARE")
				frozen = []ids.VersionID{original.Version}
			case "unused":
				for _, q := range []string{"INSERT INTO visibility_heap VALUES(2)", "DELETE FROM visibility_heap WHERE n=1", "VACUUM (FREEZE) visibility_heap"} {
					enginetest.Run(t, e, q)
				}
				if store.Headers.Header(original.Version).Slot != storage.HeapSlotUnused {
					t.Fatal("actual unindexed DELETE retirement did not leave an unused slot")
				}
			case "redirect":
				enginetest.Run(t, e, "UPDATE visibility_heap SET n=2")
				successor := store.Versions[0]
				if store.Retained == nil || store.Retained.Version.Version != original.Version || store.Headers.Header(original.Version).Successor != successor.Version {
					t.Fatal("missing actual committed predecessor/successor lineage")
				}
				a, _, err := storage.LogicalTID(original.Version)
				if err != nil {
					t.Fatal(err)
				}
				b, _, err := storage.LogicalTID(successor.Version)
				if err != nil || a != b {
					t.Fatal("actual successor crossed LogicalTID block", a, b, err)
				}
				enginetest.Run(t, e, "VACUUM (FREEZE) visibility_heap")
				header := store.Headers.Header(original.Version)
				if header.Slot != storage.HeapSlotRedirect || header.Redirect != successor.Version {
					t.Fatal("actual unindexed predecessor did not redirect to its survivor", header)
				}
			}
			defer func() {
				if writer.TransactionState() != session.TxIdle {
					enginetest.SessionSQL(t, writer, "ROLLBACK")
				}
			}()
			scope, finish := visibilityScope(t, e, enginetest.StartSession(t, e, nil))
			defer finish(nil)
			h := visibilityHost(scope)
			e.Mu.Lock()
			before, local, err := h.RelationDataViewLocked(ref)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			retained := before.Retain()
			if local {
				before.Release()
			}
			defer retained.Release()
			data := retained.Data()
			if private != 0 {
				for _, row := range data.Committed[ref.Address] {
					if row.Version == private {
						t.Fatal("private version entered observer committed source")
					}
				}
				if len(data.Own[ref.Address]) != 0 {
					t.Fatal("observer borrowed writer private roots")
				}
			}
			root, facts := visibilityCheckMark(t, e, scope, ref, phase != "private_unmarked", phase != "private_unmarked")
			if phase == "wide_continuation" && (root.Map.Blocks != 2 || len(facts) != 1 || len(facts[0].Version.Values) != 1200) {
				t.Fatal("fixture did not cross two modeled blocks", root.Map, len(facts))
			}
			if phase == "lock_xmax" {
				if len(facts) != 1 || !facts[0].Version.Frozen || !facts[0].LockMaintenance {
					t.Fatal("missing real Frozen xmin/raw lock-xmax witness", facts)
				}
			}
			if phase == "old_unfrozen" && storage.VisibilityTupleBits(facts[0], storage.VisibilityHorizon{}, false).AllVisible {
				t.Fatal("fixture did not distinguish READ sufficiency from visible truth")
			}
			slices.Sort(visible)
			slices.Sort(frozen)
			versions, ends, headers, generation := store.Versions, store.Ends, store.Headers, store.Generation
			visibilityCheckFindings(t, e, scope, ref, "pg_check_visible", visible)
			visibilityCheckFindings(t, e, scope, ref, "pg_check_frozen", frozen)
			if !storage.SameVersionRoot(versions, store.Versions) || ends != store.Ends || headers != store.Headers || generation != store.Generation {
				t.Fatal("checker changed MVCC/physical tuple truth")
			}
			if !storage.SameVersionRoot(data.Committed[ref.Address], versions) || data.Headers[ref.Address] != headers {
				t.Fatal("VM claims changed observer SELECT roots")
			}
			// Pending writes/tuple locks belong to writer. Truncate through that
			// real owner so AX upgrades its own holds rather than waiting forever
			// on our inactive fixture transaction; keep observer's retained roots.
			mutator, finishMutator := visibilityScope(t, e, writer)
			truncate := visibilityCheckNode(t, e, "pg_truncate_visibility_map")
			if err := finishMutator(core.HostOf(mutator).Contrib().PgvisibilityTruncate(truncate, uint32(ref.Address.ObjectID))); err != nil {
				t.Fatal(err)
			}
			visibilityCheckFindings(t, e, scope, ref, "pg_check_visible", nil)
			visibilityCheckFindings(t, e, scope, ref, "pg_check_frozen", nil)
			if store.Visibility == root || !store.Visibility.Invalidated || root.Invalidated || root.Map.AllVisible == 0 && phase != "private_unmarked" {
				t.Fatal("truncate mutated old immutable root", root, store.Visibility)
			}
			if !storage.SameVersionRoot(versions, store.Versions) || ends != store.Ends || headers != store.Headers || generation != store.Generation {
				t.Fatal("truncate changed SELECT/tuple roots")
			}
			if err := finish(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("read_only_snapshot_not_corruption", func(t *testing.T) {
		e, ref := visibilityCheckFixture(t, true, false)
		reader := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY")
		enginetest.SessionSQL(t, reader, "SELECT n FROM visibility_heap")
		defer enginetest.SessionSQL(t, reader, "ROLLBACK")
		// CurrentSnapshot is the public full-XID value, not an owned heap view.
		// Retain the reader's actual stable relation view past its statement scope.
		snapshotScope, finishSnapshot := visibilityScope(t, e, reader)
		e.Mu.Lock()
		view, local, err := visibilityHost(snapshotScope).RelationDataViewLocked(ref)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(finishSnapshot(err))
		}
		snapshot := view.Retain()
		if local {
			view.Release()
		}
		defer snapshot.Release()
		if err := finishSnapshot(nil); err != nil {
			t.Fatal(err)
		}
		if snapshot == nil {
			t.Fatal("reader did not provide a retained heap view")
		}
		oldRows := snapshot.Data().Committed[ref.Address]
		if len(oldRows) != 1 || reader.Transactions.CurrentSnapshot == nil || reader.Transactions.Ticket.AssignedFullXID() != 0 {
			t.Fatal("read-only snapshot fixture assigned a writer")
		}
		enginetest.Run(t, e, "INSERT INTO visibility_heap VALUES(2)")
		enginetest.Run(t, e, "VACUUM (FREEZE) visibility_heap")
		store := e.Storage.Relations[ref.Address]
		if len(store.Versions) != 2 || !store.Versions[1].Frozen || store.Versions[1].Born <= snapshot.Data().Frontier {
			t.Fatal("snapshot frontier was not older than a healthy Frozen insertion")
		}
		scope, finish := visibilityScope(t, e, enginetest.StartSession(t, e, nil))
		defer finish(nil)
		root, _ := visibilityCheckMark(t, e, scope, ref, true, true)
		visibilityCheckFindings(t, e, scope, ref, "pg_check_visible", nil)
		visibilityCheckFindings(t, e, scope, ref, "pg_check_frozen", nil)
		if store.Visibility != root || root.Map.AllVisible != 1 || root.Map.AllFrozen != 1 || len(snapshot.Data().Committed[ref.Address]) != 1 || !storage.SameVersionRoot(oldRows, snapshot.Data().Committed[ref.Address]) {
			t.Fatal("advertised read-only snapshot erased claims or changed SELECT roots")
		}
		if err := finish(nil); err != nil {
			t.Fatal(err)
		}
	})
}

func visibilityBorrowedEvaluation(t *testing.T, e *core.Engine, scope *core.StatementScope, n plan.InstalledPgvisibilityExpr) *exec.Context {
	t.Helper()
	view := e.CatalogRead()
	e.Mu.Lock()
	auth, err := scope.RoutineAuthorizationLocked()
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	evaluation := &exec.Context{
		EvalStatement: exec.EvalStatement{Context: t.Context(), Host: core.HostOf(scope), Catalog: view, Input: scope.Input, Output: scope.Output},
		EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}},
	}
	if err := exec.BeginRoutineInitialization(evaluation); err != nil {
		t.Fatal(err)
	}
	if err := exec.AdmitRoutineCall(evaluation, n, false, false); err != nil {
		t.Fatal(err)
	}
	return evaluation
}

func TestPgvisibilityBorrowedCapture(t *testing.T) {
	for _, name := range []string{"pg_check_visible", "pg_check_frozen"} {
		t.Run("findings_first_demand_and_detached_tail/"+name, func(t *testing.T) {
			e, ref := visibilityCheckFixture(t, false, false)
			enginetest.Run(t, e, "INSERT INTO visibility_heap VALUES(2)")
			visibilityActivateIncoming(t, e)
			writer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, writer, "BEGIN")
			enginetest.SessionSQL(t, writer, "DELETE FROM visibility_heap")
			defer enginetest.SessionSQL(t, writer, "ROLLBACK")
			scope, finish := visibilityScope(t, e, enginetest.StartSession(t, e, nil))
			defer finish(nil)
			visibilityCheckMark(t, e, scope, ref, false, false)
			n := visibilityCheckNode(t, e, name)
			evaluation := visibilityBorrowedEvaluation(t, e, scope, n)
			cursor, err := n.OpenSource(evaluation)
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			// The claims change AFTER metadata Open. Both actual unfrozen tuples
			// have real pending deletions, so both checks must capture their TIDs.
			visibilityCheckMark(t, e, scope, ref, true, true)
			versions := e.Storage.Relations[ref.Address].Versions
			if len(versions) != 2 {
				t.Fatal("missing real unfrozen tuple cohort", versions)
			}
			for i, version := range versions {
				value, present, err := n.AdvanceSource(t.Context(), evaluation, &cursor)
				want := uint64(version.Version) - 1
				block, offset, tidErr := value.TID()
				if err != nil || !present || tidErr != nil || block != uint32(want/65535) || offset != uint16(want%65535+1) {
					t.Fatal("first-demand captured TID", i, value, present, err, tidErr)
				}
				if scope.Op.Session.Sepgsql.Current() != "A" {
					t.Fatal("actual finding invocation leaked trusted subject")
				}
				if i == 0 {
					// A new healthy map cannot mutate this invocation's findings.
					visibilityCheckMark(t, e, scope, ref, false, false)
				}
			}
			if _, present, err := n.AdvanceSource(t.Context(), evaluation, &cursor); err != nil || present || cursor.State != nil || !cursor.Exhausted {
				t.Fatal("actual findings DONE did not detach", present, err, cursor)
			}
			if err := finish(nil); err != nil {
				t.Fatal(err)
			}
			if _, present, err := n.AdvanceSource(t.Context(), nil, &cursor); err != nil || present {
				t.Fatal("detached EOF requested a dead evaluator", present, err)
			}
		})
	}
	t.Run("map_and_independent_page_runs", func(t *testing.T) {
		e, ref := visibilityCheckFixture(t, false, true)
		visibilityActivateIncoming(t, e)
		scope, finish := visibilityScope(t, e, enginetest.StartSession(t, e, nil))
		defer finish(nil)
		visibilityCheckMark(t, e, scope, ref, false, false)
		n := visibilityCheckNode(t, e, "pg_visibility")
		evaluation := visibilityBorrowedEvaluation(t, e, scope, n)
		cursor, err := n.OpenSource(evaluation)
		if err != nil {
			t.Fatal(err)
		}
		defer cursor.Close()
		root, _ := visibilityCheckMark(t, e, scope, ref, true, true)
		if root.Map.Blocks != 2 {
			t.Fatal("wide row must cross two actual modeled pages", root.Map)
		}
		for block := range int64(2) {
			value, present, err := n.AdvanceSource(t.Context(), evaluation, &cursor)
			if err != nil || !present {
				t.Fatal("borrowed visibility row", block, present, err)
			}
			if scope.Op.Session.Sepgsql.Current() != "A" {
				t.Fatal("actual page invocation leaked trusted subject")
			}
			got, failure := value.CompositeField(0).Integer()
			av, avErr := value.CompositeField(1).Boolean()
			af, afErr := value.CompositeField(2).Boolean()
			pd, pdErr := value.CompositeField(3).Boolean()
			if failure != nil || avErr != nil || afErr != nil || pdErr != nil || got != block || !av || !af || pd {
				t.Fatal("VM claims must not replace independent page truth", value, failure, avErr, afErr, pdErr)
			}
			if block == 0 {
				visibilityCheckMark(t, e, scope, ref, false, false)
			}
		}
		if _, present, err := n.AdvanceSource(t.Context(), evaluation, &cursor); err != nil || present || cursor.State != nil {
			t.Fatal("map DONE did not release captured runs", present, err)
		}
	})
	t.Run("truncate_source_waits_for_borrowed_demand", func(t *testing.T) {
		e, ref := visibilityCheckFixture(t, true, false)
		visibilityActivateIncoming(t, e)
		scope, finish := visibilityScope(t, e, enginetest.StartSession(t, e, nil))
		defer finish(nil)
		root, _ := visibilityCheckMark(t, e, scope, ref, true, true)
		n := visibilityCheckNode(t, e, "pg_truncate_visibility_map")
		evaluation := visibilityBorrowedEvaluation(t, e, scope, n)
		cursor, err := n.OpenSource(evaluation)
		if err != nil {
			t.Fatal(err)
		}
		defer cursor.Close()
		store := e.Storage.Relations[ref.Address]
		if store.Visibility != root || root.Invalidated || cursor.State == nil || cursor.Values != nil {
			t.Fatal("scalar source Open ran truncate", root, store.Visibility, cursor)
		}
		value, present, err := n.AdvanceSource(t.Context(), evaluation, &cursor)
		if err != nil || !present || value.IsNull() || value.Type().ID() != sqltype.Void || store.Visibility == root || !store.Visibility.Invalidated || root.Invalidated {
			t.Fatal("first scalar demand failed real immutable truncate", value, present, err, root, store.Visibility)
		}
		if scope.Op.Session.Sepgsql.Current() != "A" {
			t.Fatal("actual truncate invocation leaked trusted subject")
		}
		if _, present, err := n.AdvanceSource(t.Context(), nil, &cursor); err != nil || present || cursor.State != nil {
			t.Fatal("scalar DONE reentered truncate", present, err)
		}
		page := visibilityCheckNode(t, e, "pg_visibility")
		pageEvaluation := visibilityBorrowedEvaluation(t, e, scope, page)
		rows, err := page.OpenSource(pageEvaluation)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		value, present, err = page.AdvanceSource(t.Context(), pageEvaluation, &rows)
		if err != nil || !present {
			t.Fatal("post-truncate page", present, err)
		}
		for i, want := range []bool{false, false, true} {
			got, err := value.CompositeField(i + 1).Boolean()
			if err != nil || got != want {
				t.Fatal("truncate must suppress VM only, retaining actual PD", i, got, want, err)
			}
		}
	})
}

func visibilityActivateIncoming(t *testing.T, e *core.Engine) {
	t.Helper()
	runtime, err := sepgsql.Compile(sepgsql.SimulationConfig{InstallSQL: true, Policy: sepgsql.Policy{
		ID: "incoming-visibility-owner", Version: "1", Contexts: []string{"A", "P", "S", "T"}, InitialContext: "A", UnlabeledContext: "P",
		Rules: []sepgsql.Rule{
			{Source: "A", Target: "*", Class: "*", Allow: []string{"*"}},
			{Source: "T", Target: "*", Class: "*", Allow: []string{"*"}},
			{Source: "T", Target: "S", Class: "db_procedure", Deny: []string{"execute"}},
		},
		TrustedTransitions: []sepgsql.TrustedTransition{{Source: "A", Procedure: "S", Result: "T"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.InstallSepgsqlSimulation(runtime, true)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, signature := range []string{"pg_check_visible(regclass)", "pg_check_frozen(regclass)", "pg_visibility(regclass)", "pg_truncate_visibility_map(regclass)"} {
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION "+signature+" IS 'S'")
	}
}

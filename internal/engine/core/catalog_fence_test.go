package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type orderProbeFence struct {
	kind session.FenceKind
	name string
	err  error
	seen *[]string
}

func (f orderProbeFence) Kind() session.FenceKind { return f.kind }

func (f orderProbeFence) CurrentLocked(*Engine, *session.Journal) error {
	*f.seen = append(*f.seen, f.name)
	return f.err
}

func TestCatalogFencesCheckKindsInCallOrder(t *testing.T) {
	var seen []string
	c := session.NewJournal()
	for _, f := range []orderProbeFence{
		{kind: session.FenceNamespace, name: "namespace"},
		{kind: session.FenceStorageRead, name: "read-1"},
		{kind: fenceTruncateGraph, name: "truncate"},
		{kind: session.FenceIndexSet, name: "index"},
		{kind: session.FenceStorageRead, name: "read-2"},
	} {
		f.seen = &seen
		c.AddFence(f)
	}
	if err := (&Engine{}).catalogFencesCurrentLocked(c, session.FenceStorageRead, session.FenceIndexSet, session.FenceNamespace); err != nil {
		t.Fatal(err)
	}
	want := []string{"read-1", "read-2", "index", "namespace"}
	if len(seen) != len(want) {
		t.Fatalf("checked %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("checked %v, want %v", seen, want)
		}
	}
	clone := c.Clone()
	if len(clone.Fences) != len(c.Fences) || &clone.Fences[0] == &c.Fences[0] {
		t.Fatal("journal clone must copy the fence list in order into its own slice")
	}
}

func TestCatalogFencesStopAtFirstFailure(t *testing.T) {
	var seen []string
	first, second := errors.New("first"), errors.New("second")
	c := session.NewJournal()
	c.AddFence(orderProbeFence{kind: session.FenceIndexSet, name: "index", err: second, seen: &seen})
	c.AddFence(orderProbeFence{kind: session.FenceStorageRead, name: "read", err: first, seen: &seen})
	if err := (&Engine{}).catalogFencesCurrentLocked(c, session.FenceStorageRead, session.FenceIndexSet); !errors.Is(err, first) {
		t.Fatalf("got %v, want the earlier kind's error", err)
	}
	if len(seen) != 1 || seen[0] != "read" {
		t.Fatalf("checked %v after the first failure", seen)
	}
}

// These owning tests use the core constructor and real admission/publication
// seams, without importing the composition (which would form an import cycle).
// The initial zero-column table is a descriptor/storage fixture; every marker
// below is an actual TRUNCATE, with the production fence and overlay adoption.
func fenceReplayFixture(t *testing.T) (*Engine, *session.Session, ids.ObjectAddress) {
	t.Helper()
	e, err := NewEngine(nil, nil, "", Startup{})
	if err != nil {
		t.Fatal(err)
	}
	login, err := e.ResolveLogin("postgres")
	if err != nil {
		t.Fatal(err)
	}
	admission, err := e.AdmitDatabase(context.Background(), login, 1, DatabaseSelector{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.StartSession(context.Background(), admission, session.SessionStart{ID: 1})
	admission.Release()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
	table := &catalog.Relation{ObjectAddress: address, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "marker", ObjectRevision: 1, Owner: login, RelationKind: catalog.TableRelation, Index: map[string]int{}, TableMethod: 2}
	e.Mu.Lock()
	state := catalog.CloneState(e.Catalog.State)
	state.SetDescriptor(address, table)
	state.BindName(catalog.DescriptorNameKey(table), address)
	e.Catalog.PublishLocked(state)
	e.Storage.InstallRelationLocked(address, &storage.Relation{Generation: 1})
	e.Mu.Unlock()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Begin(op, session.TxOptions{})
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	return e, s, address
}

func fenceReplaySQL(t *testing.T, e *Engine, s *session.Session, sql string) {
	t.Helper()
	statements, err := parser.Parse(sql)
	if err != nil || len(statements) != 1 {
		t.Fatal(sql, err)
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	if _, err := e.Execute(op, statements[0], 0, nil); err != nil {
		t.Fatal(sql, err)
	}
}

func fenceReplayDDL(t *testing.T, e *Engine, s *session.Session, name string, fail session.Fence, addresses ...map[ids.ObjectAddress]catalog.Descriptor) error {
	t.Helper()
	_, err := fenceReplayDDLAttempt(t, e, s, name, fail, false, addresses...)
	return err
}

func fenceReplayDDLAttempt(t *testing.T, e *Engine, s *session.Session, name string, fail session.Fence, invalidAuthority bool, addresses ...map[ids.ObjectAddress]catalog.Descriptor) (*session.Journal, error) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	statements, err := parser.Parse("CREATE SCHEMA " + name)
	if err != nil || len(statements) != 1 {
		t.Fatal(err)
	}
	if err := scope.Gate(statements[0]); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	base := e.PrivateCatalogLocked(scope.PendingCatalog())
	stage := e.BeginCatalogStage(base)
	oid, err := e.Catalog.AllocateOIDLocked()
	if invalidAuthority {
		stage.AuthSession = s
		stage.AuthRevision = s.Authorization.Revision + 1
		stage.RoleRevision = e.OperationRolesLocked(op).Revision
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	if err := stage.Create(&catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: oid}, ObjectName: name, ObjectRevision: 1, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}); err != nil {
		t.Fatal(err)
	}
	prior := scope.PendingCatalog()
	changes := prior.Clone()
	if fail != nil {
		changes.AddFence(fail)
	}
	for _, expected := range addresses {
		for address, descriptor := range stage.Descriptors() {
			expected[address] = descriptor
		}
		for address := range stage.Removals() {
			expected[address] = nil
		}
	}
	err = e.StageCatalogOverlay(scope, stage, changes)
	if err != nil && scope.PendingCatalog() != prior {
		t.Fatal("rejected overlay replaced the statement's actual entry journal")
	}
	return changes, scope.Finish(err)
}

func fenceReplayGraphs(t *testing.T, journal *session.Journal) []truncateGraphFence {
	t.Helper()
	var graphs []truncateGraphFence
	for _, fence := range journal.Fences {
		if graph, ok := fence.(truncateGraphFence); ok {
			graphs = append(graphs, graph)
		}
	}
	if len(graphs) == 0 {
		t.Fatal("actual TRUNCATE did not record any graph")
	}
	return graphs
}

func fenceReplayEntries(graph truncateGraphFence, current, base catalog.Read, journal *session.Journal) (dml.ReconcileMode, map[ids.ObjectAddress]catalog.Descriptor) {
	descriptors, mode := graph.ReconcileDescriptors(current, base, journal)
	entries := make(map[ids.ObjectAddress]catalog.Descriptor)
	for address, descriptor := range descriptors {
		entries[address] = descriptor
	}
	return mode, entries
}

func fenceReplaySnapshotGraphs(t *testing.T, journal *session.Journal) []truncateGraphFence {
	t.Helper()
	graphs := fenceReplayGraphs(t, journal)
	for i := range graphs {
		graphs[i].Targets = maps.Clone(graphs[i].Targets)
		graphs[i].Sequences = maps.Clone(graphs[i].Sequences)
	}
	return graphs
}

func fenceReplayAssertGraphs(t *testing.T, journal *session.Journal, saved []truncateGraphFence) {
	t.Helper()
	graphs := fenceReplayGraphs(t, journal)
	if len(graphs) != len(saved) {
		t.Fatal("saved graph list changed length")
	}
	for i, graph := range graphs {
		want := saved[i]
		if graph.Base != want.Base || graph.Checked != want.Checked || graph.ReceiptBase != want.ReceiptBase || graph.ReceiptMark != want.ReceiptMark || graph.Token != want.Token || graph.Restart != want.Restart || !maps.Equal(graph.Targets, want.Targets) || !maps.Equal(graph.Sequences, want.Sequences) {
			t.Fatal("saved graph provenance, receipt, targets or sequence bindings changed")
		}
	}
}

func TestTruncateReplayPrivateDDL(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(fmt.Sprintf("private_%v", private), func(t *testing.T) {
			e, s, target := fenceReplayFixture(t)
			if private {
				if err := fenceReplayDDL(t, e, s, "before_markers", nil); err != nil {
					t.Fatal(err)
				}
			}
			var admitted catalog.DescriptorVersion
			for marker := 1; marker <= 256; marker++ {
				fenceReplaySQL(t, e, s, "TRUNCATE marker")
				if marker == 1 {
					admitted = fenceReplayGraphs(t, s.Transactions.Catalog)[0].Base
				}
				if private && marker == 16 {
					saved := s.Transactions.Catalog
					old := fenceReplaySnapshotGraphs(t, saved)
					expected := make(map[ids.ObjectAddress]catalog.Descriptor)
					if err := fenceReplayDDL(t, e, s, "between_markers", nil, expected); err != nil {
						t.Fatal(err)
					}
					current := fenceReplayCurrent(e, s.Transactions.Catalog)
					if len(s.Transactions.Catalog.Descriptors) < 2 {
						t.Fatal("private DDL inventory is empty")
					}
					fenceReplayAssertGraphs(t, saved, old)
					if len(expected) != 1 {
						t.Fatal("between_markers stage must change exactly its new namespace")
					}
					for _, descriptor := range expected {
						if namespace, ok := descriptor.(*catalog.Namespace); !ok || namespace.ObjectName != "between_markers" {
							t.Fatal("between_markers stage did not record its real namespace")
						}
					}
					for i, graph := range fenceReplayGraphs(t, saved) {
						if graph.Checked != old[i].Checked || graph.Base != old[i].Base || graph.ReceiptBase != old[i].ReceiptBase || graph.ReceiptMark != old[i].ReceiptMark || graph.Base != admitted {
							t.Fatal("new DDL refreshed a saved journal or original admitted baseline")
						}
						if mode, entries := fenceReplayEntries(graph, current, e.Catalog.Current(), s.Transactions.Catalog); mode != dml.ReconcileDelta || !maps.Equal(entries, expected) {
							t.Fatal("older saved receipt counterfeited the later private generation")
						}
					}
				}
				if marker == 32 || marker == 64 || marker == 128 || marker == 256 {
					journal := s.Transactions.Catalog
					current := fenceReplayCurrent(e, journal)
					graphs := fenceReplayGraphs(t, journal)
					if len(graphs) != marker {
						t.Fatalf("retained graphs %d, want %d actual markers", len(graphs), marker)
					}
					entries := 0
					for _, graph := range graphs {
						mode, reconciled := fenceReplayEntries(graph, current, e.Catalog.Current(), journal)
						if mode != dml.ReconcileExact && mode != dml.ReconcileDelta {
							t.Fatal("current adopted receipt fell back to the full inventory")
						}
						entries += len(reconciled)
						if graph.Targets[target] == 0 || graph.Base.Get(target) != admitted.Get(target) {
							t.Fatal("actual overlay lost whole-preflight receipt, target or admitted provenance")
						}
					}
					if entries != 0 {
						t.Fatalf("%d unchanged retained markers enumerated %d actual descriptors", marker, entries)
					}
					e.Mu.Lock()
					err := e.CatalogPreflightLocked(journal)
					e.Mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestTruncateReplayReceiptIsolation(t *testing.T) {
	e, s, target := fenceReplayFixture(t)
	for range 4 {
		fenceReplaySQL(t, e, s, "TRUNCATE marker")
	}
	fenceReplaySQL(t, e, s, "SAVEPOINT parent_mark")
	parent := s.Transactions.Savepoints[s.Transactions.FindSavepoint("parent_mark")].Catalog
	graphs := fenceReplaySnapshotGraphs(t, parent)
	parentFences := parent.Fences
	parentRoot := parent.Storage[target].Replacement
	if err := fenceReplayDDL(t, e, s, "child_ddl", nil); err != nil {
		t.Fatal(err)
	}
	fenceReplaySQL(t, e, s, "TRUNCATE marker")
	child := s.Transactions.Catalog
	childGraphs := fenceReplaySnapshotGraphs(t, child)
	childRoot := child.Storage[target].Replacement
	fenceReplaySQL(t, e, s, "ROLLBACK TO parent_mark")
	if s.Transactions.Catalog != parent || parent.Storage[target].Replacement != parentRoot {
		t.Fatal("rollback failed to restore exact parent journal/storage root")
	}
	fenceReplayAssertGraphs(t, parent, graphs)
	if len(parent.Fences) != len(parentFences) || &parent.Fences[0] != &parentFences[0] {
		t.Fatal("discarded child replaced the saved fence slice")
	}
	fenceReplaySQL(t, e, s, "TRUNCATE marker")
	if child.Storage[target].Replacement != childRoot || &child.Fences[0] == &s.Transactions.Catalog.Fences[0] {
		t.Fatal("reappend reused discarded child fence slice/storage")
	}
	fenceReplayAssertGraphs(t, child, childGraphs)
	fenceReplaySQL(t, e, s, "ROLLBACK TO parent_mark")
	fenceReplaySQL(t, e, s, "RELEASE parent_mark")
	// Finish aborts an explicit transaction without a live mark. Retain the
	// real failure boundary, rather than expecting an aborted block's journal.
	fenceReplaySQL(t, e, s, "SAVEPOINT rejected_stage")
	entry := s.Transactions.Savepoints[s.Transactions.FindSavepoint("rejected_stage")].Catalog
	entryGraphs := fenceReplaySnapshotGraphs(t, entry)
	entryRoot := entry.Storage[target].Replacement
	// A real graph check succeeds against a changed private inventory, then
	// the later materialized-source kind fails. No saved receipt can change.
	var seen []string
	failure := errors.New("later materialized-source fence")
	expected := make(map[ids.ObjectAddress]catalog.Descriptor)
	rejected, err := fenceReplayDDLAttempt(t, e, s, "failed_ddl", orderProbeFence{kind: fenceMaterializedSource, name: "later", err: failure, seen: &seen}, false, expected)
	if !errors.Is(err, failure) {
		t.Fatal("later-kind preflight did not veto", err)
	}
	if !slices.Equal(seen, []string{"later"}) || s.Transactions.Catalog != entry || entry.Storage[target].Replacement != entryRoot || rejected == entry || &rejected.Fences[0] == &entry.Fences[0] {
		t.Fatal("failed later-kind stage changed the saved journal/storage/fence ownership")
	}
	fenceReplayAssertGraphs(t, parent, graphs)
	fenceReplayAssertGraphs(t, entry, entryGraphs)
	fenceReplayAssertGraphs(t, rejected, entryGraphs)
	current := fenceReplayCurrent(e, rejected)
	for i, graph := range fenceReplayGraphs(t, rejected) {
		mode, entries := fenceReplayEntries(graph, current, e.Catalog.Current(), rejected)
		if graph.Checked != entryGraphs[i].Checked || graph.Base != entryGraphs[i].Base || graph.ReceiptBase != entryGraphs[i].ReceiptBase || graph.ReceiptMark != entryGraphs[i].ReceiptMark || !maps.Equal(graph.Targets, entryGraphs[i].Targets) || !maps.Equal(graph.Sequences, entryGraphs[i].Sequences) || mode != dml.ReconcileDelta || len(expected) != 1 || !maps.Equal(entries, expected) {
			t.Fatal("failed later-kind candidate acquired unchecked graph receipts")
		}
	}
}

func fenceReplayCurrent(e *Engine, journal *session.Journal) catalog.Read {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.PrivateCatalogLocked(journal)
}

func TestCatalogFencesRealTruncateFirstFailure(t *testing.T) {
	e, s, target := fenceReplayFixture(t)
	fenceReplaySQL(t, e, s, "TRUNCATE marker")
	graph := fenceReplayGraphs(t, s.Transactions.Catalog)[0]
	changed := s.Transactions.Catalog.Clone()
	child := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 21000}, ObjectName: "incoming"}
	child.Constraints.ForeignKeys = []catalog.ForeignKeyConstraint{{Target: target}}
	changed.RecordDescriptor(e.Catalog.State, child.Address(), child)
	first := errors.New("earlier recorded truncate-kind failure")
	for _, graphFirst := range []bool{false, true} {
		var seen []string
		journal := changed.Clone()
		journal.Fences = nil
		probe := orderProbeFence{kind: fenceTruncateGraph, name: "first", err: first, seen: &seen}
		if graphFirst {
			journal.AddFence(graph)
			journal.AddFence(probe)
		} else {
			journal.AddFence(probe)
			journal.AddFence(graph)
		}
		journal.AddFence(orderProbeFence{kind: fenceTruncateGraph, name: "unreachable", seen: &seen})
		e.Mu.Lock()
		err := e.catalogFencesCurrentLocked(journal, fenceTruncateGraph)
		e.Mu.Unlock()
		if graphFirst {
			if err != (catalog.Conflict{}) || len(seen) != 0 {
				t.Fatal("real graph failure did not stop before later recorded fences", err, seen)
			}
		} else if err != first || !slices.Equal(seen, []string{"first"}) {
			t.Fatal("recorded first failure was replaced by graph/later failure", err, seen)
		}
	}
}

func TestTruncateReplayRestartCheckpoint(t *testing.T) {
	e, s, target := fenceReplayFixture(t)
	address, published := fenceReplaySequence(e, target)
	for range 4 {
		fenceReplaySQL(t, e, s, "TRUNCATE marker RESTART IDENTITY")
	}
	parent := s.Transactions.Catalog
	graphs := fenceReplaySnapshotGraphs(t, parent)
	sequenceMaps := make([]map[ids.ObjectAddress]*storage.SequenceGenerator, len(graphs))
	targetMaps := make([]map[ids.ObjectAddress]int, len(graphs))
	for i, graph := range graphs {
		sequenceMaps[i], targetMaps[i] = maps.Clone(graph.Sequences), maps.Clone(graph.Targets)
		if len(graph.Sequences) != 1 || graph.Sequences[address] != published {
			t.Fatal("actual restart graph did not retain the committed binding")
		}
	}
	root, generator := parent.Storage[target].Replacement, parent.Generators[address]
	fenceReplaySQL(t, e, s, "SAVEPOINT restart_parent")
	if err := fenceReplayDDL(t, e, s, "restart_child_ddl", nil); err != nil {
		t.Fatal(err)
	}
	fenceReplaySQL(t, e, s, "TRUNCATE marker RESTART IDENTITY")
	fenceReplaySQL(t, e, s, "SAVEPOINT restart_child")
	fenceReplaySQL(t, e, s, "TRUNCATE marker RESTART IDENTITY")
	fenceReplaySQL(t, e, s, "RELEASE restart_child")
	discarded := s.Transactions.Catalog
	discardedGraphs := fenceReplaySnapshotGraphs(t, discarded)
	discardedRoot, discardedGenerator := discarded.Storage[target].Replacement, discarded.Generators[address]
	fenceReplaySQL(t, e, s, "ROLLBACK TO restart_parent")
	if s.Transactions.Catalog != parent || parent.Storage[target].Replacement != root || parent.Generators[address] != generator {
		t.Fatal("restart rollback lost exact parent storage/generator/journal roots")
	}
	fenceReplaySQL(t, e, s, "TRUNCATE marker RESTART IDENTITY")
	fenceReplayAssertGraphs(t, parent, graphs)
	fenceReplayAssertGraphs(t, discarded, discardedGraphs)
	for i, graph := range fenceReplayGraphs(t, parent) {
		if graph.Base != graphs[i].Base || graph.Checked != graphs[i].Checked || graph.ReceiptBase != graphs[i].ReceiptBase || graph.ReceiptMark != graphs[i].ReceiptMark || !maps.Equal(graph.Sequences, sequenceMaps[i]) || !maps.Equal(graph.Targets, targetMaps[i]) {
			t.Fatal("rollback/release/reappend mutated saved restart graph authority")
		}
	}
	if discarded.Storage[target].Replacement != discardedRoot || discarded.Generators[address] != discardedGenerator || e.Storage.Sequences[address] != published || published.Last != 9 || published.Called {
		t.Fatal("reappend changed discarded or published sequence/storage state")
	}
	for i, graph := range fenceReplayGraphs(t, discarded) {
		if graph.Base != discardedGraphs[i].Base || graph.Checked != discardedGraphs[i].Checked || graph.ReceiptBase != discardedGraphs[i].ReceiptBase || graph.ReceiptMark != discardedGraphs[i].ReceiptMark {
			t.Fatal("discarded released-child receipts changed")
		}
	}
}

func TestTruncateReplayException(t *testing.T) {
	e, s, target := fenceReplayFixture(t)
	address, published := fenceReplaySequence(e, target)
	fenceReplaySQL(t, e, s, "TRUNCATE marker RESTART IDENTITY")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	statements, err := parser.Parse("TRUNCATE marker RESTART IDENTITY")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scope.Execute(statements[0], 0, nil); err != nil {
		t.Fatal(err)
	}
	// A nested command needs fresh lexical admission (not another Execute on
	// the outer statement's already bound TRUNCATE manifest).
	invoke := func() (err error) {
		restore := scope.ActivateSQLRoutine(nil, catalog.Read{})
		defer func() { err = restore(err) }()
		finish, err := scope.BeginRoutineCommand()
		if err != nil {
			return err
		}
		defer func() { err = finish(err) }()
		if err := scope.AcquireRelation(statements[0], true); err != nil {
			return err
		}
		_, err = e.executeStatement(scope, statements[0], 0, nil)
		return err
	}
	parent := scope.Catalog
	parentGraphs := fenceReplaySnapshotGraphs(t, parent)
	parentFences := parent.Fences
	sequences := parentGraphs[0].Sequences
	if len(sequences) != 1 || sequences[address] != published {
		t.Fatal("exception fixture did not retain a real committed sequence map")
	}
	root := parent.Storage[target].Replacement
	generator := parent.Generators[address]
	mark, err := scope.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	if mark.catalog != parent || s.Transactions.Savepoints[len(s.Transactions.Savepoints)-1].StatementCatalog != parent {
		t.Fatal("exception mark did not retain the actual enclosing journal")
	}
	if err := invoke(); err != nil {
		t.Fatal(err)
	}
	discarded := scope.Catalog
	discardedGraphs := fenceReplaySnapshotGraphs(t, discarded)
	discardedFences := discarded.Fences
	discardedRoot, discardedGenerator := discarded.Storage[target].Replacement, discarded.Generators[address]
	if len(discardedGraphs) != len(parentGraphs)+1 || &discarded.Fences[0] == &parent.Fences[0] || discardedRoot == root || discardedGenerator == generator {
		t.Fatal("exception child did not own a genuine new truncate/fence/generator")
	}
	if err := scope.EndPLException(mark, true, errors.New("body failure")); err != nil {
		t.Fatal(err)
	}
	if scope.Catalog != parent || parent.Storage[target].Replacement != root || parent.Generators[address] != generator {
		t.Fatal("exception rollback did not restore actual statement journal/root")
	}
	if err := invoke(); err != nil {
		t.Fatal(err)
	}
	reappended := scope.Catalog
	if len(fenceReplayGraphs(t, reappended)) != len(parentGraphs)+1 || &reappended.Fences[0] == &parent.Fences[0] || &reappended.Fences[0] == &discarded.Fences[0] || reappended.Storage[target].Replacement == root || reappended.Storage[target].Replacement == discardedRoot || reappended.Generators[address] == generator || reappended.Generators[address] == discardedGenerator {
		t.Fatal("exception reappend reused saved or discarded child ownership")
	}
	fenceReplayAssertGraphs(t, parent, parentGraphs)
	fenceReplayAssertGraphs(t, discarded, discardedGraphs)
	if len(parent.Fences) != len(parentFences) || &parent.Fences[0] != &parentFences[0] || len(discarded.Fences) != len(discardedFences) || &discarded.Fences[0] != &discardedFences[0] {
		t.Fatal("exception/reappend replaced a retained fence slice")
	}
	if discarded.Storage[target].Replacement != discardedRoot || discarded.Generators[address] != discardedGenerator || e.Storage.Sequences[address] != published || published.Last != 9 || published.Called {
		t.Fatal("exception reappend changed discarded or published roots/generator state")
	}
	// A changed graph must still reconcile; discarded-child success cannot
	// authorize the parent or its reappended successor.
	changed := scope.Catalog.Clone()
	child := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 22000}, ObjectName: "late"}
	child.Constraints.ForeignKeys = []catalog.ForeignKeyConstraint{{Target: target}}
	changed.RecordDescriptor(e.Catalog.State, child.Address(), child)
	e.Mu.Lock()
	err = e.catalogFencesCurrentLocked(changed, fenceTruncateGraph)
	e.Mu.Unlock()
	if err != (catalog.Conflict{}) {
		t.Fatal("discarded exception authority authorized a later incoming edge", err)
	}
}

func fenceReplaySequence(e *Engine, target ids.ObjectAddress) (ids.ObjectAddress, *storage.SequenceGenerator) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	base := e.Catalog.Current()
	state := catalog.CloneState(base.State())
	marker := *state.MustRelation(target)
	integer, _ := base.LookupType(sqltype.Int8)
	marker.Columns = []catalog.Column{{Name: "id", Usage: integer.Usage()}}
	marker.Attributes = []int32{1}
	marker.ColumnMeta.HighWater = 1
	marker.Index = map[string]int{"id": 0}
	state.SetDescriptor(target, &marker)
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}
	owned := target
	owned.SubID = 1
	options := catalog.SequenceOptions{SQLType: sqltype.Int8, Start: 9, Increment: 1, Min: 1, Max: 9223372036854775807, Cache: 1, Owned: owned}
	descriptor := sequence.NewRelation(sequence.RelationSpec{Address: address, Namespace: marker.Namespace(), Name: "marker_id_seq", Revision: 1, Owner: marker.Owner, Columns: sequence.Columns(base), Options: options})
	state.SetDescriptor(address, descriptor)
	state.BindName(catalog.DescriptorNameKey(descriptor), address)
	e.Catalog.PublishLocked(state)
	published := sequence.InitialGenerator(options, nil, 1)
	e.Storage.ReplaceSequencesLocked(map[ids.ObjectAddress]*storage.SequenceGenerator{address: published})
	return address, published
}

func TestTruncateReplayAdmissionReceiptIsolation(t *testing.T) {
	e, s, target := fenceReplayFixture(t)
	for range 4 {
		fenceReplaySQL(t, e, s, "TRUNCATE marker")
	}
	fenceReplaySQL(t, e, s, "SAVEPOINT admission_parent")
	parent := s.Transactions.Savepoints[s.Transactions.FindSavepoint("admission_parent")].Catalog
	graphs := fenceReplaySnapshotGraphs(t, parent)
	root := parent.Storage[target].Replacement
	var seen []string
	expected := make(map[ids.ObjectAddress]catalog.Descriptor)
	rejected, err := fenceReplayDDLAttempt(t, e, s, "admission_failure", orderProbeFence{kind: fenceTruncateGraph, name: "after_real_graphs", seen: &seen}, true, expected)
	if err == nil || err.Error() != "stuntgres: authorization changed before publication" || !slices.Equal(seen, []string{"after_real_graphs"}) || s.Transactions.Catalog != parent || parent.Storage[target].Replacement != root || rejected == parent || &rejected.Fences[0] == &parent.Fences[0] {
		t.Fatal("post-preflight authorization veto did not preserve saved journal/storage/fence ownership", err, seen)
	}
	fenceReplayAssertGraphs(t, rejected, graphs)
	current := fenceReplayCurrent(e, rejected)
	for i, graph := range fenceReplayGraphs(t, rejected) {
		mode, entries := fenceReplayEntries(graph, current, e.Catalog.Current(), rejected)
		if graph.Base != graphs[i].Base || graph.Checked != graphs[i].Checked || graph.ReceiptBase != graphs[i].ReceiptBase || graph.ReceiptMark != graphs[i].ReceiptMark || !maps.Equal(graph.Targets, graphs[i].Targets) || !maps.Equal(graph.Sequences, graphs[i].Sequences) || mode != dml.ReconcileDelta || len(expected) != 1 || !maps.Equal(entries, expected) {
			t.Fatal("complete graph preflight followed by failed admission minted a receipt")
		}
	}
	fenceReplayAssertGraphs(t, parent, graphs)
}

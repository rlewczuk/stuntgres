package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

type concurrentUnitSink struct{ calls, bytes int }

func (w *concurrentUnitSink) Write(data []byte) (int, error) {
	w.calls++
	w.bytes += len(data)
	return len(data), nil
}

func TestConcurrentIndexU7InvalidDumpAndSuccessOnlyRepair(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(n INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1),(1)")
	concurrentUnitError(t, s, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", "23505")
	initial, root := concurrentUnitIndex(t, e, "ui", false, false)
	if root != nil {
		t.Fatal("first-build failure installed a root")
	}
	sink := new(concurrentUnitSink)
	err := e.Dump(context.Background(), sink)
	if err == nil || err.Error() != `stuntgres: dump cannot serialize invalid index "public"."ui"` || sink.calls != 0 || sink.bytes != 0 {
		t.Fatal(err, sink)
	}
	concurrentUnitError(t, s, "REINDEX INDEX ui", "23505")
	unchanged, root := concurrentUnitIndex(t, e, "ui", false, false)
	if unchanged != initial || root != nil {
		t.Fatal("failed repair changed descriptor or absent root")
	}
	sessionSQL(t, s, "DELETE FROM t WHERE n=1")
	sessionSQL(t, s, "REINDEX TABLE t")
	healthy, _ := concurrentUnitIndex(t, e, "ui", true, true)
	concurrentUnitRootMatchesStorage(t, e, healthy)
	sessionSQL(t, s, "INSERT INTO t VALUES(2)")
	concurrentUnitError(t, s, "INSERT INTO t VALUES(2)", "23505")
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "CREATE UNIQUE INDEX") || strings.Contains(dump.String(), "CONCURRENTLY") {
		t.Fatal("valid standalone dump is not ordinary executable SQL", dump.String())
	}
	restored := New()
	if err := restored.Restore(context.Background(), strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	consumer := sessionStart(t, restored, nil)
	concurrentUnitError(t, consumer, "INSERT INTO t VALUES(2)", "23505")
	imported, _ := concurrentUnitIndex(t, restored, "ui", true, true)
	if imported.SQLIndex.Constraint != nil {
		t.Fatal("restore fabricated backing constraint")
	}
	concurrentUnitRootMatchesStorage(t, restored, imported)
	lockEmpty(t, e)
	lockEmpty(t, restored)
}

func TestConcurrentIndexU6ReadyCancellationReleasesSUE(t *testing.T) {
	e := New()
	reader, builder, consumer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, reader, "CREATE TABLE t(n INT)")
	sessionSQL(t, reader, "CREATE TABLE u(n INT)")
	sessionSQL(t, reader, "INSERT INTO t VALUES(1)")
	sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	rows := sessionSQL(t, reader, "SELECT n FROM u")
	rows.Rows.Close()
	cancel, await := indexMaintenanceAsync(t, e, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)")
	concurrentUnitCompletion(t, e, reader.Transactions.Ticket, builder)
	ready, root := concurrentUnitIndex(t, e, "ui", true, false)
	_, _, _, waiting, writers, inspectErr := e.InspectIndex(context.Background(), "public", "ui")
	if inspectErr != nil || !waiting || writers {
		t.Fatal("real older cohort not exposed exclusively by its coordinator", waiting, writers, inspectErr)
	}
	cancel()
	if err := await(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	retained, after := concurrentUnitIndex(t, e, "ui", true, false)
	if retained != ready || after != root {
		t.Fatal("older-phase cancellation retracted ready root")
	}
	_, _, _, waiting, writers, inspectErr = e.InspectIndex(context.Background(), "public", "ui")
	if inspectErr != nil || waiting || writers {
		t.Fatal("canceled coordinator retained cohort admission", waiting, writers, inspectErr)
	}
	sink := new(concurrentUnitSink)
	if err := e.Dump(context.Background(), sink); err == nil || sink.calls != 0 {
		t.Fatal(err, sink)
	}
	concurrentUnitError(t, consumer, "INSERT INTO t VALUES(1)", "23505")
	// Reader owns u only: DROP's target AX must be free after builder cancellation.
	sessionSQL(t, consumer, "DROP INDEX ui")
	sessionSQL(t, consumer, "INSERT INTO t VALUES(1)")
	sessionSQL(t, reader, "ROLLBACK")
	lockEmpty(t, e)
	e.mu.Lock()
	remaining := len(e.visibility.Entries)
	e.mu.Unlock()
	if remaining != 0 {
		t.Fatal("canceled builder registry leak")
	}
}

func TestConcurrentIndexU6BeforeDescriptorAndWriterCancellation(t *testing.T) {
	for _, phase := range []string{"before_descriptor", "first_writer", "second_writer"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			a, b, d := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(n INT)")
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			sessionSQL(t, a, "BEGIN ISOLATION LEVEL READ COMMITTED")
			if phase == "before_descriptor" {
				sessionSQL(t, a, "LOCK TABLE t IN SHARE MODE")
			} else {
				sessionSQL(t, a, "INSERT INTO t VALUES(2)")
			}
			cancel, await := indexMaintenanceAsync(t, e, b, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)")
			if phase == "before_descriptor" {
				lockPending(t, e, 1)
				ref := roleTableRef(t, e, "t")
				lockState := e.locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
				exact := len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.ShareUpdateExclusive
				if !exact {
					t.Fatal("predescriptor SUE acquisition not actually pending")
				}
			} else {
				concurrentUnitCompletion(t, e, a.Transactions.Ticket, b)
				if phase == "second_writer" {
					sessionSQL(t, d, "BEGIN ISOLATION LEVEL READ COMMITTED")
					sessionSQL(t, d, "INSERT INTO t VALUES(3)")
					sessionSQL(t, a, "COMMIT")
					concurrentUnitCompletion(t, e, d.Transactions.Ticket, b)
					concurrentUnitIndex(t, e, "ui", true, false)
				} else {
					concurrentUnitIndex(t, e, "ui", false, false)
				}
				ready, valid, live, older, writers, err := e.InspectIndex(context.Background(), "public", "ui")
				if err != nil || ready != (phase == "second_writer") || valid || !live || older || !writers {
					t.Fatal("frozen writer cohort not observed", ready, valid, live, older, writers, err)
				}
			}
			cancel()
			if err := await(); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if phase != "before_descriptor" {
				_, _, _, older, writers, err := e.InspectIndex(context.Background(), "public", "ui")
				if err != nil || older || writers {
					t.Fatal("canceled builder retained a cohort observation", older, writers, err)
				}
			}
			if phase == "before_descriptor" {
				_, _, _, _, _, err := e.InspectIndex(context.Background(), "public", "ui")
				if err == nil {
					t.Fatal("canceled acquisition published descriptor")
				}
				sessionSQL(t, a, "ROLLBACK")
			} else if phase == "first_writer" {
				sessionSQL(t, a, "COMMIT")
				concurrentUnitIndex(t, e, "ui", false, false)
				sessionSQL(t, d, "REINDEX INDEX ui")
				index, _ := concurrentUnitIndex(t, e, "ui", true, true)
				concurrentUnitRootMatchesStorage(t, e, index)
			} else {
				sessionSQL(t, d, "COMMIT")
				concurrentUnitIndex(t, e, "ui", true, false)
				sessionSQL(t, d, "REINDEX INDEX ui")
				index, _ := concurrentUnitIndex(t, e, "ui", true, true)
				concurrentUnitRootMatchesStorage(t, e, index)
			}
			lockEmpty(t, e)
		})
	}
}

func TestConcurrentIndexU6LateSuccessCannotBeRetracted(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(n INT)")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(statement(t, "CREATE INDEX CONCURRENTLY ix ON t(n)"), 0, nil)
	if err != nil || result.Count != 0 || !scope.completed {
		scope.Finish(err)
		t.Fatal(result, err)
	}
	cancel()
	if err := scope.Finish(nil); err != nil {
		t.Fatal("late cancellation retracted installed validity", err)
	}
	index, _ := concurrentUnitIndex(t, e, "ix", true, true)
	concurrentUnitRootMatchesStorage(t, e, index)
	lockEmpty(t, e)
}

func TestConcurrentIndexU7OrdinaryUniqueUsesPublicParseAndExecute(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(n INT)")
	statements, err := parser.Parse("CREATE UNIQUE INDEX ui ON t(n) NULLS NOT DISTINCT")
	if err != nil {
		t.Fatal(err)
	}
	before := e.CatalogRead().State()
	op := transactionOperation(t, s)
	result, err := e.Execute(op, statements[0], 0, nil)
	op.Done()
	if err != nil || result.Count != 0 || e.CatalogRead().State() == before {
		t.Fatal("ordinary UNIQUE public execution", result, err)
	}
	index, _ := concurrentUnitIndex(t, e, "ui", true, true)
	if !index.SQLIndex.Unique || !index.SQLIndex.NullsNotDistinct || index.SQLIndex.Constraint != nil {
		t.Fatal("ordinary UNIQUE definition identity")
	}
	sessionSQL(t, s, "INSERT INTO t VALUES(NULL)")
	concurrentUnitError(t, s, "INSERT INTO t VALUES(NULL)", "23505")
	lockEmpty(t, e)
}

func TestConcurrentIndexU7ReplacementRejectsInvalidSourceAndIsAtomic(t *testing.T) {
	source, target := New(), New()
	a, b := sessionStart(t, source, nil), sessionStart(t, target, nil)
	sessionSQL(t, a, "CREATE TABLE t(n INT)")
	sessionSQL(t, a, "INSERT INTO t VALUES(1),(1)")
	concurrentUnitError(t, a, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", "23505")
	sessionSQL(t, b, "CREATE TABLE sentinel(n INT)")
	sessionSQL(t, b, "INSERT INTO sentinel VALUES(9)")
	sourceState, targetState := source.CatalogRead().State(), target.CatalogRead().State()
	oldOID, oldRevision, oldRow, oldVersion := target.catalog.nextOID, target.catalog.nextRevision, target.storage.RowFrontier(), target.storage.VersionFrontier()
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err == nil {
		t.Fatal("invalid source was imported/healed")
	}
	if target.CatalogRead().State() != targetState || source.CatalogRead().State() != sourceState || target.catalog.nextOID != oldOID || target.catalog.nextRevision != oldRevision || target.storage.RowFrontier() != oldRow || target.storage.VersionFrontier() != oldVersion {
		t.Fatal("failed replacement changed source or destination authority")
	}
	for _, stream := range []string{
		"CREATE TABLE t(n INT); INSERT INTO t VALUES(1),(1); CREATE UNIQUE INDEX ui ON t(n);",
		"CREATE TABLE t(n INT); CREATE INDEX CONCURRENTLY ix ON t(n);",
		"CREATE TABLE t(n INT); INSERT INTO t VALUES(1); SELECT FROM",
	} {
		if err := target.Restore(context.Background(), strings.NewReader(stream)); err == nil {
			t.Fatal("invalid isolated stream succeeded", stream)
		}
		if target.CatalogRead().State() != targetState || target.catalog.nextOID != oldOID || target.catalog.nextRevision != oldRevision {
			t.Fatal("isolated failed stream mutated destination", stream)
		}
	}
	lockEmpty(t, source)
	lockEmpty(t, target)
}

func TestConcurrentIndexU7RetainedInvalidTempIsNotImportedOrHealed(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	// TEMP table SQL is outside native admission. Reuse the legitimate
	// catalog-provider component seam, then exercise real SQL index phases.
	sessionSQL(t, s, "CREATE TABLE temp_prototype(n INT)")
	prototype := e.CatalogRead().MustRelation(roleTableRef(t, e, "temp_prototype").Address)
	op := transactionOperation(t, s)
	arrayComponentTemporary(t, e, op, prototype)
	op.Done()
	sessionSQL(t, s, "ALTER TABLE array_component_temp RENAME TO retained_ci_temp")
	sessionSQL(t, s, "INSERT INTO retained_ci_temp VALUES(1),(1)")
	concurrentUnitError(t, s, "CREATE UNIQUE INDEX CONCURRENTLY retained_ci_temp_index ON retained_ci_temp(n)", "23505")
	var original *catalog.Relation
	e.mu.Lock()
	for _, descriptor := range e.catalog.state.Descriptors() {
		if index, ok := descriptor.(*catalog.Relation); ok && index.Name() == "retained_ci_temp_index" {
			namespace := e.catalog.state.MustNamespace(index.Namespace())
			if namespace.Temporary == s.SessionToken {
				original = index
			}
		}
	}
	e.mu.Unlock()
	if original == nil || original.SQLIndex.Ready || original.SQLIndex.Valid {
		t.Fatal("real retained TEMP invalid descriptor missing")
	}
	if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE imported(n INT); INSERT INTO imported VALUES(7);")); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	retained := e.catalog.state.Get(original.Address()) == original && e.storage.IndexRoots[original.Address()] == nil
	e.mu.Unlock()
	if !retained {
		t.Fatal("replacement healed or replaced retained not-ready TEMP descriptor")
	}
	sessionSQL(t, s, "INSERT INTO retained_ci_temp VALUES(1)")
	sessionSQL(t, s, "DROP INDEX retained_ci_temp_index")
	lockEmpty(t, e)
}

func TestConcurrentIndexU1U6DetachedCandidateSourceAndFinalVeto(t *testing.T) {
	for _, mode := range []string{"cancel", "source_change"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(n INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1),(1)")
			concurrentUnitError(t, s, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", "23505")
			sessionSQL(t, s, "DELETE FROM t")
			sessionSQL(t, s, "INSERT INTO t VALUES(2)")
			original, _ := concurrentUnitIndex(t, e, "ui", false, false)
			base := e.CatalogRead()
			e.mu.Lock()
			parent := e.catalog.state.MustRelation(original.SQLIndex.Parent)
			store := e.storage.Relations[parent.Address()]
			snapshot := *store
			e.mu.Unlock()
			next, definition := *original, *original.SQLIndex
			definition.Ready = true
			next.SQLIndex = &definition
			next.ObjectRevision++
			root, err := buildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, &definition, &snapshot, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateUniqueRoot(context.Background(), parent, &next, root, "public", sqltype.OutputEnvironment{}, nil, nil, true); err != nil {
				t.Fatal(err)
			}
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			stage.indexes().parents = map[ids.ObjectAddress]*catalog.Relation{parent.Address(): parent}
			stage.indexes().candidates = map[ids.ObjectAddress]indexRootCandidate{next.Address(): {index: &next, root: root, storage: store, generation: snapshot.Generation, ends: snapshot.Ends, length: len(snapshot.Versions)}}
			if err := stage.Replace(&next); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				cancel()
			} else {
				sessionSQL(t, s, "INSERT INTO t VALUES(3)")
			}
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()})
			if mode == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else {
				if _, ok := err.(catalog.Conflict); !ok {
					t.Fatal("stale detached source was published", err)
				}
			}
			retained, absent := concurrentUnitIndex(t, e, "ui", false, false)
			if retained != original || absent != nil || e.CatalogRead().State() != base.State() {
				t.Fatal("final veto/source conflict mutated committed phase")
			}
			lockEmpty(t, e)
		})
	}
}

func TestConcurrentIndexU7OlderAdmissionEpochFence(t *testing.T) {
	e := New()
	reader, builder := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, reader, "CREATE TABLE t(n INT)")
	sessionSQL(t, reader, "CREATE TABLE u(n INT)")
	sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	rows := sessionSQL(t, reader, "SELECT n FROM u")
	rows.Rows.Close()
	_, await := indexMaintenanceAsync(t, e, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)")
	concurrentUnitCompletion(t, e, reader.Transactions.Ticket, builder)
	_, _, _, waiting, writers, err := e.InspectIndex(context.Background(), "public", "ix")
	if err != nil || !waiting || writers {
		t.Fatal("actual older admission absent or mixed with writer admission", waiting, writers, err)
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	fenced := len(e.indexWaits.waits) == 0
	e.mu.Unlock()
	if !fenced {
		t.Fatal("replacement retained an older-cohort observation")
	}
	if err := await(); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("old coordinator did not finish at the actual epoch fence", err)
	}
	sessionSQL(t, reader, "ROLLBACK")
	sessionSQL(t, builder, "CREATE TABLE t(n INT)")
	sessionSQL(t, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)")
	ready, valid, live, waiting, writers, err := e.InspectIndex(context.Background(), "public", "ix")
	if err != nil || !ready || !valid || !live || waiting || writers {
		t.Fatal("replacement index inherited old admission", ready, valid, live, waiting, writers, err)
	}
	lockEmpty(t, e)
}

package core_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type concurrentUnitSink struct{ calls, bytes int }

func (w *concurrentUnitSink) Write(data []byte) (int, error) {
	w.calls++
	w.bytes += len(data)
	return len(data), nil
}

func TestConcurrentIndexU7InvalidDumpAndSuccessOnlyRepair(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(n INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(1)")
	concurrentUnitError(t, s, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", "23505")
	initial, root := concurrentUnitIndex(t, e, "ui", false, false)
	if root != nil {
		t.Fatal("first-build failure installed a root")
	}
	sink := new(concurrentUnitSink)
	err := engine.Dump(context.Background(), e, sink)
	if err == nil || err.Error() != `stuntgres: dump cannot serialize invalid index "public"."ui"` || sink.calls != 0 || sink.bytes != 0 {
		t.Fatal(err, sink)
	}
	concurrentUnitError(t, s, "REINDEX INDEX ui", "23505")
	unchanged, root := concurrentUnitIndex(t, e, "ui", false, false)
	if unchanged != initial || root != nil {
		t.Fatal("failed repair changed descriptor or absent root")
	}
	enginetest.SessionSQL(t, s, "DELETE FROM t WHERE n=1")
	enginetest.SessionSQL(t, s, "REINDEX TABLE t")
	healthy, _ := concurrentUnitIndex(t, e, "ui", true, true)
	concurrentUnitRootMatchesStorage(t, e, healthy)
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(2)")
	concurrentUnitError(t, s, "INSERT INTO t VALUES(2)", "23505")
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "CREATE UNIQUE INDEX") || strings.Contains(dump.String(), "CONCURRENTLY") {
		t.Fatal("valid standalone dump is not ordinary executable SQL", dump.String())
	}
	restored := engine.New()
	if err := engine.Restore(context.Background(), restored, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	consumer := enginetest.StartSession(t, restored, nil)
	concurrentUnitError(t, consumer, "INSERT INTO t VALUES(2)", "23505")
	imported, _ := concurrentUnitIndex(t, restored, "ui", true, true)
	if imported.SQLIndex.Constraint != nil {
		t.Fatal("restore fabricated backing constraint")
	}
	concurrentUnitRootMatchesStorage(t, restored, imported)
	enginetest.LockEmpty(t, e)
	enginetest.LockEmpty(t, restored)
}

func TestConcurrentIndexU6ReadyCancellationReleasesSUE(t *testing.T) {
	e := engine.New()
	reader, builder, consumer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, reader, "CREATE TABLE t(n INT)")
	enginetest.SessionSQL(t, reader, "CREATE TABLE u(n INT)")
	enginetest.SessionSQL(t, reader, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	rows := enginetest.SessionSQL(t, reader, "SELECT n FROM u")
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
	if err := engine.Dump(context.Background(), e, sink); err == nil || sink.calls != 0 {
		t.Fatal(err, sink)
	}
	concurrentUnitError(t, consumer, "INSERT INTO t VALUES(1)", "23505")
	// Reader owns u only: DROP's target AX must be free after builder cancellation.
	enginetest.SessionSQL(t, consumer, "DROP INDEX ui")
	enginetest.SessionSQL(t, consumer, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, reader, "ROLLBACK")
	enginetest.LockEmpty(t, e)
	e.Mu.Lock()
	remaining := len(e.Visibility.Entries)
	e.Mu.Unlock()
	if remaining != 0 {
		t.Fatal("canceled builder registry leak")
	}
}

func TestConcurrentIndexU6BeforeDescriptorAndWriterCancellation(t *testing.T) {
	for _, phase := range []string{"before_descriptor", "first_writer", "second_writer"} {
		t.Run(phase, func(t *testing.T) {
			e := engine.New()
			a, b, d := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE TABLE t(n INT)")
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
			enginetest.SessionSQL(t, a, "BEGIN ISOLATION LEVEL READ COMMITTED")
			if phase == "before_descriptor" {
				enginetest.SessionSQL(t, a, "LOCK TABLE t IN SHARE MODE")
			} else {
				enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
			}
			cancel, await := indexMaintenanceAsync(t, e, b, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)")
			if phase == "before_descriptor" {
				enginetest.LockPending(t, e, 1)
				ref := enginetest.RoleTableRef(t, e, "t")
				lockState := e.Locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
				exact := len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.ShareUpdateExclusive
				if !exact {
					t.Fatal("predescriptor SUE acquisition not actually pending")
				}
			} else {
				concurrentUnitCompletion(t, e, a.Transactions.Ticket, b)
				if phase == "second_writer" {
					enginetest.SessionSQL(t, d, "BEGIN ISOLATION LEVEL READ COMMITTED")
					enginetest.SessionSQL(t, d, "INSERT INTO t VALUES(3)")
					enginetest.SessionSQL(t, a, "COMMIT")
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
				enginetest.SessionSQL(t, a, "ROLLBACK")
			} else if phase == "first_writer" {
				enginetest.SessionSQL(t, a, "COMMIT")
				concurrentUnitIndex(t, e, "ui", false, false)
				enginetest.SessionSQL(t, d, "REINDEX INDEX ui")
				index, _ := concurrentUnitIndex(t, e, "ui", true, true)
				concurrentUnitRootMatchesStorage(t, e, index)
			} else {
				enginetest.SessionSQL(t, d, "COMMIT")
				concurrentUnitIndex(t, e, "ui", true, false)
				enginetest.SessionSQL(t, d, "REINDEX INDEX ui")
				index, _ := concurrentUnitIndex(t, e, "ui", true, true)
				concurrentUnitRootMatchesStorage(t, e, index)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestConcurrentIndexU6LateSuccessCannotBeRetracted(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(n INT)")
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
	result, err := scope.Execute(enginetest.Statement(t, "CREATE INDEX CONCURRENTLY ix ON t(n)"), 0, nil)
	if err != nil || result.Count != 0 || !scope.Completed {
		scope.Finish(err)
		t.Fatal(result, err)
	}
	cancel()
	if err := scope.Finish(nil); err != nil {
		t.Fatal("late cancellation retracted installed validity", err)
	}
	index, _ := concurrentUnitIndex(t, e, "ix", true, true)
	concurrentUnitRootMatchesStorage(t, e, index)
	enginetest.LockEmpty(t, e)
}

func TestConcurrentIndexU7OrdinaryUniqueUsesPublicParseAndExecute(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(n INT)")
	statements, err := parser.Parse("CREATE UNIQUE INDEX ui ON t(n) NULLS NOT DISTINCT")
	if err != nil {
		t.Fatal(err)
	}
	before := e.CatalogRead().State()
	op := enginetest.TransactionOperation(t, s)
	result, err := e.Execute(op, statements[0], 0, nil)
	op.Done()
	if err != nil || result.Count != 0 || e.CatalogRead().State() == before {
		t.Fatal("ordinary UNIQUE public execution", result, err)
	}
	index, _ := concurrentUnitIndex(t, e, "ui", true, true)
	if !index.SQLIndex.Unique || !index.SQLIndex.NullsNotDistinct || index.SQLIndex.Constraint != nil {
		t.Fatal("ordinary UNIQUE definition identity")
	}
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(NULL)")
	concurrentUnitError(t, s, "INSERT INTO t VALUES(NULL)", "23505")
	enginetest.LockEmpty(t, e)
}

func TestConcurrentIndexU7ReplacementRejectsInvalidSourceAndIsAtomic(t *testing.T) {
	source, target := engine.New(), engine.New()
	a, b := enginetest.StartSession(t, source, nil), enginetest.StartSession(t, target, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(n INT)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1),(1)")
	concurrentUnitError(t, a, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", "23505")
	enginetest.SessionSQL(t, b, "CREATE TABLE sentinel(n INT)")
	enginetest.SessionSQL(t, b, "INSERT INTO sentinel VALUES(9)")
	sourceState, targetState := source.CatalogRead().State(), target.CatalogRead().State()
	oldOID, oldRevision, oldRow, oldVersion := target.Catalog.NextOID(), target.Catalog.NextRevision(), target.Storage.RowFrontier(), target.Storage.VersionFrontier()
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err == nil {
		t.Fatal("invalid source was imported/healed")
	}
	if target.CatalogRead().State() != targetState || source.CatalogRead().State() != sourceState || target.Catalog.NextOID() != oldOID || target.Catalog.NextRevision() != oldRevision || target.Storage.RowFrontier() != oldRow || target.Storage.VersionFrontier() != oldVersion {
		t.Fatal("failed replacement changed source or destination authority")
	}
	for _, stream := range []string{
		"CREATE TABLE t(n INT); INSERT INTO t VALUES(1),(1); CREATE UNIQUE INDEX ui ON t(n);",
		"CREATE TABLE t(n INT); CREATE INDEX CONCURRENTLY ix ON t(n);",
		"CREATE TABLE t(n INT); INSERT INTO t VALUES(1); SELECT FROM",
	} {
		if err := engine.Restore(context.Background(), target, strings.NewReader(stream)); err == nil {
			t.Fatal("invalid isolated stream succeeded", stream)
		}
		if target.CatalogRead().State() != targetState || target.Catalog.NextOID() != oldOID || target.Catalog.NextRevision() != oldRevision {
			t.Fatal("isolated failed stream mutated destination", stream)
		}
	}
	enginetest.LockEmpty(t, source)
	enginetest.LockEmpty(t, target)
}

func TestConcurrentIndexU7RetainedInvalidTempIsNotImportedOrHealed(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	// TEMP table SQL is outside native admission. Reuse the legitimate
	// catalog-provider component seam, then exercise real SQL index phases.
	enginetest.SessionSQL(t, s, "CREATE TABLE temp_prototype(n INT)")
	prototype := e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, "temp_prototype").Address)
	op := enginetest.TransactionOperation(t, s)
	arrayComponentTemporary(t, e, op, prototype)
	op.Done()
	enginetest.SessionSQL(t, s, "ALTER TABLE array_component_temp RENAME TO retained_ci_temp")
	enginetest.SessionSQL(t, s, "INSERT INTO retained_ci_temp VALUES(1),(1)")
	concurrentUnitError(t, s, "CREATE UNIQUE INDEX CONCURRENTLY retained_ci_temp_index ON retained_ci_temp(n)", "23505")
	var original *catalog.Relation
	e.Mu.Lock()
	for _, descriptor := range e.Catalog.State.Descriptors() {
		if index, ok := descriptor.(*catalog.Relation); ok && index.Name() == "retained_ci_temp_index" {
			namespace := e.Catalog.State.MustNamespace(index.Namespace())
			if namespace.Temporary == s.SessionToken {
				original = index
			}
		}
	}
	e.Mu.Unlock()
	if original == nil || original.SQLIndex.Ready || original.SQLIndex.Valid {
		t.Fatal("real retained TEMP invalid descriptor missing")
	}
	if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE imported(n INT); INSERT INTO imported VALUES(7);")); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	retained := e.Catalog.State.Get(original.Address()) == original && e.Storage.IndexRoots[original.Address()] == nil
	e.Mu.Unlock()
	if !retained {
		t.Fatal("replacement healed or replaced retained not-ready TEMP descriptor")
	}
	enginetest.SessionSQL(t, s, "INSERT INTO retained_ci_temp VALUES(1)")
	enginetest.SessionSQL(t, s, "DROP INDEX retained_ci_temp_index")
	enginetest.LockEmpty(t, e)
}

func TestConcurrentIndexU1U6DetachedCandidateSourceAndFinalVeto(t *testing.T) {
	for _, mode := range []string{"cancel", "source_change"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE t(n INT)")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(1)")
			concurrentUnitError(t, s, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", "23505")
			enginetest.SessionSQL(t, s, "DELETE FROM t")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(2)")
			original, _ := concurrentUnitIndex(t, e, "ui", false, false)
			base := e.CatalogRead()
			e.Mu.Lock()
			parent := e.Catalog.State.MustRelation(original.SQLIndex.Parent)
			store := e.Storage.Relations[parent.Address()]
			snapshot := *store
			e.Mu.Unlock()
			next, definition := *original, *original.SQLIndex
			definition.Ready = true
			next.SQLIndex = &definition
			next.ObjectRevision++
			root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, parent, &definition, &snapshot, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := index.ValidateUniqueRoot(context.Background(), parent, &next, root, "public", sqltype.OutputEnvironment{}, &access.View{Catalog: base}, nil, true); err != nil {
				t.Fatal(err)
			}
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			stage.Indexes().Parents = map[ids.ObjectAddress]*catalog.Relation{parent.Address(): parent}
			stage.Indexes().Candidates = map[ids.ObjectAddress]index.RootCandidate{next.Address(): {Index: &next, Root: root, Storage: store, Generation: snapshot.Generation, Ends: snapshot.Ends, Length: len(snapshot.Versions)}}
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
				enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(3)")
			}
			_, err = enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: base.Epoch()})
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
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestConcurrentIndexU7OlderAdmissionEpochFence(t *testing.T) {
	e := engine.New()
	reader, builder := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, reader, "CREATE TABLE t(n INT)")
	enginetest.SessionSQL(t, reader, "CREATE TABLE u(n INT)")
	enginetest.SessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	rows := enginetest.SessionSQL(t, reader, "SELECT n FROM u")
	rows.Rows.Close()
	_, await := indexMaintenanceAsync(t, e, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)")
	concurrentUnitCompletion(t, e, reader.Transactions.Ticket, builder)
	_, _, _, waiting, writers, err := e.InspectIndex(context.Background(), "public", "ix")
	if err != nil || !waiting || writers {
		t.Fatal("actual older admission absent or mixed with writer admission", waiting, writers, err)
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	fenced := e.IndexWaits.CountLocked() == 0
	e.Mu.Unlock()
	if !fenced {
		t.Fatal("replacement retained an older-cohort observation")
	}
	if err := await(); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("old coordinator did not finish at the actual epoch fence", err)
	}
	enginetest.SessionSQL(t, reader, "ROLLBACK")
	enginetest.SessionSQL(t, builder, "CREATE TABLE t(n INT)")
	enginetest.SessionSQL(t, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)")
	ready, valid, live, waiting, writers, err := e.InspectIndex(context.Background(), "public", "ix")
	if err != nil || !ready || !valid || !live || waiting || writers {
		t.Fatal("replacement index inherited old admission", ready, valid, live, waiting, writers, err)
	}
	enginetest.LockEmpty(t, e)
}

// All waits are observed through the actual manager queue. Register cancellation
// and joining after session cleanup so workers finish before their sessions end.
func indexMaintenanceAsync(t *testing.T, e *engine.Engine, s *session.Session, q string) (context.CancelFunc, func() error) {
	t.Helper()
	stmt := enginetest.Statement(t, q)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	joined := false
	go func() {
		op, err := s.Acquire(ctx)
		if err == nil {
			result, executeErr := e.Execute(op, stmt, 0, nil)
			err = executeErr
			if result.Rows != nil {
				result.Rows.Close()
			}
			op.Done()
		}
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("index utility did not terminate during cleanup")
			}
		}
	})
	return cancel, func() error {
		t.Helper()
		select {
		case err := <-done:
			joined = true
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("index utility did not terminate")
			return nil
		}
	}
}

// These are valid catalog-provider component fixtures, not public TEMP-table SQL.
func arrayComponentTemporary(t *testing.T, e *engine.Engine, op *session.Operation, prototype *catalog.Relation) *catalog.Relation {
	t.Helper()
	e.Mu.Lock()
	nsOID, err := e.Catalog.AllocateOIDLocked()
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	tableOID, err := e.Catalog.AllocateOIDLocked()
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	revision, err := e.Catalog.RevisionLocked()
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: nsOID}, ObjectName: "pg_temp_array_component", ObjectRevision: revision, Owner: op.RoleState().CurrentUser, Temporary: op.Session.SessionToken}
	table := *prototype
	table.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: tableOID}
	table.ObjectNamespace = ns.Address()
	table.ObjectName = "array_component_temp"
	table.ObjectRevision = revision
	table.RowType = 0
	table.Columns = append([]catalog.Column(nil), prototype.Columns...)
	for i := range table.Columns {
		table.Columns[i].OriginTable = uint32(tableOID)
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Create(ns); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = e.StageRowTypeLocked(stage, &table)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(&table); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	e.Mu.Lock()
	op.Session.Mu.Lock()
	op.Session.TemporaryNamespace = ns.Address()
	op.Session.Mu.Unlock()
	e.Mu.Unlock()
	return &table
}

// Reduced storage fixtures still need real family members and genuine callable
// declarations. Share an immutable ordinary engine bootstrap, never a provider
// fallback or a test callback.
func operatorFamilyTestRead() catalog.Read { return operatorFamilyUnitSeed() }

func domainUnitPublish(t *testing.T, e *engine.Engine, stage *engine.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}

var operatorFamilyUnitSeed = sync.OnceValue(func() catalog.Read { return engine.New().CatalogRead() })

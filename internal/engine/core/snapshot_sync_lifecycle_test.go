package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// These are composed publication/ownership tests, supplementing the public
// reference scenarios. Every worker owns a separately acquired operation; only
// the incumbent owner -> Engine -> Session gates publish or retire authority.
// The two ordered schedules prove both sides of each cut independently of the
// scheduler; repeated simultaneous starts exercise the genuine interleaving.
func snapshotLifecycleSchedules(t *testing.T, run func(*testing.T, string)) {
	t.Helper()
	for _, order := range []string{"import-first", "transition-first", "concurrent"} {
		n := 1
		if order == "concurrent" {
			n = 16
		}
		for i := range n {
			t.Run(fmt.Sprintf("%s/%d", order, i), func(t *testing.T) { run(t, order) })
		}
	}
}

func snapshotLifecyclePair(order string, first, second func()) {
	start, firstDone, secondDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		defer close(firstDone)
		<-start
		if order == "transition-first" {
			<-secondDone
		}
		first()
	}()
	go func() {
		defer workers.Done()
		defer close(secondDone)
		<-start
		if order == "import-first" {
			<-firstDone
		}
		second()
	}()
	close(start)
	workers.Wait()
}

type snapshotLifecycleFixture struct {
	e                  *core.Engine
	source, importer   *session.Session
	keeper             *session.Session
	token              string
	ref                ids.TxRef
	roots              map[ids.ObjectAddress]visibility.Root
	snapshot           *sqltype.Snapshot
	frontier           ids.CommitSeq
	catalog            *catalog.State
	stableSnapshot     *sqltype.Snapshot
	detached           []core.Result
}

func snapshotLifecycleNew(t *testing.T, stableSource bool) *snapshotLifecycleFixture {
	t.Helper()
	f := &snapshotLifecycleFixture{e: engine.New()}
	peer := enginetest.StartSession(t, f.e, nil)
	f.source = enginetest.StartSession(t, f.e, nil)
	f.importer = enginetest.StartSession(t, f.e, nil)
	f.keeper = enginetest.StartSession(t, f.e, nil)
	for _, q := range []string{
		"CREATE TABLE sync_lifecycle(id INT PRIMARY KEY,v INT)",
		"INSERT INTO sync_lifecycle VALUES(1,10),(3,30)",
		"CREATE TABLE sync_lifecycle_unread(id INT,v INT)",
		"INSERT INTO sync_lifecycle_unread VALUES(1,10)",
		"CREATE TABLE sync_lifecycle_empty(id INT,v INT)",
	} {
		enginetest.SessionSQL(t, peer, q)
	}
	begin := "BEGIN"
	if stableSource {
		begin = "BEGIN ISOLATION LEVEL REPEATABLE READ"
	}
	enginetest.SessionSQL(t, f.source, begin)
	enginetest.SessionSQL(t, f.source, "INSERT INTO sync_lifecycle VALUES(9,90)")
	r := enginetest.SessionSQL(t, f.source, "SELECT pg_export_snapshot()")
	rows := enginetest.Tuples(r)
	if len(rows) != 1 || len(rows[0]) != 1 {
		t.Fatal("export lost its single text result", rows)
	}
	var ok bool
	f.token, ok = rows[0][0].(string)
	if !ok || f.token == "" {
		t.Fatal("export lost nonempty text identity", rows)
	}
	f.roots, f.snapshot, f.frontier = core.EngineExportCapture(f.e, f.token)
	f.source.Transactions.Mu.Lock()
	f.ref, f.stableSnapshot = f.source.Transactions.Ref, f.source.Transactions.CurrentSnapshot
	f.source.Transactions.Mu.Unlock()
	f.e.Mu.Lock()
	f.catalog = f.e.Catalog.State
	coherent := f.roots != nil && f.snapshot != nil
	f.e.Mu.Unlock()
	if !coherent {
		t.Fatal("initial real export lacks committed roots/public snapshot")
	}
	for _, name := range []string{"sync_lifecycle", "sync_lifecycle_unread", "sync_lifecycle_empty"} {
		a := enginetest.RoleTableRef(t, f.e, name).Address
		f.e.Mu.Lock()
		descriptor, _ := f.e.Catalog.State.Relation(a)
		published := f.e.Storage.Relations[a]
		f.e.Mu.Unlock()
		root, present := f.roots[a]
		if !present || root.Relation != descriptor || root.Storage != published || root.Storage == nil {
			t.Fatal("capture omitted committed descriptor/storage pair", name)
		}
		if name == "sync_lifecycle" {
			if len(root.Indexes) == 0 {
				t.Fatal("capture omitted supporting primary-key root")
			}
			for _, index := range root.Indexes {
				if index.Root == nil || index.Definition == nil {
					t.Fatal("half-captured supporting index")
				}
			}
		}
	}
	for _, s := range []*session.Session{f.importer, f.keeper} {
		enginetest.SessionSQL(t, s, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	}
	enginetest.SessionSQL(t, f.keeper, "SET TRANSACTION SNAPSHOT '"+f.token+"'")
	f.assertInstalled(t, f.keeper)
	f.assertRows(t, f.keeper)
	// Retain detached views as well: ordinary old-epoch execution must be
	// fenced by Reset/Restore, but already detached committed cuts survive.
	for _, q := range snapshotLifecycleQueries {
		f.detached = append(f.detached, enginetest.SessionSQL(t, f.keeper, q))
	}
	for _, q := range []string{
		"UPDATE sync_lifecycle SET v=20 WHERE id=1",
		"DELETE FROM sync_lifecycle WHERE id=3",
		"INSERT INTO sync_lifecycle VALUES(2,200)",
		"UPDATE sync_lifecycle_unread SET v=20",
		"INSERT INTO sync_lifecycle_empty VALUES(2,20)",
	} {
		enginetest.SessionSQL(t, peer, q)
	}
	return f
}

var snapshotLifecycleQueries = []string{
	"SELECT id,v FROM sync_lifecycle ORDER BY id",
	"SELECT id,v FROM sync_lifecycle_unread ORDER BY id",
	"SELECT id,v FROM sync_lifecycle_empty ORDER BY id",
}

var snapshotLifecycleRows = [][][]driver.Value{
	{{int64(1), int64(10)}, {int64(3), int64(30)}},
	{{int64(1), int64(10)}},
	{},
}

func (f *snapshotLifecycleFixture) assertInstalled(t *testing.T, s *session.Session) {
	t.Helper()
	o := s.Transactions
	o.Mu.Lock()
	complete := o.SnapshotSet && o.CurrentSnapshot == f.snapshot && o.Frontier == f.frontier &&
		o.SequenceCatalog == f.catalog && reflect.ValueOf(o.Roots).Pointer() == reflect.ValueOf(f.roots).Pointer()
	o.Mu.Unlock()
	if !complete {
		t.Fatal("import did not install the exact retained roots/catalog/public snapshot/frontier")
	}
}

func snapshotLifecycleAssertRejected(t *testing.T, s *session.Session) {
	t.Helper()
	o := s.Transactions
	o.Mu.Lock()
	clean := o.State == session.TxAborted && o.Roots == nil && o.SequenceCatalog == nil &&
		o.CurrentSnapshot == nil && o.Frontier == 0 && o.Writes == nil && o.Deletes == nil
	o.Mu.Unlock()
	if !clean {
		t.Fatal("failed lifecycle operation retained partial import or private authority")
	}
}

func (f *snapshotLifecycleFixture) assertRows(t *testing.T, s *session.Session) {
	t.Helper()
	for i, q := range snapshotLifecycleQueries {
		r := enginetest.SessionSQL(t, s, q)
		if got := enginetest.Tuples(r); !reflect.DeepEqual(got, snapshotLifecycleRows[i]) {
			t.Fatal("retained committed cut changed or acquired exporter-private effects", q, got)
		}
	}
}

func (f *snapshotLifecycleFixture) assertDetached(t *testing.T) {
	t.Helper()
	for i, r := range f.detached {
		if got := enginetest.Tuples(r); !reflect.DeepEqual(got, snapshotLifecycleRows[i]) {
			t.Fatal("native replacement mutated a detached retained cut", i, got)
		}
	}
}

func snapshotLifecycleAcquire(t *testing.T, s *session.Session) *session.Operation {
	t.Helper()
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	return op
}

// Worker-safe: parsing/admission and all testing.T failures stay on the test
// goroutine. Even unexpected result rows are released before the lease ends.
func snapshotLifecycleExecute(e *core.Engine, op *session.Operation, stmt parser.Statement) error {
	defer op.Done()
	r, err := e.Execute(op, stmt, 0, nil)
	if r.Rows != nil {
		if closeErr := r.Rows.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

func snapshotLifecycleDiagnostic(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	var got *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
		t.Fatalf("lifecycle diagnostic: got %#v (%v), want %#v", got, err, want)
	}
}

func (f *snapshotLifecycleFixture) assertFreshMissing(t *testing.T) {
	t.Helper()
	s := enginetest.StartSession(t, f.e, nil)
	enginetest.SessionSQL(t, s, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	op := snapshotLifecycleAcquire(t, s)
	err := snapshotLifecycleExecute(f.e, op, enginetest.Statement(t, "SET TRANSACTION SNAPSHOT '"+f.token+"'"))
	snapshotLifecycleDiagnostic(t, err, "42704", "snapshot \""+f.token+"\" does not exist", "")
	enginetest.SessionSQL(t, s, "ROLLBACK")
}

func (f *snapshotLifecycleFixture) assertNotAdvertised(t *testing.T) {
	t.Helper()
	f.e.Mu.Lock()
	_, advertised := f.e.Visibility.Entries[f.ref]
	f.e.Mu.Unlock()
	if advertised {
		t.Fatal("source transition left or resurrected live snapshot visibility")
	}
}

func TestSnapshotSyncAbortImportRace(t *testing.T) {
	snapshotLifecycleSchedules(t, func(t *testing.T, order string) {
		f := snapshotLifecycleNew(t, false)
		importOp, sourceOp := snapshotLifecycleAcquire(t, f.importer), snapshotLifecycleAcquire(t, f.source)
		importSQL := enginetest.Statement(t, "SET TRANSACTION SNAPSHOT '"+f.token+"'")
		failSQL := enginetest.Statement(t, "SELECT 1/0")
		var imported, failed error
		snapshotLifecyclePair(order,
			func() { imported = snapshotLifecycleExecute(f.e, importOp, importSQL) },
			func() { failed = snapshotLifecycleExecute(f.e, sourceOp, failSQL) })
		snapshotLifecycleDiagnostic(t, failed, "22012", "division by zero", "")
		snapshotLifecycleAssertRejected(t, f.source)
		if imported == nil {
			if order == "transition-first" {
				t.Fatal("post-abort import acquired withdrawn authority")
			}
			f.assertInstalled(t, f.importer)
			f.assertRows(t, f.importer)
		} else {
			if order == "import-first" {
				t.Fatal("live ordered import failed", imported)
			}
			snapshotLifecycleDiagnostic(t, imported, "55000", "could not import the requested snapshot",
				fmt.Sprintf("The source process with PID %d is not running anymore.", f.source.Token().ID()))
			snapshotLifecycleAssertRejected(t, f.importer)
		}
		if core.EngineExportCount(f.e) != 1 {
			t.Fatal("explicit abort deleted the unended source identifier")
		}
		roots, value, cut := core.EngineExportCapture(f.e, f.token)
		if roots != nil || value != nil || cut != f.frontier {
			t.Fatal("aborted tombstone retained data authority or lost its cut identity")
		}
		f.assertNotAdvertised(t)
		f.assertInstalled(t, f.keeper)
		f.assertRows(t, f.keeper)
		// A separate eligible import after the failure must be 55000, never
		// the 42704 belonging to the subsequent real block cleanup.
		probe := enginetest.StartSession(t, f.e, nil)
		enginetest.SessionSQL(t, probe, "BEGIN ISOLATION LEVEL REPEATABLE READ")
		err := snapshotLifecycleExecute(f.e, snapshotLifecycleAcquire(t, probe), importSQL)
		snapshotLifecycleDiagnostic(t, err, "55000", "could not import the requested snapshot",
			fmt.Sprintf("The source process with PID %d is not running anymore.", f.source.Token().ID()))
		enginetest.SessionSQL(t, probe, "ROLLBACK")
		enginetest.SessionSQL(t, f.source, "ROLLBACK")
		if core.EngineExportCount(f.e) != 0 {
			t.Fatal("real ROLLBACK leaked the aborted identifier")
		}
		f.assertFreshMissing(t)
		f.assertRows(t, f.keeper)
		if imported == nil {
			f.assertRows(t, f.importer)
		}
	})
}

func TestSnapshotSyncEndImportRace(t *testing.T) {
	for _, end := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(end, func(t *testing.T) {
			snapshotLifecycleSchedules(t, func(t *testing.T, order string) {
				f := snapshotLifecycleNew(t, false)
				importOp, sourceOp := snapshotLifecycleAcquire(t, f.importer), snapshotLifecycleAcquire(t, f.source)
				importSQL := enginetest.Statement(t, "SET TRANSACTION SNAPSHOT '"+f.token+"'")
				endSQL := enginetest.Statement(t, end)
				var imported, ended error
				snapshotLifecyclePair(order,
					func() { imported = snapshotLifecycleExecute(f.e, importOp, importSQL) },
					func() { ended = snapshotLifecycleExecute(f.e, sourceOp, endSQL) })
				if ended != nil {
					t.Fatal("real source end failed", ended)
				}
				if imported == nil {
					if order == "transition-first" {
						t.Fatal("ended source retained import authority")
					}
					f.assertInstalled(t, f.importer)
					f.assertRows(t, f.importer)
				} else {
					if order == "import-first" {
						t.Fatal("live ordered import failed", imported)
					}
					snapshotLifecycleDiagnostic(t, imported, "42704", "snapshot \""+f.token+"\" does not exist", "")
					snapshotLifecycleAssertRejected(t, f.importer)
				}
				if core.EngineExportCount(f.e) != 0 {
					t.Fatal("real source end leaked its token")
				}
				f.assertNotAdvertised(t)
				f.assertInstalled(t, f.keeper)
				f.assertRows(t, f.keeper)
				f.assertFreshMissing(t)
			})
		})
	}
}

func snapshotLifecycleReplace(ctx context.Context, e *core.Engine, kind string) error {
	if kind == "reset" {
		return engine.Reset(ctx, e)
	}
	return engine.Restore(ctx, e, strings.NewReader("CREATE TABLE sync_lifecycle_replacement(v INT);INSERT INTO sync_lifecycle_replacement VALUES(99);"))
}

func TestSnapshotSyncNativeEpochImportRace(t *testing.T) {
	for _, kind := range []string{"reset", "restore"} {
		t.Run(kind, func(t *testing.T) {
			snapshotLifecycleSchedules(t, func(t *testing.T, order string) {
				f := snapshotLifecycleNew(t, false)
				// BEGIN has already minted the importer's old TxRef. Acquire the
				// independent live lease before replacement, not a fresh tx after it.
				op := snapshotLifecycleAcquire(t, f.importer)
				stmt := enginetest.Statement(t, "SET TRANSACTION SNAPSHOT '"+f.token+"'")
				var imported, replaced error
				snapshotLifecyclePair(order,
					func() { imported = snapshotLifecycleExecute(f.e, op, stmt) },
					func() { replaced = snapshotLifecycleReplace(t.Context(), f.e, kind) })
				if replaced != nil {
					t.Fatal("actual native replacement failed", replaced)
				}
				if imported == nil {
					if order == "transition-first" {
						t.Fatal("old admitted importer crossed native epoch")
					}
					f.assertInstalled(t, f.importer)
				} else if order == "import-first" || !errors.Is(imported, ids.ErrCatalogReplaced) {
					t.Fatal("old incarnation did not retain a complete cut or return its native epoch fence", imported)
				}
				if imported != nil {
					snapshotLifecycleAssertRejected(t, f.importer)
				}
				f.assertNativeReplaced(t)
				f.assertFreshMissing(t)
				enginetest.SessionSQL(t, f.source, "ROLLBACK")
				enginetest.SessionSQL(t, f.importer, "ROLLBACK")
			})
		})
	}
}

func (f *snapshotLifecycleFixture) assertNativeReplaced(t *testing.T) {
	t.Helper()
	f.e.Mu.Lock()
	epoch := f.e.Catalog.State.Epoch()
	f.e.Mu.Unlock()
	if epoch != f.ref.Epoch()+1 || core.EngineExportCount(f.e) != 0 {
		t.Fatal("native replacement did not atomically advance epoch and empty registry")
	}
	f.assertNotAdvertised(t)
	f.assertDetached(t)
	// Detached views survive; ordinary execution by the old owner does not.
	err := snapshotLifecycleExecute(f.e, snapshotLifecycleAcquire(t, f.keeper), enginetest.Statement(t, "SELECT 1"))
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("old ordinary owner escaped native epoch fence", err)
	}
	enginetest.SessionSQL(t, f.keeper, "ROLLBACK")
	if core.EngineExportCount(f.e) != 0 {
		t.Fatal("fenced old execution resurrected a source token")
	}
}

func TestSnapshotSyncNativeEpochCaptureRace(t *testing.T) {
	for _, kind := range []string{"reset", "restore"} {
		t.Run(kind, func(t *testing.T) {
			snapshotLifecycleSchedules(t, func(t *testing.T, order string) {
				// The real stable exporter owns the saved cut before peer changes;
				// selected Execute must retain exactly that backing, not latest data.
				f := snapshotLifecycleNew(t, true)
				op := snapshotLifecycleAcquire(t, f.source)
				stmt := enginetest.Statement(t, "SELECT pg_export_snapshot()")
				scope, err := f.e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				var execution, replaced error
				var roots map[ids.ObjectAddress]visibility.Root
				var read *catalog.State
				var value *sqltype.Snapshot
				var cut ids.CommitSeq
				var token string
				var publicationObserved, publicationCoherent bool
				snapshotLifecyclePair(order, func() {
					defer op.Done()
					r, runErr := scope.Execute(stmt, 0, nil)
					roots, read, value, cut = core.ScopeExportCapture(scope)
					if runErr == nil {
						frame := make([]sqltype.Value, 1)
						present, rowErr := r.Rows.Next(op.Context(), frame)
						if rowErr != nil {
							runErr = rowErr
						} else if !present || frame[0].IsNull() {
							runErr = fmt.Errorf("successful exporting Execute lost its token")
						} else {
							token, runErr = frame[0].Text()
						}
						if token != "" {
							published, publicValue, publicCut := core.EngineExportCapture(f.e, token)
							publicationObserved = published != nil
							publicationCoherent = publicationObserved &&
								reflect.ValueOf(published).Pointer() == reflect.ValueOf(f.roots).Pointer() &&
								publicValue != nil && publicValue.String() == f.snapshot.String() && publicCut == f.frontier
						}
					}
					if r.Rows != nil {
						if closeErr := r.Rows.Close(); runErr == nil {
							runErr = closeErr
						}
					}
					execution = scope.Finish(runErr)
				}, func() { replaced = snapshotLifecycleReplace(t.Context(), f.e, kind) })
				if replaced != nil {
					t.Fatal("actual native replacement failed", replaced)
				}
				if publicationObserved && !publicationCoherent || order == "import-first" && !publicationObserved {
					t.Fatal("export publication did not retain the complete saved cut")
				}
				if execution == nil {
					if order == "transition-first" || token == "" || token == f.token ||
						reflect.ValueOf(roots).Pointer() != reflect.ValueOf(f.roots).Pointer() ||
						read != f.catalog || value != f.stableSnapshot || cut != f.frontier {
						t.Fatal("selected exporting Execute returned an incomplete or recaptured cut")
					}
				} else if order == "import-first" || !errors.Is(execution, ids.ErrCatalogReplaced) {
					t.Fatal("exporting Execute did not retain its saved cut or return the native epoch fence", execution)
				}
				// Even when a later stage fenced Execute, any already acquired
				// backing must be the complete immutable old pair, never half-roots.
				if roots != nil && (reflect.ValueOf(roots).Pointer() != reflect.ValueOf(f.roots).Pointer() ||
					read != f.catalog || value != f.stableSnapshot || cut != f.frontier) {
					t.Fatal("epoch race left a partial selected capture")
				}
				f.assertNativeReplaced(t)
				f.assertFreshMissing(t)
				enginetest.SessionSQL(t, f.source, "ROLLBACK")
			})
		})
	}
}

// External input blocks outside the target publication lock. Cancellation must
// discard the staged replacement, not fence the still-live exported snapshot.
type snapshotLifecycleGatedReader struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	source  io.Reader
}

func (r *snapshotLifecycleGatedReader) Read(p []byte) (int, error) {
	r.once.Do(func() {
		close(r.entered)
		<-r.release
	})
	return r.source.Read(p)
}

func TestSnapshotSyncRestoreInFlightCancellation(t *testing.T) {
	f := snapshotLifecycleNew(t, false)
	f.e.Mu.Lock()
	beforeCatalog, beforeFrontier := f.e.Catalog.State, f.e.Visibility.CommitSeq()
	f.e.Mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader := &snapshotLifecycleGatedReader{
		entered: make(chan struct{}), release: make(chan struct{}),
		source: strings.NewReader("CREATE TABLE sync_lifecycle_cancelled(v INT);INSERT INTO sync_lifecycle_cancelled VALUES(99);"),
	}
	done := make(chan error, 1)
	go func() { done <- engine.Restore(ctx, f.e, reader) }()
	select {
	case <-reader.entered:
		cancel()
		close(reader.release)
	case err := <-done:
		t.Fatal("Restore returned before reaching its external input gate", err)
	case <-t.Context().Done():
		cancel()
		close(reader.release)
		<-done
		t.Fatal("Restore did not reach its external input gate", t.Context().Err())
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("in-flight Restore did not preserve ordinary context cancellation", err)
	}
	f.e.Mu.Lock()
	epoch := f.e.Catalog.State.Epoch()
	unchanged := f.e.Catalog.State == beforeCatalog && f.e.Visibility.CommitSeq() == beforeFrontier
	f.e.Mu.Unlock()
	roots, value, cut := core.EngineExportCapture(f.e, f.token)
	if !unchanged || epoch != f.ref.Epoch() || core.EngineExportCount(f.e) != 1 ||
		reflect.ValueOf(roots).Pointer() != reflect.ValueOf(f.roots).Pointer() || value != f.snapshot || cut != f.frontier {
		t.Fatal("cancelled staged Restore changed epoch or retained export authority")
	}
	fresh := enginetest.StartSession(t, f.e, nil)
	enginetest.SessionSQL(t, fresh, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	enginetest.SessionSQL(t, fresh, "SET TRANSACTION SNAPSHOT '"+f.token+"'")
	f.assertInstalled(t, fresh)
	f.assertRows(t, fresh)
	enginetest.SessionSQL(t, f.importer, "SET TRANSACTION SNAPSHOT '"+f.token+"'")
	f.assertInstalled(t, f.importer)
	f.assertRows(t, f.importer)
	f.assertInstalled(t, f.keeper)
	f.assertRows(t, f.keeper)
	f.assertDetached(t)
	enginetest.SessionSQL(t, f.source, "ROLLBACK")
	f.assertFreshMissing(t)
	f.assertRows(t, f.importer)
}

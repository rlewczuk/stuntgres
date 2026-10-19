package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func transactionOperation(t *testing.T, s *Session) *Operation {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	return op
}
func transactionBegin(t *testing.T, s *Session) ids.TxRef {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	ref, err := s.Engine().Begin(op, TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
func transactionEnd(t *testing.T, s *Session, ref ids.TxRef, commit bool) EndResult {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	var r EndResult
	var err error
	if commit {
		r, err = s.Engine().Commit(op, ref)
	} else {
		r, err = s.Engine().Rollback(op, ref)
	}
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func transactionRows(t *testing.T, s *Session, q string, want ...int64) *finalRows {
	t.Helper()
	r := sessionSQL(t, s, q)
	t.Cleanup(func() {
		if err := r.Rows.Close(); err != nil {
			t.Error(err)
		}
	})
	got := tuples(r)
	expected := make([][]driver.Value, len(want))
	for i, v := range want {
		expected[i] = []driver.Value{v}
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("%s got %v want %v", q, got, expected)
	}
	return r.Rows.(*finalRows)
}
func TestTransactionVisibilityVersionsAndRetention(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	sessionSQL(t, b, "CREATE TABLE u(v INT)")
	ref := transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(1),(1)")
	sessionSQL(t, a, "INSERT INTO u VALUES(9)")
	own := transactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 1)
	empty := transactionRows(t, b, "SELECT v FROM t ORDER BY v")
	sessionSQL(t, b, "INSERT INTO t VALUES(7)")
	transactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 1, 7)
	transactionRows(t, b, "SELECT v FROM u ORDER BY v")
	if !transactionEnd(t, a, ref, true).Committed {
		t.Fatal("successful commit reported rollback")
	}
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 1, 7)
	transactionRows(t, b, "SELECT v FROM u ORDER BY v", 9)
	if own.count != 2 || empty.count != 0 {
		t.Fatal("retained result widened")
	}
	e.mu.Lock()
	var versions []rowVersion
	for _, store := range e.storage.relations {
		versions = append(versions, store.versions...)
	}
	e.mu.Unlock()
	seen := map[ids.RowID]bool{}
	for _, v := range versions {
		if v.row == 0 || v.version == 0 || v.born == 0 || seen[v.row] {
			t.Fatal("invalid/duplicate identity", v)
		}
		seen[v.row] = true
	}
	ref = transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(2)")
	private := transactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 1, 2, 7)
	transactionEnd(t, a, ref, false)
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 1, 7)
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if private.count != 4 || own.count != 2 {
		t.Fatal("end/replacement invalidated result")
	}
	private.Close()
	private.Close()
	own.Close()
	empty.Close()
}
func TestTransactionScopeFinishAndViewOwnership(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	ref := transactionBegin(t, a)
	op := transactionOperation(t, a)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Execute(statement(t, "INSERT INTO t VALUES(1)"), 0, nil); err != nil {
		t.Fatal(err)
	}
	view, err := scope.AcquireDataView(catalogRef(t, e, e.CatalogRead(), "t"))
	if err != nil {
		t.Fatal(err)
	}
	if len(view.read().own) != 0 {
		t.Fatal("same-command writes became visible")
	}
	retained := view.Retain()
	if retained == nil || retained.Tx() != ref || retained.Command() != 0 {
		t.Fatal("view authority")
	}
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if err = scope.Finish(errors.New("ignored")); err != nil {
		t.Fatal("Finish was not idempotent", err)
	}
	if view.Retain() != nil {
		t.Fatal("released view authorized retention")
	}
	if retained.Catalog().Epoch() == 0 {
		t.Fatal("one release invalidated retained handle")
	}
	retained.Release()
	retained.Release()
	if _, err = scope.Execute(statement(t, "INSERT INTO t VALUES(2)"), 0, nil); err == nil {
		t.Fatal("finished scope executed")
	}
	transactionRows(t, a, "SELECT v FROM t ORDER BY v", 1)
	transactionEnd(t, a, ref, true)
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1)
}
func TestTransactionFailureCancellationAndEpoch(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
			sessionSQL(t, b, "INSERT INTO t VALUES(7)")
			var ref ids.TxRef
			if explicit {
				ref = transactionBegin(t, a)
				sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			}
			ctx, cancel := context.WithCancel(context.Background())
			op, err := a.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = scope.Execute(statement(t, "INSERT INTO t VALUES(2),(3)"), 0, nil); err != nil {
				t.Fatal(err)
			}
			cancel()
			if err = scope.Finish(nil); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if !errors.Is(scope.Finish(nil), context.Canceled) {
				t.Fatal("lost finish error")
			}
			op.Done()
			if explicit {
				if a.TransactionState() != TxAborted {
					t.Fatal("not aborted")
				}
				op = transactionOperation(t, a)
				_, err = e.Execute(op, statement(t, "SELECT v FROM missing"), 0, nil)
				state(t, err, "25P02")
				op.Done()
				if transactionEnd(t, a, ref, true).Committed {
					t.Fatal("aborted commit")
				}
			} else if a.TransactionState() != TxIdle {
				t.Fatal("implicit not idle")
			}
			transactionRows(t, b, "SELECT v FROM t ORDER BY v", 7)
		})
	}
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	ref := transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(v INT); INSERT INTO t VALUES(99)")); err != nil {
		t.Fatal(err)
	}
	op := transactionOperation(t, a)
	_, err := e.Execute(op, statement(t, "SELECT v FROM t"), 0, nil)
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	op.Done()
	transactionEnd(t, a, ref, false)
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 99)
	ref = transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(2)")
	if err = e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	op = transactionOperation(t, a)
	_, err = e.Commit(op, ref)
	op.Done()
	if !errors.Is(err, ids.ErrCatalogReplaced) || a.TransactionState() != TxIdle {
		t.Fatal("stale commit", err)
	}
}
func TestTransactionReferencesCountersAndPrecancel(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	ref := transactionBegin(t, a)
	op := transactionOperation(t, a)
	defer op.Done()
	if _, err := e.Begin(op, TxOptions{}); err == nil {
		t.Fatal("nested begin")
	}
	for _, bad := range []ids.TxRef{{}, New().catalog.Domain.Tx(a.Token(), ref.ID(), 0, nil, 0), e.catalog.Domain.Tx(b.Token(), ref.ID(), 0, nil, 0)} {
		if _, err := e.Commit(op, bad); err == nil {
			t.Fatal("foreign ref")
		}
	}
	copyOp := *op
	for _, bad := range []*Operation{nil, &copyOp, transactionOperation(t, b)} {
		if _, err := e.Commit(bad, ref); err == nil {
			t.Fatal("foreign/copy operation")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if a.TransactionState() != TxActive {
		t.Fatal("pre-cancel mutated state")
	}
	transactionEnd(t, a, ref, false)
	next := transactionBegin(t, a)
	if next.ID() == 0 || next.ID() <= ref.ID() {
		t.Fatal("reused incarnation")
	}
	if _, err := e.Rollback(op, ref); err == nil {
		t.Fatal("old reference ended new work")
	}
	transactionEnd(t, a, next, false)
	done := transactionOperation(t, a)
	done.Done()
	next = transactionBegin(t, a)
	if _, err := e.Commit(done, next); err == nil || a.TransactionState() != TxActive {
		t.Fatal("done operation mutated state")
	}
	transactionEnd(t, a, next, false)
	if _, err := e.Begin(op, TxOptions{Isolation: Isolation(99)}); err == nil || a.TransactionState() != TxIdle {
		t.Fatal("option mutation")
	}
	a.transactions.next = ids.TxID(^uint64(0))
	if _, err := e.Begin(op, TxOptions{}); err == nil || a.TransactionState() != TxIdle {
		t.Fatal("identity wrapped")
	}
	a.transactions.next = next.ID()
	ref = transactionBegin(t, a)
	a.transactions.command = ids.CommandID(^uint64(0))
	_, err := e.Execute(op, statement(t, "INSERT INTO t VALUES(1)"), 0, nil)
	if err == nil {
		t.Fatal("command wrapped")
	}
	transactionEnd(t, a, ref, false)
	e.storage.nextRow = ^uint64(0)
	_, err = e.Execute(op, statement(t, "INSERT INTO t VALUES(1)"), 0, nil)
	if err == nil {
		t.Fatal("row identity wrapped")
	}
	e.storage.nextRow = 0
	ref = transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	e.commitSeq = ids.CommitSeq(^uint64(0))
	_, err = e.Commit(op, ref)
	if err == nil || a.TransactionState() != TxIdle {
		t.Fatal("commit frontier wrapped")
	}
	transactionRows(t, b, "SELECT v FROM t ORDER BY v")
}

// Context.Err is the existing cooperative preflight seam, not a production hook.
// Arm only after staging. Entry validation checks twice; third check is the final
// publication boundary while both Session.mu and Engine.mu are held.
type transactionBarrierContext struct {
	context.Context
	checks  atomic.Int32
	entered chan struct{}
	release chan struct{}
	late    context.CancelFunc
}

func (c *transactionBarrierContext) Err() error {
	if c.checks.Add(1) == 3 {
		close(c.entered)
		<-c.release
		if c.late != nil {
			c.late()
			return nil
		}
	}
	return c.Context.Err()
}
func TestTransactionPublicationLinearization(t *testing.T) {
	for _, boundary := range []string{"cancel_before", "cancel_after", "success", "terminal_before", "epoch_before", "counter_before", "ref_before"} {
		t.Run(boundary, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT)")
			sessionSQL(t, b, "CREATE TABLE u(v INT)")
			// Separate singleton commits select geometric spare capacity in t
			// and a full committed vector in u, without manufacturing storage.
			for _, query := range []string{"INSERT INTO t VALUES(7)", "INSERT INTO t VALUES(8)", "INSERT INTO t VALUES(10)", "INSERT INTO u VALUES(70)"} {
				sessionSQL(t, b, query)
			}
			retained := [2]*finalRows{
				transactionRows(t, b, "SELECT v FROM t ORDER BY v", 7, 8, 10),
				transactionRows(t, b, "SELECT v FROM u ORDER BY v", 70),
			}
			addresses := [2]ids.ObjectAddress{
				catalogRef(t, e, e.CatalogRead(), "t").Address,
				catalogRef(t, e, e.CatalogRead(), "u").Address,
			}
			stores := [2]*relationStorage{e.storage.relations[addresses[0]], e.storage.relations[addresses[1]]}
			roots := [2][]rowVersion{stores[0].versions, stores[1].versions}
			records := [2][]rowVersion{}
			for i, root := range roots {
				records[i] = append([]rowVersion(nil), root...)
				for j := range records[i] {
					records[i][j].values = append([]sqltype.Value(nil), root[j].values...)
				}
			}
			spare := roots[0][len(roots[0]):cap(roots[0])]
			suffix := append([]rowVersion(nil), spare...)
			frontier := e.commitSeq
			retainedFrontier, retainedEpoch := frontier, e.catalog.state.Epoch()
			retainedCatalog := e.catalog.state
			ref := transactionBegin(t, a)
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			sessionSQL(t, a, "INSERT INTO u VALUES(9)")
			staged := [2]rowVersion{a.transactions.writes[addresses[0]][0], a.transactions.writes[addresses[1]][0]}
			for i, root := range roots {
				size := len(root) + len(a.transactions.writes[addresses[i]])
				if len(root) != [2]int{3, 1}[i] || cap(root) != [2]int{4, 1}[i] || (size <= cap(root)) != (i == 0) {
					t.Fatalf("relation %d does not select mixed spare/growth: len=%d cap=%d staged=%d", i, len(root), cap(root), size-len(root))
				}
			}
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := a.Acquire(base)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			var outcome EndResult
			switch boundary {
			case "cancel_before", "cancel_after":
				barrier := &transactionBarrierContext{Context: op.Context(), entered: make(chan struct{}), release: make(chan struct{})}
				if boundary == "cancel_after" {
					barrier.late = cancel
				}
				op.ctx = barrier
				type commitResult struct {
					outcome EndResult
					err     error
				}
				result := make(chan commitResult, 1)
				go func() {
					r, err := e.Commit(op, ref)
					result <- commitResult{r, err}
				}()
				<-barrier.entered
				observed := make(chan [2]int, 1)
				started := make(chan struct{})
				go func() {
					close(started)
					e.mu.Lock()
					counts := [2]int{len(stores[0].versions), len(stores[1].versions)}
					e.mu.Unlock()
					observed <- counts
				}()
				<-started
				if boundary == "cancel_before" {
					cancel()
				}
				close(barrier.release)
				r := <-result
				outcome, err = r.outcome, r.err
				counts := <-observed
				want := [2]int{3, 1}
				if boundary == "cancel_after" {
					want = [2]int{4, 2}
				}
				if counts != want {
					t.Fatalf("observer saw partial publication: got %v want %v", counts, want)
				}
			case "terminal_before":
				a.RequestEnd(Terminated)
				// Keep the admitted lease and private batch alive. Exercise the
				// publication seam after terminal admission, so both buffers are
				// prepared and the final live-operation check rejects installation.
				err = e.publishWrites(op, ref, a.transactions.writes, a.transactions.deletes)
			case "epoch_before":
				// Isolate epoch rejection from replacement's own storage/frontier
				// effects; the mixed roots remain installed as the no-write witness.
				e.mu.Lock()
				next := *e.catalog.state
				next.SetEpoch(next.Epoch() + 1)
				e.catalog.state = &next
				e.mu.Unlock()
				outcome, err = e.Commit(op, ref)
			case "counter_before":
				e.mu.Lock()
				e.commitSeq = ids.CommitSeq(^uint64(0))
				frontier = e.commitSeq
				e.mu.Unlock()
				outcome, err = e.Commit(op, ref)
			case "ref_before":
				stale := ref
				stale = withTxID(stale, stale.ID()+1)
				outcome, err = e.Commit(op, stale)
			default:
				outcome, err = e.Commit(op, ref)
			}
			published := boundary == "success" || boundary == "cancel_after"
			if published {
				if err != nil || !outcome.Committed || e.commitSeq != frontier+1 {
					t.Fatal("complete publication must advance exactly one frontier", outcome, err, e.commitSeq, frontier)
				}
			} else {
				want := map[string]error{
					"cancel_before":   context.Canceled,
					"terminal_before": errors.New("stuntgres: inactive operation"),
					"epoch_before":    ids.ErrCatalogReplaced,
					"counter_before":  errors.New("stuntgres: commit sequence exhausted"),
					"ref_before":      errors.New("stuntgres: stale transaction reference"),
				}[boundary]
				if err == nil || (!errors.Is(err, want) && err.Error() != want.Error()) || outcome.Committed || e.commitSeq != frontier {
					t.Fatal("rejection changed committed frontier or outcome", outcome, err, e.commitSeq, frontier)
				}
				if !reflect.DeepEqual(spare, suffix) {
					t.Fatal("rejected publication wrote unpublished spare suffix")
				}
			}
			for i, root := range roots {
				current := stores[i].versions
				wantLen := len(root)
				if published {
					wantLen++
				}
				if e.storage.relations[addresses[i]] != stores[i] || len(current) != wantLen {
					t.Fatalf("relation %d storage/visible length changed unexpectedly", i)
				}
				sameRoot := &current[0] == &root[0]
				if sameRoot != (!published || i == 0) {
					t.Fatalf("relation %d used wrong spare/growth root", i)
				}
				data := retained[i].source.view.read()
				if data.frontier != retainedFrontier || data.catalog.State() != retainedCatalog || data.catalog.Epoch() != retainedEpoch {
					t.Fatal("retained frontier/catalog metadata changed")
				}
				if retained[i].count != len(root) {
					t.Fatal("retained result length changed")
				}
				if len(data.committed) != 1 || !reflect.DeepEqual(data.committed[addresses[i]], records[i]) || !reflect.DeepEqual(current[:len(root)], records[i]) {
					t.Fatal("retained or live prefix identities/birth/payload changed")
				}
				if published {
					row := current[len(root)]
					if row.row == 0 || row.version == 0 || row.row != staged[i].row || row.version != staged[i].version || row.born != frontier+1 || !reflect.DeepEqual(row.values, staged[i].values) || len(row.values) != 1 || testExport(row.values[0]) != [2]int64{1, 9}[i] {
						t.Fatal("new row lacks common publication frontier/payload", row)
					}
					if len(a.transactions.writes[addresses[i]]) != 0 {
						t.Fatal("successful commit retained private writes")
					}
				}
			}
			if boundary == "ref_before" {
				if a.TransactionState() != TxActive || len(a.transactions.writes) != 2 {
					t.Fatal("invalid reference discarded current incarnation")
				}
				transactionEnd(t, a, ref, false)
			}
			wantT, wantU := []int64{7, 8, 10}, []int64{70}
			if published {
				wantT, wantU = []int64{1, 7, 8, 10}, []int64{9, 70}
			}
			transactionRows(t, b, "SELECT v FROM t ORDER BY v", wantT...)
			transactionRows(t, b, "SELECT v FROM u ORDER BY v", wantU...)
			op.Done()
			if boundary == "terminal_before" {
				if err := a.WaitEnd(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestTransactionTerminalCleanupAndEndFence(t *testing.T) {
	for _, reason := range []EndReason{CleanClose, Terminated, Abandoned} {
		t.Run(map[EndReason]string{CleanClose: "close", Terminated: "terminate", Abandoned: "abandon"}[reason], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT)")
			sessionSQL(t, b, "INSERT INTO t VALUES(7)")
			sessionSQL(t, b, "SET application_name TO peer")
			transactionBegin(t, a)
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			op := transactionOperation(t, a)
			a.RequestEnd(reason)
			<-op.Context().Done()
			if len(a.transactions.writes) == 0 {
				t.Fatal("cleanup before drain")
			}
			op.Done()
			if err := a.WaitEnd(context.Background()); err != nil {
				t.Fatal(err)
			}
			if a.TransactionState() != TxIdle {
				t.Fatal("owned writes survived physical end")
			}
			transactionRows(t, b, "SELECT v FROM t ORDER BY v", 7)
			sessionValue(t, b, "peer")
		})
	}
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	ref := transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	op := transactionOperation(t, a)
	a.RequestEnd(Terminated)
	_, err := e.Commit(op, ref)
	if err == nil {
		t.Fatal("terminal end published")
	}
	op.Done()
	_ = a.WaitEnd(context.Background())
	transactionRows(t, b, "SELECT v FROM t ORDER BY v")
}
func TestTransactionConcurrentPeerRetention(t *testing.T) {
	e := New()
	setup := sessionStart(t, e, nil)
	sessionSQL(t, setup, "CREATE TABLE t(v INT)")
	var wg sync.WaitGroup
	for i := range 8 {
		s := sessionStart(t, e, nil)
		wg.Add(1)
		go func(v int64) {
			defer wg.Done()
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			defer op.Done()
			ref, err := e.Begin(op, TxOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			_, err = testExecuteFrame(e, op, statement(t, "INSERT INTO t VALUES($1)"), []driver.NamedValue{{Ordinal: 1, Value: v}})
			if err == nil {
				_, err = e.Commit(op, ref)
			}
			if err != nil {
				t.Error(err)
			}
		}(int64(i))
	}
	wg.Wait()
	transactionRows(t, setup, "SELECT v FROM t ORDER BY v", 0, 1, 2, 3, 4, 5, 6, 7)
}

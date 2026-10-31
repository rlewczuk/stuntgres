package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestTransactionVisibilityVersionsAndRetention(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, b, "CREATE TABLE u(v INT)")
	ref := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1),(1)")
	enginetest.SessionSQL(t, a, "INSERT INTO u VALUES(9)")
	own := enginetest.TransactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 1)
	empty := enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(7)")
	enginetest.TransactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 1, 7)
	enginetest.TransactionRows(t, b, "SELECT v FROM u ORDER BY v")
	if !enginetest.TransactionEnd(t, a, ref, true).Committed {
		t.Fatal("successful commit reported rollback")
	}
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 1, 7)
	enginetest.TransactionRows(t, b, "SELECT v FROM u ORDER BY v", 9)
	if own.Count != 2 || empty.Count != 0 {
		t.Fatal("retained result widened")
	}
	e.Mu.Lock()
	var versions []storage.RowVersion
	for _, store := range e.Storage.Relations {
		versions = append(versions, store.Versions...)
	}
	e.Mu.Unlock()
	seen := map[ids.RowID]bool{}
	for _, v := range versions {
		if v.Row == 0 || v.Version == 0 || v.Born == 0 || seen[v.Row] {
			t.Fatal("invalid/duplicate identity", v)
		}
		seen[v.Row] = true
	}
	ref = enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
	private := enginetest.TransactionRows(t, a, "SELECT v FROM t ORDER BY v", 1, 1, 2, 7)
	enginetest.TransactionEnd(t, a, ref, false)
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 1, 7)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if private.Count != 4 || own.Count != 2 {
		t.Fatal("end/replacement invalidated result")
	}
	private.Close()
	private.Close()
	own.Close()
	empty.Close()
}
func TestTransactionScopeFinishAndViewOwnership(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	ref := enginetest.TransactionBegin(t, a)
	op := enginetest.TransactionOperation(t, a)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Execute(enginetest.Statement(t, "INSERT INTO t VALUES(1)"), 0, nil); err != nil {
		t.Fatal(err)
	}
	view, err := scope.AcquireDataView(enginetest.CatalogRef(t, e, e.CatalogRead(), "t"))
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Data().Own) != 0 {
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
	if _, err = scope.Execute(enginetest.Statement(t, "INSERT INTO t VALUES(2)"), 0, nil); err == nil {
		t.Fatal("finished scope executed")
	}
	enginetest.TransactionRows(t, a, "SELECT v FROM t ORDER BY v", 1)
	enginetest.TransactionEnd(t, a, ref, true)
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 1)
}
func TestTransactionFailureCancellationAndEpoch(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
			enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(7)")
			var ref ids.TxRef
			if explicit {
				ref = enginetest.TransactionBegin(t, a)
				enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
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
			if _, err = scope.Execute(enginetest.Statement(t, "INSERT INTO t VALUES(2),(3)"), 0, nil); err != nil {
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
				if a.TransactionState() != session.TxAborted {
					t.Fatal("not aborted")
				}
				op = enginetest.TransactionOperation(t, a)
				_, err = e.Execute(op, enginetest.Statement(t, "SELECT v FROM missing"), 0, nil)
				enginetest.State(t, err, "25P02")
				op.Done()
				if enginetest.TransactionEnd(t, a, ref, true).Committed {
					t.Fatal("aborted commit")
				}
			} else if a.TransactionState() != session.TxIdle {
				t.Fatal("implicit not idle")
			}
			enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 7)
		})
	}
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	ref := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE t(v INT); INSERT INTO t VALUES(99)")); err != nil {
		t.Fatal(err)
	}
	op := enginetest.TransactionOperation(t, a)
	_, err := e.Execute(op, enginetest.Statement(t, "SELECT v FROM t"), 0, nil)
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(err)
	}
	op.Done()
	enginetest.TransactionEnd(t, a, ref, false)
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 99)
	ref = enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
	if err = engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	op = enginetest.TransactionOperation(t, a)
	_, err = e.Commit(op, ref)
	op.Done()
	if !errors.Is(err, ids.ErrCatalogReplaced) || a.TransactionState() != session.TxIdle {
		t.Fatal("stale commit", err)
	}
}
func TestTransactionReferencesCountersAndPrecancel(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	ref := enginetest.TransactionBegin(t, a)
	op := enginetest.TransactionOperation(t, a)
	defer op.Done()
	if _, err := e.Begin(op, session.TxOptions{}); err == nil {
		t.Fatal("nested begin")
	}
	for _, bad := range []ids.TxRef{{}, engine.New().Catalog.Domain.Tx(a.Token(), ref.ID(), 0, nil, 0), e.Catalog.Domain.Tx(b.Token(), ref.ID(), 0, nil, 0)} {
		if _, err := e.Commit(op, bad); err == nil {
			t.Fatal("foreign ref")
		}
	}
	copyOp := *op
	for _, bad := range []*session.Operation{nil, &copyOp, enginetest.TransactionOperation(t, b)} {
		if _, err := e.Commit(bad, ref); err == nil {
			t.Fatal("foreign/copy operation")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if a.TransactionState() != session.TxActive {
		t.Fatal("pre-cancel mutated state")
	}
	enginetest.TransactionEnd(t, a, ref, false)
	next := enginetest.TransactionBegin(t, a)
	if next.ID() == 0 || next.ID() <= ref.ID() {
		t.Fatal("reused incarnation")
	}
	if _, err := e.Rollback(op, ref); err == nil {
		t.Fatal("old reference ended new work")
	}
	enginetest.TransactionEnd(t, a, next, false)
	done := enginetest.TransactionOperation(t, a)
	done.Done()
	next = enginetest.TransactionBegin(t, a)
	if _, err := e.Commit(done, next); err == nil || a.TransactionState() != session.TxActive {
		t.Fatal("done operation mutated state")
	}
	enginetest.TransactionEnd(t, a, next, false)
	if _, err := e.Begin(op, session.TxOptions{Isolation: session.Isolation(99)}); err == nil || a.TransactionState() != session.TxIdle {
		t.Fatal("option mutation")
	}
	a.Transactions.Next = ids.TxID(^uint64(0))
	if _, err := e.Begin(op, session.TxOptions{}); err == nil || a.TransactionState() != session.TxIdle {
		t.Fatal("identity wrapped")
	}
	a.Transactions.Next = next.ID()
	ref = enginetest.TransactionBegin(t, a)
	a.Transactions.Command = ids.CommandID(^uint64(0))
	_, err := e.Execute(op, enginetest.Statement(t, "INSERT INTO t VALUES(1)"), 0, nil)
	if err == nil {
		t.Fatal("command wrapped")
	}
	enginetest.TransactionEnd(t, a, ref, false)
	e.Storage.SetFrontierLocked(^uint64(0), e.Storage.VersionFrontier())
	_, err = e.Execute(op, enginetest.Statement(t, "INSERT INTO t VALUES(1)"), 0, nil)
	if err == nil {
		t.Fatal("row identity wrapped")
	}
	e.Storage.SetFrontierLocked(0, e.Storage.VersionFrontier())
	ref = enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	e.Visibility.SetCommitSeqLocked(ids.CommitSeq(^uint64(0)))
	_, err = e.Commit(op, ref)
	if err == nil || a.TransactionState() != session.TxIdle {
		t.Fatal("commit frontier wrapped")
	}
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v")
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
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, b, "CREATE TABLE u(v INT)")
			// Separate singleton commits select geometric spare capacity in t
			// and a full committed vector in u, without manufacturing storage.
			for _, query := range []string{"INSERT INTO t VALUES(7)", "INSERT INTO t VALUES(8)", "INSERT INTO t VALUES(10)", "INSERT INTO u VALUES(70)"} {
				enginetest.SessionSQL(t, b, query)
			}
			retained := [2]*exec.FinalRows{
				enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 7, 8, 10),
				enginetest.TransactionRows(t, b, "SELECT v FROM u ORDER BY v", 70),
			}
			addresses := [2]ids.ObjectAddress{
				enginetest.CatalogRef(t, e, e.CatalogRead(), "t").Address,
				enginetest.CatalogRef(t, e, e.CatalogRead(), "u").Address,
			}
			stores := [2]*storage.Relation{e.Storage.Relations[addresses[0]], e.Storage.Relations[addresses[1]]}
			roots := [2][]storage.RowVersion{stores[0].Versions, stores[1].Versions}
			records := [2][]storage.RowVersion{}
			for i, root := range roots {
				records[i] = append([]storage.RowVersion(nil), root...)
				for j := range records[i] {
					records[i][j].Values = append([]sqltype.Value(nil), root[j].Values...)
				}
			}
			spare := roots[0][len(roots[0]):cap(roots[0])]
			suffix := append([]storage.RowVersion(nil), spare...)
			frontier := e.Visibility.CommitSeq()
			retainedFrontier, retainedEpoch := frontier, e.Catalog.State.Epoch()
			retainedCatalog := e.Catalog.State
			ref := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
			enginetest.SessionSQL(t, a, "INSERT INTO u VALUES(9)")
			staged := [2]storage.RowVersion{a.Transactions.Writes[addresses[0]][0], a.Transactions.Writes[addresses[1]][0]}
			for i, root := range roots {
				size := len(root) + len(a.Transactions.Writes[addresses[i]])
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
			var outcome core.EndResult
			switch boundary {
			case "cancel_before", "cancel_after":
				barrier := &transactionBarrierContext{Context: op.Context(), entered: make(chan struct{}), release: make(chan struct{})}
				if boundary == "cancel_after" {
					barrier.late = cancel
				}
				op.Ctx = barrier
				type commitResult struct {
					outcome core.EndResult
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
					e.Mu.Lock()
					counts := [2]int{len(stores[0].Versions), len(stores[1].Versions)}
					e.Mu.Unlock()
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
				a.RequestEnd(session.Terminated)
				// Keep the admitted lease and private batch alive. Exercise the
				// publication seam after terminal admission, so both buffers are
				// prepared and the final live-operation check rejects installation.
				err = core.EnginePublishWrites(e, op, ref, a.Transactions.Writes, a.Transactions.Deletes, nil)
			case "epoch_before":
				// Isolate epoch rejection from replacement's own storage/frontier
				// effects; the mixed roots remain installed as the no-write witness.
				e.Mu.Lock()
				next := *e.Catalog.State
				next.SetEpoch(next.Epoch() + 1)
				e.Catalog.PublishLocked(&next)
				e.Mu.Unlock()
				outcome, err = e.Commit(op, ref)
			case "counter_before":
				e.Mu.Lock()
				e.Visibility.SetCommitSeqLocked(ids.CommitSeq(^uint64(0)))
				frontier = e.Visibility.CommitSeq()
				e.Mu.Unlock()
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
				if err != nil || !outcome.Committed || e.Visibility.CommitSeq() != frontier+1 {
					t.Fatal("complete publication must advance exactly one frontier", outcome, err, e.Visibility.CommitSeq(), frontier)
				}
			} else {
				want := map[string]error{
					"cancel_before":   context.Canceled,
					"terminal_before": errors.New("stuntgres: inactive operation"),
					"epoch_before":    ids.ErrCatalogReplaced,
					"counter_before":  errors.New("stuntgres: commit sequence exhausted"),
					"ref_before":      errors.New("stuntgres: stale transaction reference"),
				}[boundary]
				if err == nil || (!errors.Is(err, want) && err.Error() != want.Error()) || outcome.Committed || e.Visibility.CommitSeq() != frontier {
					t.Fatal("rejection changed committed frontier or outcome", outcome, err, e.Visibility.CommitSeq(), frontier)
				}
				if !reflect.DeepEqual(spare, suffix) {
					t.Fatal("rejected publication wrote unpublished spare suffix")
				}
			}
			for i, root := range roots {
				current := stores[i].Versions
				wantLen := len(root)
				if published {
					wantLen++
				}
				if e.Storage.Relations[addresses[i]] != stores[i] || len(current) != wantLen {
					t.Fatalf("relation %d storage/visible length changed unexpectedly", i)
				}
				sameRoot := &current[0] == &root[0]
				if sameRoot != (!published || i == 0) {
					t.Fatalf("relation %d used wrong spare/growth root", i)
				}
				data := retained[i].Source.View.Data()
				if data.Frontier != retainedFrontier || data.Catalog.State() != retainedCatalog || data.Catalog.Epoch() != retainedEpoch {
					t.Fatal("retained frontier/catalog metadata changed")
				}
				if retained[i].Count != len(root) {
					t.Fatal("retained result length changed")
				}
				if len(data.Committed) != 1 || !reflect.DeepEqual(data.Committed[addresses[i]], records[i]) || !reflect.DeepEqual(current[:len(root)], records[i]) {
					t.Fatal("retained or live prefix identities/birth/payload changed")
				}
				if published {
					row := current[len(root)]
					if row.Row == 0 || row.Version == 0 || row.Row != staged[i].Row || row.Version != staged[i].Version || row.Born != frontier+1 || !reflect.DeepEqual(row.Values, staged[i].Values) || len(row.Values) != 1 || enginetest.Export(row.Values[0]) != [2]int64{1, 9}[i] {
						t.Fatal("new row lacks common publication frontier/payload", row)
					}
					if len(a.Transactions.Writes[addresses[i]]) != 0 {
						t.Fatal("successful commit retained private writes")
					}
				}
			}
			if boundary == "ref_before" {
				if a.TransactionState() != session.TxActive || len(a.Transactions.Writes) != 2 {
					t.Fatal("invalid reference discarded current incarnation")
				}
				enginetest.TransactionEnd(t, a, ref, false)
			}
			wantT, wantU := []int64{7, 8, 10}, []int64{70}
			if published {
				wantT, wantU = []int64{1, 7, 8, 10}, []int64{9, 70}
			}
			enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", wantT...)
			enginetest.TransactionRows(t, b, "SELECT v FROM u ORDER BY v", wantU...)
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
	for _, reason := range []session.EndReason{session.CleanClose, session.Terminated, session.Abandoned} {
		t.Run(map[session.EndReason]string{session.CleanClose: "close", session.Terminated: "terminate", session.Abandoned: "abandon"}[reason], func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(7)")
			enginetest.SessionSQL(t, b, "SET application_name TO peer")
			enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
			op := enginetest.TransactionOperation(t, a)
			a.RequestEnd(reason)
			<-op.Context().Done()
			if len(a.Transactions.Writes) == 0 {
				t.Fatal("cleanup before drain")
			}
			op.Done()
			if err := a.WaitEnd(context.Background()); err != nil {
				t.Fatal(err)
			}
			if a.TransactionState() != session.TxIdle {
				t.Fatal("owned writes survived physical end")
			}
			enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 7)
			sessionValue(t, b, "peer")
		})
	}
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	ref := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	op := enginetest.TransactionOperation(t, a)
	a.RequestEnd(session.Terminated)
	_, err := e.Commit(op, ref)
	if err == nil {
		t.Fatal("terminal end published")
	}
	op.Done()
	_ = a.WaitEnd(context.Background())
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v")
}
func TestTransactionConcurrentPeerRetention(t *testing.T) {
	e := engine.New()
	setup := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, setup, "CREATE TABLE t(v INT)")
	var wg sync.WaitGroup
	for i := range 8 {
		s := enginetest.StartSession(t, e, nil)
		wg.Add(1)
		go func(v int64) {
			defer wg.Done()
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			defer op.Done()
			ref, err := e.Begin(op, session.TxOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			_, err = enginetest.ExecuteFrame(e, op, enginetest.Statement(t, "INSERT INTO t VALUES($1)"), []driver.NamedValue{{Ordinal: 1, Value: v}})
			if err == nil {
				_, err = e.Commit(op, ref)
			}
			if err != nil {
				t.Error(err)
			}
		}(int64(i))
	}
	wg.Wait()
	enginetest.TransactionRows(t, setup, "SELECT v FROM t ORDER BY v", 0, 1, 2, 3, 4, 5, 6, 7)
}

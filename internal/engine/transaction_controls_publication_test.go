package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestControlTransactionsStaleCommitEpochPrecedence(t *testing.T) {
	for _, end := range []string{"COMMIT", "COMMIT AND CHAIN"} {
		t.Run(end, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT NOT NULL)")
			sessionSQL(t, b, "INSERT INTO t VALUES(77)")
			controlsRequire(t, a, "BEGIN")
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(v INT NOT NULL); INSERT INTO t VALUES(99);")); err != nil {
				t.Fatal(err)
			}
			op, err := a.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			_, err = scope.Execute(statement(t, "SELECT v FROM t"), 0, nil)
			if !errors.Is(scope.Finish(err), ids.ErrCatalogReplaced) {
				t.Fatal("stale ordinary statement", err)
			}
			op.Done()
			if a.TransactionState() != TxAborted || a.transactions.writes != nil {
				t.Fatal("stale statement did not discard private work")
			}
			op, err = a.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err = e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("test cleanup"))
			_, err = scope.Execute(statement(t, end), 0, nil)
			if !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("stale aborted commit must reject publication", err)
			}
			if !errors.Is(scope.Finish(err), ids.ErrCatalogReplaced) {
				t.Fatal("lost replacement error")
			}
			if a.TransactionState() != TxIdle || a.transactions.ticket != nil || a.transactions.ref != (ids.TxRef{}) {
				t.Fatal("failed end installed chain or retained old owner")
			}
			transactionRows(t, b, "SELECT v FROM t", 99)
		})
	}
}

func TestTransactionControlsPublicationBoundary(t *testing.T) {
	for _, boundary := range []string{"success", "cancel_before", "cancel_after", "terminal_before", "epoch_before", "counter_before", "ref_before"} {
		t.Run(boundary, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT)")
			sessionSQL(t, b, "CREATE TABLE u(v INT)")
			for _, q := range []string{"INSERT INTO t VALUES(7)", "INSERT INTO t VALUES(8)", "INSERT INTO t VALUES(10)", "INSERT INTO u VALUES(70)"} {
				sessionSQL(t, b, q)
			}
			addresses := [2]ids.ObjectAddress{catalogRef(t, e, e.CatalogRead(), "t").Address, catalogRef(t, e, e.CatalogRead(), "u").Address}
			stores := [2]*storage.Relation{e.storage.Relations[addresses[0]], e.storage.Relations[addresses[1]]}
			roots := [2][]storage.RowVersion{stores[0].Versions, stores[1].Versions}
			if len(roots[0]) != 3 || cap(roots[0]) != 4 || len(roots[1]) != 1 || cap(roots[1]) != 1 {
				t.Fatal("mixed spare/growth witness not selected")
			}
			prefixes := [2][]storage.RowVersion{append([]storage.RowVersion(nil), roots[0]...), append([]storage.RowVersion(nil), roots[1]...)}
			for i, root := range roots {
				for j := range prefixes[i] {
					prefixes[i][j].Values = append([]sqltype.Value(nil), root[j].Values...)
				}
			}
			retained := [2]*finalRows{transactionRows(t, b, "SELECT v FROM t ORDER BY v", 7, 8, 10), transactionRows(t, b, "SELECT v FROM u ORDER BY v", 70)}
			retainedCatalog := e.catalog.state
			retainedEpoch := e.catalog.state.Epoch()
			retainedFrontier := e.visibility.CommitSeq()
			spare := append([]storage.RowVersion(nil), roots[0][3:4]...)
			frontier := e.visibility.CommitSeq()
			controlsRequire(t, a, "BEGIN")
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			sessionSQL(t, a, "INSERT INTO u VALUES(9)")
			staged := [2]storage.RowVersion{a.transactions.writes[addresses[0]][0], a.transactions.writes[addresses[1]][0]}
			old := a.transactions.ref
			oldTicket := a.transactions.ticket
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := a.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			query := statement(t, "COMMIT AND CHAIN")
			switch boundary {
			case "cancel_before", "cancel_after":
				barrier := &transactionBarrierContext{Context: op.Context(), entered: make(chan struct{}), release: make(chan struct{})}
				if boundary == "cancel_after" {
					barrier.late = cancel
				}
				op.ctx = barrier
				// Classification checks exact authority before prospective birth;
				// arm the shared barrier at the final publisher, not reservation.
				barrier.checks.Store(-1)
				done := make(chan error, 1)
				go func() { _, err := scope.Execute(query, 0, nil); done <- err }()
				<-barrier.entered
				observed := make(chan [2]int, 1)
				go func() {
					e.mu.Lock()
					n := [2]int{len(stores[0].Versions), len(stores[1].Versions)}
					e.mu.Unlock()
					observed <- n
				}()
				if boundary == "cancel_before" {
					cancel()
				}
				close(barrier.release)
				err = <-done
				want := [2]int{3, 1}
				if boundary == "cancel_after" {
					want = [2]int{4, 2}
				}
				if n := <-observed; n != want {
					t.Fatal("partial mixed publication", n, want)
				}
			case "terminal_before":
				a.RequestEnd(Terminated)
				err = e.publishWrites(op, old, a.transactions.writes, a.transactions.deletes, nil)
			case "epoch_before":
				e.mu.Lock()
				next := *e.catalog.state
				next.SetEpoch(next.Epoch() + 1)
				e.catalog.state = &next
				e.mu.Unlock()
				_, err = scope.Execute(query, 0, nil)
			case "counter_before":
				e.mu.Lock()
				e.visibility.SetCommitSeqLocked(ids.CommitSeq(^uint64(0)))
				frontier = e.visibility.CommitSeq()
				e.mu.Unlock()
				_, err = scope.Execute(query, 0, nil)
			case "ref_before":
				bad := old
				bad = withTxID(bad, bad.ID()+1)
				_, err = e.endAdmitted(op, a.transactions, bad, true, true, true, scope.statementStart, scope)
			default:
				_, err = scope.Execute(query, 0, nil)
			}
			success := boundary == "success" || boundary == "cancel_after"
			if success {
				if err != nil {
					t.Fatal(err)
				}
				if err = scope.Finish(nil); err != nil {
					t.Fatal("late completion retracted publication", err)
				}
				next := a.transactions.ref
				ticket := a.transactions.ticket
				transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 7, 8, 10)
				transactionRows(t, b, "SELECT v FROM u ORDER BY v", 9, 70)
				if e.visibility.CommitSeq() != frontier+1 {
					t.Fatal("not one frontier")
				}
				if next.ID() <= old.ID() || a.TransactionState() != TxActive || a.transactions.command != 0 || ticket == oldTicket || a.transactions.writes != nil {
					t.Fatal("completed chain authority")
				}
				// An actual catalog replacement after control execution also cannot
				// turn idempotent Finish into a second end of the newer incarnation.
				if boundary == "success" {
					if err = e.Reset(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				if err = scope.Finish(errors.New("late caller error")); err != nil || a.transactions.ref != next || a.transactions.ticket != ticket || a.transactions.command != 0 {
					t.Fatal("repeated Finish mutated chain", err)
				}
				if boundary == "cancel_after" {
					transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 7, 8, 10)
					transactionRows(t, b, "SELECT v FROM u ORDER BY v", 9, 70)
					if e.visibility.CommitSeq() != frontier+1 {
						t.Fatal("not one frontier")
					}
				}
			} else {
				if err == nil {
					t.Fatal("rejected boundary succeeded")
				}
				if boundary == "cancel_before" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if boundary == "epoch_before" && !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
				wantError := map[string]string{"terminal_before": "stuntgres: inactive operation", "counter_before": "stuntgres: commit sequence exhausted", "ref_before": "stuntgres: stale transaction reference"}[boundary]
				if wantError != "" && err.Error() != wantError {
					t.Fatal("wrong authority/counter error", err, wantError)
				}
				if got := scope.Finish(err); !errors.Is(got, err) {
					t.Fatal("lost original boundary error", got, err)
				}
				if a.TransactionState() == TxActive && a.transactions.ref != old {
					t.Fatal("rejection installed chain")
				}
				if e.visibility.CommitSeq() != frontier || len(stores[0].Versions) != 3 || len(stores[1].Versions) != 1 || &stores[0].Versions[0] != &roots[0][0] || &stores[1].Versions[0] != &roots[1][0] {
					t.Fatal("rejection changed storage frontier/root")
				}
				if !reflect.DeepEqual(roots[0][3:4], spare) {
					t.Fatal("rejection wrote shared spare suffix")
				}
			}
			for i, root := range roots {
				if !reflect.DeepEqual(root, prefixes[i]) {
					t.Fatal("immutable prefix changed", i)
				}
				data := retained[i].Source.View.Data()
				if retained[i].Count != len(root) || data.Frontier != retainedFrontier || data.Catalog.State() != retainedCatalog || data.Catalog.Epoch() != retainedEpoch || !reflect.DeepEqual(data.Committed[addresses[i]], prefixes[i]) {
					t.Fatal("retained rows/types/frontier changed", i)
				}
				if success {
					current := stores[i].Versions
					if len(current) != len(root)+1 || (&current[0] == &root[0]) != (i == 0) {
						t.Fatal("fit/growth root or length", i)
					}
					row := current[len(root)]
					if row.Row == 0 || row.Version == 0 || row.Row != staged[i].Row || row.Version != staged[i].Version || row.Born != frontier+1 || !reflect.DeepEqual(row.Values, staged[i].Values) || len(row.Values) != 1 || testExport(row.Values[0]) != [2]int64{1, 9}[i] {
						t.Fatal("new typed row identity/birth/payload", i, row)
					}
				}
			}
			op.Done()
			if boundary == "terminal_before" {
				if err := a.WaitEnd(context.Background()); err != nil {
					t.Fatal(err)
				}
				lockEmpty(t, e)
			}
			if boundary == "ref_before" {
				if a.transactions.ref != old || a.TransactionState() != TxActive || len(a.transactions.writes) != 2 {
					t.Fatal("bad ref disposed genuine work")
				}
				transactionEnd(t, a, old, false)
				lockEmpty(t, e)
			}
		})
	}
	for _, boundary := range []string{"terminal_after_execute", "replacement_before_first_finish"} {
		t.Run(boundary, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT)")
			sessionSQL(t, b, "INSERT INTO t VALUES(7)")
			address := catalogRef(t, e, e.CatalogRead(), "t").Address
			controlsRequire(t, a, "BEGIN")
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			sessionSQL(t, a, "INSERT INTO t VALUES(1)")
			old, oldTicket := a.transactions.ref, a.transactions.ticket
			frontier := e.visibility.CommitSeq()
			op := transactionOperation(t, a)
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("test cleanup"))
			if _, err := scope.Execute(statement(t, "COMMIT AND CHAIN"), 0, nil); err != nil {
				t.Fatal(err)
			}
			next, ticket := a.transactions.ref, a.transactions.ticket
			if scope.finished || next.ID() <= old.ID() || ticket == nil || ticket == oldTicket ||
				a.TransactionState() != TxActive || a.transactions.command != 0 || a.transactions.writes != nil ||
				e.visibility.CommitSeq() != frontier+1 {
				t.Fatal("execution did not publish once and leave an unfinished fresh chain")
			}
			// A real peer SELECT completes before control Finish: the old AX
			// hold is gone and both typed rows have already been published.
			retained := transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 7)
			data := retained.Source.View.Data()
			catalog, epoch := data.Catalog.State(), data.Catalog.Epoch()
			versions := append([]storage.RowVersion(nil), data.Committed[address]...)
			for i := range versions {
				versions[i].Values = append([]sqltype.Value(nil), versions[i].Values...)
			}
			lockState := e.locks.Inspect()
			oldLive := lockState.Owners[old] != nil
			freshLive := lockState.Owners[next] == ticket
			if oldLive || !freshLive || len(e.locks.Inspect(ticket).Journals[ticket]) != 0 {
				t.Fatal("execution did not release old ticket/install empty fresh authority")
			}
			if boundary == "terminal_after_execute" {
				a.RequestEnd(Terminated)
				<-op.Context().Done()
				select {
				case <-a.completed:
					t.Fatal("physical end bypassed retained operation lease")
				default:
				}
			} else {
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
				if e.CatalogRead().Epoch() == epoch || e.visibility.CommitSeq() != frontier+2 {
					t.Fatal("actual replacement did not install its distinct epoch/frontier")
				}
			}
			completedFrontier := e.visibility.CommitSeq()
			// These are the FIRST and repeated Finish, both after the real
			// lifecycle boundary, not the generic already-finished early return.
			if scope.finished {
				t.Fatal("control finished before lifecycle boundary")
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal("first completed-control Finish retracted accepted publication", err)
			}
			if err := scope.Finish(errors.New("late caller error")); err != nil {
				t.Fatal("repeated completed-control Finish lost saved success", err)
			}
			if a.transactions.ref != next || a.transactions.ticket != ticket ||
				a.TransactionState() != TxActive || a.transactions.command != 0 ||
				a.transactions.writes != nil || e.visibility.CommitSeq() != completedFrontier {
				t.Fatal("Finish ended or mutated the fresh chained incarnation")
			}
			assertRetained := func() {
				t.Helper()
				view := retained.Source.View.Data()
				values := make([]sqltype.Value, 1)
				cursor := independentFinalRows(retained)
				defer cursor.Close()
				for i, want := range []int64{1, 7} {
					if present, err := cursor.Next(context.Background(), values); !present || err != nil || testExport(values[0]) != want {
						t.Fatal("accepted retained typed row changed", i, present, err, values)
					}
				}
				if retained.Count != 2 || view.Frontier != frontier+1 || view.Catalog.State() != catalog ||
					view.Catalog.Epoch() != epoch || !reflect.DeepEqual(view.Committed[address], versions) {
					t.Fatal("lifecycle boundary changed retained committed rows/metadata/frontier")
				}
			}
			assertRetained()
			if boundary == "terminal_after_execute" {
				lockState := e.locks.Inspect()
				freshLive = lockState.Owners[next] == ticket
				if !freshLive {
					t.Fatal("Finish prematurely cleaned leased fresh ticket")
				}
				op.Done()
				if err := a.WaitEnd(context.Background()); err != nil {
					t.Fatal(err)
				}
				if a.TransactionState() != TxIdle || a.transactions.ticket != nil || a.transactions.writes != nil {
					t.Fatal("physical drain retained chained transaction")
				}
				lockEmpty(t, e)
				op.Done()
				a.RequestEnd(Terminated)
				if err := a.WaitEnd(context.Background()); err != nil || e.visibility.CommitSeq() != completedFrontier {
					t.Fatal("repeated drain changed accepted publication", err)
				}
				transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 7)
			} else {
				// Replacement fences lock authority independently of the
				// transaction owner and retained snapshot lifetimes.
				lockState := e.locks.Inspect()
				freshLive = lockState.Owners[next] != nil
				if freshLive {
					t.Fatal("Finish recreated ticket fenced by replacement")
				}
				op.Done()
				probe := transactionOperation(t, a)
				_, err := e.Execute(probe, statement(t, "SELECT v FROM t"), 0, nil)
				probe.Done()
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("ordinary work did not reject stale chained epoch", err)
				}
				controlsRequire(t, a, "ROLLBACK AND CHAIN")
				if a.transactions.ref.ID() <= next.ID() || a.transactions.ref.Epoch() != e.CatalogRead().Epoch() ||
					a.TransactionState() != TxActive || a.transactions.command != 0 {
					t.Fatal("replacement recovery did not create current-epoch chain")
				}
				sessionSQL(t, b, "CREATE TABLE t(v INT)")
				sessionSQL(t, a, "INSERT INTO t VALUES(2)")
				controlsRequire(t, a, "COMMIT")
				transactionRows(t, b, "SELECT v FROM t", 2)
				lockEmpty(t, e)
			}
			assertRetained()
		})
	}
}

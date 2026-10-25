package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// These owner-seam proofs distinguish retained candidates from latest-row
// arbitration. Public SQL/metadata/routes remain in the dual-mode definition.
func TestUpdateCurrentOfPeerCommitRecheck(t *testing.T) {
	for _, lane := range []string{"blocked_peer", "snapshot_gap"} {
		t.Run(lane, func(t *testing.T) {
			e := New()
			owner, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
			rollbackStatement := statement(t, "ROLLBACK")
			rollback := func(s *session.Session) {
				cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
				defer stop()
				operation, err := s.Acquire(cleanup)
				if err != nil {
					t.Error("cleanup admission", err)
					return
				}
				defer operation.Done()
				if _, err := e.Execute(operation, rollbackStatement, 0, nil); err != nil {
					t.Error("cleanup rollback", err)
				}
			}
			defer func() { rollback(owner); rollback(peer) }()
			sessionSQL(t, owner, "CREATE TABLE t(id INT,v TEXT)")
			sessionSQL(t, owner, "INSERT INTO t VALUES(1,'one'),(2,'two'),(3,'three')")
			sessionSQL(t, owner, "BEGIN")
			sessionSQL(t, owner, "DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1 FOR NO KEY UPDATE")
			cursorUnitBatch(t, owner, "FETCH FORWARD 1 FROM c", [][]driver.Value{{int64(1)}})
			c := PayloadOf(owner).Cursors.Entries["c"]
			source, index, root := c.CurrentSource, c.CurrentIndex, c.root
			original := exec.CursorOccurrenceVersion(source, index, 0, c.locks)
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			committed := func() storage.RowVersion {
				e.Mu.Lock()
				defer e.Mu.Unlock()
				for _, row := range e.Storage.Relations[ref.Address].Versions {
					if row.Row == original.Row {
						return row
					}
				}
				return storage.RowVersion{}
			}
			deleteCount(t, owner, "UPDATE t SET v='owner' WHERE CURRENT OF c", 1)
			if c.CurrentSource != source || c.CurrentIndex != index || c.root != root || c.Place != exec.CursorOn || c.authority.Host != nil {
				t.Fatal("positioned owner replaced or advanced borrowed cursor authority")
			}
			if got := committed(); got.Version != original.Version || testExport(got.Values[1]) != "one" {
				t.Fatal("owner published before COMMIT", got)
			}
			if got := tuples(sessionSQL(t, owner, "SELECT v FROM t WHERE id=1")); !reflect.DeepEqual(got, [][]driver.Value{{"owner"}}) {
				t.Fatal("positioned owner's private value", got)
			}
			deleteCount(t, peer, "UPDATE t SET v='other' WHERE id=2", 1)
			if got := tuples(sessionSQL(t, peer, "SELECT v FROM t WHERE id=1")); !reflect.DeepEqual(got, [][]driver.Value{{"one"}}) {
				t.Fatal("peer's precommit committed value", got)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			op, err := peer.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				op.Done()
				t.Fatal(err)
			}
			finished, released := false, false
			var workerDone chan struct{}
			var result Result
			var executeErr error
			// Join before touching scope or ending either session, including Fatal paths.
			defer func() {
				cancel()
				if workerDone != nil {
					select {
					case <-workerDone:
					case <-time.After(5 * time.Second):
						t.Error("peer did not join")
						return
					}
				}
				if !finished {
					_ = scope.Finish(context.Canceled)
				}
				if !released {
					op.Done()
				}
			}()
			update := statement(t, "UPDATE t SET v=v||'-peer' WHERE id=1")
			if err := scope.Gate(update); err != nil {
				t.Fatal(err)
			}
			if err := scope.AcquireRelation(update, true); err != nil {
				t.Fatal(err)
			}
			if err := ScopeSnapshotPhase(scope, update); err != nil {
				t.Fatal(err)
			}
			snapshot, frontier := ScopeCurrentSnapshot(scope), ScopeSnapshotFrontier(scope)
			if snapshot == nil {
				t.Fatal("missing command snapshot")
			}
			execute := func() {
				result, executeErr = e.executeStatement(scope, update, 0, func(context.Context, analyze.ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error {
					return nil
				})
			}
			if lane == "blocked_peer" {
				ticket := scope.Owner.Ticket
				ownerTicket := owner.Transactions.Ticket
				workerDone = make(chan struct{})
				go func() { defer close(workerDone); execute() }()
				lockPending(t, e, 1)
				state := e.Locks.Inspect()
				entry := state.Resources[lock.Resource{ObjectRef: ref, Row: original.Row}]
				if entry == nil || len(entry.Pending) != 1 || entry.Pending[0].Ticket != ticket || entry.Pending[0].Mode != lock.ForNoKeyUpdate || entry.Holders[ownerTicket].Mode&lock.ForNoKeyUpdate == 0 {
					t.Fatal("missing exact peer RowID/ticket/mode and conflicting positioned owner grant")
				}
				if got := committed(); got.Version != original.Version || testExport(got.Values[1]) != "one" {
					t.Fatal("blocked peer saw premature owner publication", got)
				}
			}
			sessionSQL(t, owner, "COMMIT")
			successor := committed()
			if successor.Row != original.Row || successor.Version == original.Version || successor.Born <= frontier || testExport(successor.Values[1]) != "owner" {
				t.Fatal("owner did not commit a distinct same-RowID successor beyond peer frontier", successor)
			}
			if lane == "snapshot_gap" {
				// No target view has been acquired since snapshotPhase. Publication must
				// not erase the original candidate from that selected command frontier.
				view, err := scope.AcquireDataView(scope.LockedRef)
				if err != nil {
					t.Fatal(err)
				}
				captured, err := exec.CaptureSource(ctx, view, scope.LockedRef)
				if err != nil {
					t.Fatal(err)
				}
				defer captured.Close()
				found := 0
				for i := range captured.Count() {
					row := captured.VersionAt(captured.RawIndex(i))
					if row.Row == original.Row {
						found++
						if row.Version != original.Version || testExport(row.Values[1]) != "one" {
							t.Fatal("retained candidate was replaced", row)
						}
					}
				}
				if found != 1 {
					t.Fatalf("selected snapshot lost original row1 candidate during delayed target capture: occurrences=%d source_count=%d", found, captured.Count())
				}
				if captured.Count() != 3 {
					t.Fatal("retained source lost unrelated rows", captured.Count())
				}
				if ScopeCurrentSnapshot(scope) != snapshot || ScopeSnapshotFrontier(scope) != frontier {
					t.Fatal("target capture refreshed snapshot")
				}
				execute()
				for i := range captured.Count() {
					row := captured.VersionAt(captured.RawIndex(i))
					if row.Row == original.Row && (row.Version != original.Version || testExport(row.Values[1]) != "one") {
						t.Fatal("execution mutated retained candidate", row)
					}
				}
			} else {
				select {
				case <-workerDone:
				case <-ctx.Done():
					t.Fatal("peer did not complete after owner COMMIT", ctx.Err())
				}
			}
			if executeErr != nil || result.Count != 1 {
				t.Fatal("peer UPDATE must complete once", result.Count, executeErr)
			}
			staged := 0
			for _, rows := range scope.Stage.Rows {
				for _, row := range rows {
					if row.Row == original.Row {
						staged++
						if row.Version == original.Version || row.Version == successor.Version || row.Base != successor.Version || testExport(row.Values[1]) != "owner-peer" {
							t.Fatal("peer replacement did not use owner Base/latest RHS", row)
						}
					}
				}
			}
			if !WriteStageReplacing(&scope.Stage) || staged != 1 {
				t.Fatal("missing single real private replacement", staged)
			}
			if got := committed(); got.Version != successor.Version || testExport(got.Values[1]) != "owner" {
				t.Fatal("peer Execute prematurely published", got)
			}
			if ScopeCurrentSnapshot(scope) != snapshot || ScopeSnapshotFrontier(scope) != frontier {
				t.Fatal("arbitration refreshed command snapshot")
			}
			err = scope.Finish(nil)
			finished = true
			if err != nil {
				t.Fatal(err)
			}
			op.Done()
			released = true
			want := [][]driver.Value{{int64(1), "owner-peer"}, {int64(2), "other"}, {int64(3), "three"}}
			if got := tuples(sessionSQL(t, peer, "SELECT id,v FROM t ORDER BY id")); !reflect.DeepEqual(got, want) {
				t.Fatal("wrong complete committed rows", got)
			}
			if got := committed(); got.Row != original.Row || testExport(got.Values[1]) != "owner-peer" {
				t.Fatal("publication changed logical identity", got)
			}
			lockEmpty(t, e)
		})
	}
}

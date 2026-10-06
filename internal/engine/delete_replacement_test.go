package engine

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
)

// Observe the existing cooperative boundaries, including publication preflight,
// without changing cancellation or introducing a production callback.
type deleteAdmissionContext struct {
	context.Context
	engine *Engine
	checks int
	lost   bool
}

func (c *deleteAdmissionContext) Err() error {
	c.checks++
	a := &c.engine.replacementMu
	a.mu.Lock()
	c.lost = c.lost || a.readers != 1 || a.writing
	a.mu.Unlock()
	return c.Context.Err()
}

func deleteReplacementAdmissionEmpty(t *testing.T, e *Engine) {
	t.Helper()
	a := &e.replacementMu
	a.mu.Lock()
	readers, writers, writing := a.readers, a.writers, a.writing
	a.mu.Unlock()
	if readers != 0 || writers != 0 || writing {
		t.Fatalf("residual replacement admission: readers=%d writers=%d writing=%t", readers, writers, writing)
	}
	if !a.TryLock() {
		t.Fatal("replacement writer cannot enter after DELETE cleanup")
	}
	a.Unlock()
}

func TestDeleteCoreReplacementImmediateAdmission(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1),(2),(3)")
	op := transactionOperation(t, s)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = scope.Finish(errors.New("test cleanup")) }()
	stmt := statement(t, "DELETE FROM t WHERE v<=2")
	if err = scope.gate(stmt); err != nil {
		t.Fatal(err)
	}
	probe := &deleteAdmissionContext{Context: op.Context(), engine: e}
	op.ctx = probe
	result, err := scope.Execute(stmt, 0, nil)
	if err != nil || result.Count != 2 || result.Rows != nil || !scope.replacementRead {
		t.Fatal("immediate DELETE failed", result, err)
	}
	ref := scope.lockedRef
	e.locks.mu.Lock()
	rows := 0
	for resource, entry := range e.locks.entries {
		if resource.ObjectRef == ref && resource.row != 0 && entry.holders[scope.ticket] == rowUpdate && len(entry.pending) == 0 {
			rows++
		}
	}
	e.locks.mu.Unlock()
	if rows != 2 {
		t.Fatal("immediate DELETE lacks exact row grants", rows)
	}
	if e.replacementMu.TryLock() {
		e.replacementMu.Unlock()
		t.Fatal("immediate row grants lost continuous replacement reservation")
	}
	transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 1, 2, 3)
	frontier := e.commitSeq
	writer := make(chan bool, 1)
	go func() {
		e.replacementMu.Lock()
		e.mu.Lock()
		root := e.storage[ref.Address].ends
		published := root != nil && len(root.rows) == 2 && e.commitSeq == frontier+1
		e.mu.Unlock()
		e.replacementMu.Unlock()
		writer <- published
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.replacementMu.mu.Lock()
		waiting := e.replacementMu.writers == 1 && e.replacementMu.readers == 1 && !e.replacementMu.writing
		e.replacementMu.mu.Unlock()
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("replacement writer did not wait behind immediate DELETE")
		}
		runtime.Gosched()
	}
	checksBeforeFinish := probe.checks
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if probe.lost || checksBeforeFinish == 0 || probe.checks <= checksBeforeFinish {
		t.Fatal("reservation not observed across execution and publication", probe)
	}
	select {
	case published := <-writer:
		if !published {
			t.Fatal("replacement writer admitted before successful DELETE publication")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Finish did not release replacement writer")
	}
	transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 3)
	lockEmpty(t, e)
	deleteReplacementAdmissionEmpty(t, e)
}

// Capture the exact DELETE row request, not just a goroutine-start signal or a
// relation waiter. All observations use the manager's existing protected journal.
func deleteReplacementQueued(t *testing.T, e *Engine, resource lockResource, holder, waiter *relationTicket) *relationRequest {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.locks.mu.Lock()
		entry := e.locks.entries[resource]
		var request *relationRequest
		if entry != nil && entry.holders[holder] == rowUpdate && len(entry.pending) == 1 {
			r := entry.pending[0]
			if r.ticket == waiter && r.ref == resource && r.mode == rowUpdate && !r.terminal {
				request = r
			}
		}
		e.locks.mu.Unlock()
		if request != nil {
			return request
		}
		if time.Now().After(deadline) {
			t.Fatal("exact DELETE row request did not queue behind active holder")
		}
		runtime.Gosched()
	}
}

func deleteReplacementScope(t *testing.T, e *Engine, s *Session) (*StatementScope, *relationTicket, <-chan deleteOutcome) {
	t.Helper()
	op := transactionOperation(t, s)
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	stmt := statement(t, "DELETE FROM t WHERE v<=2")
	if err = scope.gate(stmt); err != nil {
		_ = scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	ticket := scope.ticket
	done := make(chan deleteOutcome, 1)
	go func() {
		result, err := scope.Execute(stmt, 0, nil)
		done <- deleteOutcome{result, err}
	}()
	return scope, ticket, done
}

func deleteReplacementReject(t *testing.T, scope *StatementScope, done <-chan deleteOutcome) {
	t.Helper()
	var outcome deleteOutcome
	select {
	case outcome = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stale DELETE did not leave row wait/readmission")
	}
	var diagnostic *sqlstate.Error
	if !errors.Is(outcome.err, ErrCatalogReplaced) || errors.As(outcome.err, &diagnostic) || outcome.result.Count != 0 || outcome.result.Rows != nil {
		t.Fatal("replacement must return ordinary ErrCatalogReplaced and no successful result", outcome)
	}
	ticket := scope.ticket
	if err := scope.Finish(outcome.err); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal(err)
	}
	scope.op.Done()
	manager := scope.op.session.Engine().locks
	manager.mu.Lock()
	resources := len(ticket.resources)
	_, registered := manager.owners[ticket.ref]
	manager.mu.Unlock()
	if resources != 0 || registered {
		t.Fatal("stale DELETE ticket retained lock journal", resources, registered)
	}
	if scope.stage.rows != nil || scope.stage.deletes != nil || scope.view != nil || scope.ticket != nil || scope.replacementRead {
		t.Fatal("stale DELETE retained stage/view/grants/reservation")
	}
	owner := scope.owner
	if owner.writes != nil || owner.deletes != nil || owner.ticket != nil {
		t.Fatal("stale DELETE retained prior private work")
	}
}

func TestDeleteCoreReplacementWaitingPhases(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		for _, explicit := range []bool{false, true} {
			t.Run(replacement+"/"+map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
				e := New()
				holder := sessionStart(t, e, nil)
				waiter := sessionStart(t, e, nil)
				peer := sessionStart(t, e, nil)
				sessionSQL(t, peer, "CREATE TABLE t(v INT)")
				sessionSQL(t, peer, "INSERT INTO t VALUES(1),(2),(3)")
				ref := catalogRef(t, e, e.CatalogRead(), "t")
				oldStorage := e.storage[ref.Address]
				resource := lockResource{ObjectRef: ref, row: oldStorage.versions[1].row}
				hr := transactionBegin(t, holder)
				deleteCount(t, holder, "DELETE FROM t WHERE v=2", 1)
				var wr TxRef
				if explicit {
					wr = transactionBegin(t, waiter)
					sessionSQL(t, waiter, "INSERT INTO t VALUES(7)")
					deleteCount(t, waiter, "DELETE FROM t WHERE v=3", 1)
				}
				scope, ticket, done := deleteReplacementScope(t, e, waiter)
				request := deleteReplacementQueued(t, e, resource, holder.transactions.ticket, ticket)
				root := scope.stage.deletes[ref.Address]
				first := oldStorage.versions[0].row
				if root == nil || len(root.rows) != 1 || root.rows[first].row != first {
					t.Fatal("earlier matching row was not privately staged before wait")
				}
				if explicit {
					prior := waiter.transactions.deletes[ref.Address]
					if len(waiter.transactions.writes[ref.Address]) != 1 || prior == nil || len(prior.rows) != 1 {
						t.Fatal("explicit prior INSERT/DELETE work absent before replacement")
					}
				}
				// The holder has not ended. Real replacement can complete only if
				// the implicit row waiter relinquished its admission reservation.
				if replacement == "reset" {
					if err := e.Reset(context.Background()); err != nil {
						t.Fatal(err)
					}
					sessionSQL(t, peer, "CREATE TABLE t(v INT)")
					sessionSQL(t, peer, "INSERT INTO t VALUES(1),(2),(2),(91)")
				} else if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(v INT);INSERT INTO t VALUES(1),(2),(2),(91)")); err != nil {
					t.Fatal(err)
				}
				if holder.TransactionState() != TxActive || e.CatalogRead().Epoch() != ref.Epoch+1 {
					t.Fatal("replacement did not complete with old holder active")
				}
				deleteReplacementReject(t, scope, done)
				e.locks.mu.Lock()
				fenced := request.terminal && errors.Is(request.err, ErrCatalogReplaced)
				e.locks.mu.Unlock()
				if !fenced || oldStorage.ends != nil {
					t.Fatal("queued request not fenced or private deletion published")
				}
				if explicit {
					if waiter.TransactionState() != TxAborted {
						t.Fatal("explicit stale DELETE did not abort")
					}
					transactionEnd(t, waiter, wr, false)
				} else if waiter.TransactionState() != TxIdle {
					t.Fatal("implicit stale DELETE did not clear transaction")
				}
				lockEmpty(t, e)
				deleteReplacementAdmissionEmpty(t, e)
				transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 1, 2, 2, 91)
				transactionEnd(t, holder, hr, false)
				transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 1, 2, 2, 91)
				lockEmpty(t, e)
			})
		}
	}
}

func TestDeleteCoreReplacementGrantedReadmission(t *testing.T) {
	e := New()
	holder := sessionStart(t, e, nil)
	waiter := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE t(v INT)")
	sessionSQL(t, peer, "INSERT INTO t VALUES(1),(2),(3)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	oldStorage := e.storage[ref.Address]
	first, blocked := oldStorage.versions[0].row, oldStorage.versions[1].row
	resource := lockResource{ObjectRef: ref, row: blocked}
	hr := transactionBegin(t, holder)
	deleteCount(t, holder, "DELETE FROM t WHERE v=2", 1)
	scope, ticket, done := deleteReplacementScope(t, e, waiter)
	request := deleteReplacementQueued(t, e, resource, holder.transactions.ticket, ticket)
	// Use the existing final-install seam with a genuine independently seeded
	// foreign generation. Whole Reset/Restore calls are covered above; invoking
	// them while already holding this writer reservation would be reentrant.
	source := New()
	run(t, source, "CREATE TABLE t(v INT)")
	run(t, source, "INSERT INTO t VALUES(1),(2),(2),(91)")
	replacement := source.CatalogRead()
	sourceRoles, targetRoles := source.Roles(), e.Roles()
	storage := make(map[ObjectAddress]*relationStorage)
	source.mu.Lock()
	for address, relation := range source.storage {
		storage[address] = &relationStorage{versions: append([]rowVersion(nil), relation.versions...)}
	}
	source.mu.Unlock()
	e.replacementMu.Lock()
	writerHeld := true
	defer func() {
		// Also release on assertion failure, so a blocked readmission can finish.
		if writerHeld {
			e.replacementMu.Unlock()
		}
	}()
	transactionEnd(t, holder, hr, false)
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.locks.mu.Lock()
		entry := e.locks.entries[resource]
		granted := request.terminal && request.err == nil && entry != nil && entry.holders[ticket] == rowUpdate && len(entry.pending) == 0
		_, journaled := ticket.resources[resource]
		e.locks.mu.Unlock()
		if granted && journaled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual DELETE row transfer absent before readmission")
		}
		runtime.Gosched()
	}
	// The writer excludes implicit readmission: row one was staged before the
	// wait, but the newly granted row two must not reach the stage/publication.
	root := scope.stage.deletes[ref.Address]
	if root == nil || len(root.rows) != 1 || root.rows[first].row != first || oldStorage.ends != nil {
		t.Fatal("DELETE advanced past readmission or failed to stage earlier private row")
	}
	select {
	case outcome := <-done:
		t.Fatal("DELETE returned while writer excluded readmission", outcome)
	default:
	}
	e.mu.Lock()
	next, err := e.remapCatalog(replacement.state, sourceRoles, targetRoles, nil)
	if err == nil {
		nextRevision := e.nextRevision
		for _, descriptor := range next.descriptors {
			if err = validateACL(RoleRead{e, e.roles}, descriptor); err != nil {
				break
			}
			if revision := descriptor.Revision(); revision >= nextRevision {
				nextRevision = revision + 1
			}
		}
		if err == nil {
			err = e.installForeignCatalogLocked(context.Background(), next, storage, nextRevision)
		}
	}
	epoch := e.state.epoch
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	e.locks.fence(epoch)
	e.replacementMu.Unlock()
	writerHeld = false
	deleteReplacementReject(t, scope, done)
	if epoch != ref.Epoch+1 || oldStorage.ends != nil || waiter.TransactionState() != TxIdle {
		t.Fatal("stale granted DELETE published or retained implicit work")
	}
	lockEmpty(t, e)
	deleteReplacementAdmissionEmpty(t, e)
	transactionRows(t, peer, "SELECT v FROM t ORDER BY v", 1, 2, 2, 91)
	lockEmpty(t, e)
}

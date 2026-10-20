package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func truncateRestartUnitAsyncSQL(t *testing.T, e *Engine, s *Session, q string) (<-chan error, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done, finished := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(finished)
		op, err := s.Acquire(ctx)
		if err == nil {
			var r Result
			r, err = e.Execute(op, mustLockStatement(q), 0, nil)
			if r.Rows != nil {
				r.Rows.Close()
			}
			op.Done()
		}
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("sequence worker did not join")
		}
	})
	return done, cancel
}

func TestTruncateRestartLocks(t *testing.T) {
	for _, heldMode := range []lock.Mode{lock.AccessShare, lock.RowExclusive} {
		t.Run(map[lock.Mode]string{lock.AccessShare: "SequenceRowAS", lock.RowExclusive: "FunctionRX"}[heldMode], func(t *testing.T) {
			e, holder, p, q, ps, qs := truncateRestartUnitFixture(t)
			waiter := sessionStart(t, e, nil)
			tx := transactionBegin(t, holder)
			if heldMode == lock.AccessShare {
				r := sessionSQL(t, holder, "SELECT * FROM q_id_seq")
				r.Rows.Close()
			} else {
				sequenceScalar(t, holder, "SELECT nextval('q_id_seq')", 45)
			}
			oldP, oldQ, oldPS, oldQS := e.storage.Relations[p.Address], e.storage.Relations[q.Address], e.storage.Sequences[ps.Address], e.storage.Sequences[qs.Address]
			done, cancel := truncateUnitAsync(t, e, waiter, truncateRestartUnitAST("p", "q"))
			lockPending(t, e, 1)
			lockState := e.locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: qs}]
			valid := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessExclusive && entry.Holders[holder.transactions.ticket].Mode == heldMode
			if valid {
				ticket := entry.Pending[0].Ticket
				for _, ref := range []ids.ObjectRef{p, q, ps} {
					prefix := lockState.Resources[lock.Resource{ObjectRef: ref}]
					valid = valid && prefix != nil && prefix.Holders[ticket].Mode == lock.AccessExclusive
				}
			}
			if !valid {
				t.Fatal("not a real ordered AX prefix/owned-sequence wait")
			}
			cancel()
			lockAwait(t, done, context.Canceled)
			if e.storage.Relations[p.Address] != oldP || e.storage.Relations[q.Address] != oldQ || e.storage.Sequences[ps.Address] != oldPS || e.storage.Sequences[qs.Address] != oldQS || oldPS.Last != 2 {
				t.Fatal("canceled acquisition published prefix")
			}
			last := int64(44)
			if heldMode == lock.RowExclusive {
				last = 45
			}
			if oldQS.Last != last {
				t.Fatal("ordinary pre-reset draw was undone")
			}
			transactionEnd(t, holder, tx, false)
			truncateUnitExecute(t, e, waiter, truncateRestartUnitAST("p", "q"))
			lockEmpty(t, e)
		})
	}
	for _, commit := range []bool{false, true} {
		for _, query := range []string{"SELECT nextval('p_id_seq')", "SELECT currval('p_id_seq')", "SELECT lastval()", "SELECT setval('p_id_seq',80)", "SELECT * FROM p_id_seq"} {
			t.Run(map[bool]string{false: "Rollback/", true: "Commit/"}[commit]+query, func(t *testing.T) {
				e, holder, _, _, ps, _ := truncateRestartUnitFixture(t)
				waiter := sessionStart(t, e, nil)
				sessionSQL(t, holder, "ALTER SEQUENCE p_id_seq CACHE 4")
				sequenceScalar(t, waiter, "SELECT nextval('p_id_seq')", 3)
				tx := transactionBegin(t, holder)
				truncateUnitExecute(t, e, holder, truncateRestartUnitAST("p"))
				candidate := holder.transactions.catalog.generators[ps.Address]
				done, _ := truncateRestartUnitAsyncSQL(t, e, waiter, query)
				lockPending(t, e, 1)
				lockState := e.locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: ps}]
				wantMode := lock.RowExclusive
				if query == "SELECT * FROM p_id_seq" {
					wantMode = lock.AccessShare
				}
				valid := entry != nil && entry.Holders[holder.transactions.ticket].Mode == lock.AccessExclusive && len(entry.Pending) == 1 && entry.Pending[0].Mode == wantMode
				if !valid {
					t.Fatal("function/cache bypassed successful reset AX")
				}
				transactionEnd(t, holder, tx, commit)
				lockAwait(t, done, nil)
				if commit && e.storage.Sequences[ps.Address] != candidate {
					t.Fatal("commit lost replacement")
				}
				if !commit && e.storage.Sequences[ps.Address] == candidate {
					t.Fatal("rollback installed replacement")
				}
				want := int64(3)
				if query == "SELECT nextval('p_id_seq')" {
					want = 4
					if commit {
						want = 1
					}
				}
				if query == "SELECT setval('p_id_seq',80)" {
					want = 80
				}
				sequenceScalar(t, waiter, "SELECT currval('p_id_seq')", want)
				lockEmpty(t, e)
			})
		}
	}
	t.Run("OwnershipCycleCleanup", func(t *testing.T) {
		e, holder, p, _, ps, _ := truncateRestartUnitFixture(t)
		waiter := sessionStart(t, e, nil)
		tx := transactionBegin(t, holder)
		holderTicket := holder.transactions.ticket
		oldStore, oldGenerator := e.storage.Relations[p.Address], e.storage.Sequences[ps.Address]
		// Pause at OWNED BY's actual first grant, before its table-AS
		// dependency. TRUNCATE retains table-AX then requests sequence-AX.
		// The existing shared detector can resolve the closing hard cycle
		// immediately; two simultaneously pending requests are not promised.
		if err := e.locks.Acquire(context.Background(), holderTicket, lock.Resource{ObjectRef: ps}, lock.ShareRowExclusive); err != nil {
			t.Fatal(err)
		}
		reset, _ := truncateUnitAsync(t, e, waiter, truncateRestartUnitAST("p"))
		lockPending(t, e, 1)
		lockState := e.locks.Inspect()
		table := lockState.Resources[lock.Resource{ObjectRef: p}]
		sequence := lockState.Resources[lock.Resource{ObjectRef: ps}]
		var resetTicket *lock.Ticket
		valid := table != nil && sequence != nil && len(sequence.Pending) == 1 && sequence.Pending[0].Mode == lock.AccessExclusive && sequence.Holders[holderTicket].Mode == lock.ShareRowExclusive
		if valid {
			resetTicket = sequence.Pending[0].Ticket
			valid = table.Holders[resetTicket].Mode == lock.AccessExclusive
		}
		if !valid {
			t.Fatal("missing real table-AX/sequence-AX prefix and sequence-SRX holder")
		}
		alter, _ := truncateRestartUnitAsyncSQL(t, e, holder, "ALTER SEQUENCE p_id_seq OWNED BY p.id")
		var outcomes [2]error
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for range 2 {
			select {
			case outcomes[0] = <-reset:
				reset = nil
			case outcomes[1] = <-alter:
				alter = nil
			case <-timer.C:
				t.Fatal("ownership cycle workers did not terminate")
			}
		}
		// Identify the victim by its error, never channel arrival order.
		if (outcomes[0] == nil) == (outcomes[1] == nil) {
			t.Fatalf("want one victim and one survivor: %v", outcomes)
		}
		victim := 0
		if outcomes[1] != nil {
			victim = 1
		}
		var diagnostic *sqlstate.Error
		if !errors.As(outcomes[victim], &diagnostic) {
			t.Fatal("missing cycle diagnostic", outcomes[victim])
		}
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40P01", Message: "deadlock detected", Hint: "See server log for query details.", Detail: diagnostic.Detail}
		if *diagnostic != want {
			t.Fatalf("cycle fields %#v want %#v", diagnostic, want)
		}
		expected := make(map[string]bool, 2)
		for _, edge := range []struct {
			waiter, blocker *lock.Ticket
			ref             ids.ObjectRef
			mode            string
		}{
			{holderTicket, resetTicket, p, "AccessShareLock"},
			{resetTicket, holderTicket, ps, "AccessExclusiveLock"},
		} {
			expected[fmt.Sprintf("Transaction %d (session %d, epoch %d) waits for %s on relation %d (class %d, sub-ID %d, epoch %d); blocked by transaction %d (session %d, epoch %d).", edge.waiter.Ref().ID(), edge.waiter.Ref().Session().ID(), edge.waiter.Ref().Epoch(), edge.mode, edge.ref.Address.ObjectID, edge.ref.Address.ClassID, edge.ref.Address.SubID, edge.ref.Epoch, edge.blocker.Ref().ID(), edge.blocker.Ref().Session().ID(), edge.blocker.Ref().Epoch())] = true
		}
		for _, line := range strings.Split(diagnostic.Detail, "\n") {
			if !expected[line] {
				t.Fatalf("unexpected cycle edge %q", line)
			}
			delete(expected, line)
		}
		if len(expected) != 0 {
			t.Fatal("missing ownership cycle edges", expected)
		}
		if victim == 1 {
			if holder.TransactionState() != TxAborted {
				t.Fatal("OWNED BY victim did not abort")
			}
			if e.storage.Sequences[ps.Address] == oldGenerator || e.storage.Sequences[ps.Address].Last != 1 || e.storage.Sequences[ps.Address].Called || e.storage.Relations[p.Address] == oldStore || len(e.storage.Relations[p.Address].Versions) != 0 {
				t.Fatal("cycle survivor failed complete restart")
			}
		} else if e.storage.Sequences[ps.Address] != oldGenerator || e.storage.Relations[p.Address] != oldStore || oldGenerator.Last != 2 || len(oldStore.Versions) != 2 {
			t.Fatal("failed reset published a prefix")
		}
		transactionEnd(t, holder, tx, false)
		// Cancellation is covered by the acyclic waits above; after genuine
		// cycle resolution the same sessions must admit a healthy fresh reset.
		truncateUnitExecute(t, e, waiter, truncateRestartUnitAST("p"))
		lockEmpty(t, e)
	})
}

func TestTruncateRestartControlFence(t *testing.T) {
	for _, control := range []string{"Reset", "Restore"} {
		for _, phase := range []string{"QueuedReset", "StagedReset"} {
			t.Run(control+"/"+phase, func(t *testing.T) {
				e, holder, p, _, ps, _ := truncateRestartUnitFixture(t)
				waiter := sessionStart(t, e, nil)
				tx := transactionBegin(t, holder)
				oldStore, oldGenerator := e.storage.Relations[p.Address], e.storage.Sequences[ps.Address]
				var done <-chan error
				if phase == "QueuedReset" {
					r := sessionSQL(t, holder, "SELECT * FROM p_id_seq")
					r.Rows.Close()
					done, _ = truncateUnitAsync(t, e, waiter, truncateRestartUnitAST("p"))
				} else {
					truncateUnitExecute(t, e, holder, truncateRestartUnitAST("p"))
					done, _ = truncateRestartUnitAsyncSQL(t, e, waiter, "SELECT nextval('p_id_seq')")
				}
				lockPending(t, e, 1)
				if control == "Reset" {
					if err := e.Reset(context.Background()); err != nil {
						t.Fatal(err)
					}
				} else if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE p(id SERIAL PRIMARY KEY); INSERT INTO p DEFAULT VALUES;")); err != nil {
					t.Fatal(err)
				}
				lockAwait(t, done, ids.ErrCatalogReplaced)
				if phase == "StagedReset" {
					op := transactionOperation(t, holder)
					_, err := e.Commit(op, tx)
					op.Done()
					if err != ids.ErrCatalogReplaced {
						t.Fatal("old transaction published across replacement", err)
					}
					controlsRequire(t, holder, "ROLLBACK")
				} else {
					transactionEnd(t, holder, tx, false)
				}
				if holder.sequences.memory(ps.Address) != nil || waiter.sequences.memory(ps.Address) != nil {
					t.Fatal("persistent replacement retained retired sequence observation/source event")
				}
				if len(oldStore.Versions) != 2 || oldGenerator.Last != 2 || oldStore.TruncateLineage != nil {
					t.Fatal("control fence modified retained predecessor")
				}
				if control == "Reset" {
					sessionSQL(t, waiter, "CREATE TABLE p(id SERIAL PRIMARY KEY)")
				}
				current := roleTableRef(t, e, "p_id_seq")
				if current == ps || e.storage.Sequences[current.Address] == oldGenerator {
					t.Fatal("retired generator resurrected")
				}
				want := int64(1)
				if control == "Restore" {
					want = 2
				}
				sequenceScalar(t, waiter, "SELECT nextval('p_id_seq')", want)
				truncateUnitExecute(t, e, waiter, truncateRestartUnitAST("p"))
				lockEmpty(t, e)
			})
		}
		t.Run(control+"/LiveTemporaryAnalysis", func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TEMP TABLE local_t(id SERIAL)")
			ast, description := sequencePreparedDescription(t, e, s, "SELECT nextval('local_t_id_seq')")
			ref := description.sequences.constants[0].ref
			tx := transactionBegin(t, s)
			truncateUnitExecute(t, e, s, truncateRestartUnitAST("local_t"))
			transactionEnd(t, s, tx, false)
			memory := s.sequences.memory(ref.Address)
			if memory == nil || memory.sourceStamp() == 0 || currvalValid(memory) {
				t.Fatal("missing zero-valid local analysis event")
			}
			stamp := memory.sourceStamp()
			if control == "Reset" {
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else if err := e.Restore(context.Background(), strings.NewReader("CREATE TABLE replacement(id INT);")); err != nil {
				t.Fatal(err)
			}
			if s.sequences.memory(ref.Address) != memory || memory.sourceStamp() != stamp || currvalValid(memory) {
				t.Fatal("exact caller-owned live TEMP event lost across control replacement")
			}
			result, err := sequencePreparedExecute(e, s, ast, &description)
			if err != nil {
				t.Fatal(err)
			}
			got := tuples(result)
			result.Rows.Close()
			if len(got) != 1 || len(got[0]) != 1 || got[0][0] != int64(1) {
				t.Fatal("live TEMP prepared provenance failed", got)
			}
			s.RequestEnd(CleanClose)
			if err := s.WaitEnd(context.Background()); err != nil {
				t.Fatal(err)
			}
			if s.sequences.Len() != 0 {
				t.Fatal("physical end retained source events")
			}
			lockEmpty(t, e)
		})
	}
}

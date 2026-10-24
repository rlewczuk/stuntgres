package engine

import (
	"errors"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"

	"stuntgres/internal/engine/lock"
)

func exclusionInsertAwait(t *testing.T, done <-chan deleteOutcome, count int64, code string) {
	t.Helper()
	select {
	case got := <-done:
		if got.result.Rows != nil || got.result.Count != count {
			t.Fatal("wrong exclusion row outcome", got)
		}
		if code == "" {
			if got.err != nil {
				t.Fatal(got.err)
			}
			return
		}
		var diagnostic *sqlstate.Error
		if !errors.As(got.err, &diagnostic) || diagnostic.Code != code {
			t.Fatal("wrong exclusion peer diagnostic", got.err, code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exclusion inserter did not join")
	}
}

func TestExclusionEagerPeerOutcome(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		for _, commit := range []bool{false, true} {
			t.Run(method+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
				e := New()
				a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE TABLE t(k INT,CONSTRAINT ex EXCLUDE USING "+method+"(k WITH =))")
				ar := transactionBegin(t, a)
				sessionSQL(t, a, "INSERT INTO t VALUES(1)")
				done, _ := uniqueAsyncUnit(t, b, "INSERT INTO t VALUES(1)")
				lockPending(t, e, 1)
				lockState := e.locks.Inspect()
				matched := false
				for resource, entry := range lockState.Resources {
					if resource.Key() != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.UniqueClaim && entry.Pending[0].Ticket.Session() == b.SessionToken {
						matched = true
					}
				}
				if !matched {
					t.Fatal("same-key wait lacks its actual typed key resource")
				}
				other, _ := uniqueAsyncUnit(t, c, "INSERT INTO t VALUES(2)")
				exclusionInsertAwait(t, other, 1, "")
				lockPending(t, e, 1)
				transactionEnd(t, a, ar, commit)
				if commit {
					exclusionInsertAwait(t, done, 0, "23P01")
				} else {
					exclusionInsertAwait(t, done, 1, "")
				}
				if e.exclusions.CountLocked() != 0 {
					t.Fatal("peer outcome retained dirty membership")
				}
				uniqueClaimsEmpty(t, e)
			})
		}
	}
}

func TestExclusionProvisionalDeletion(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		for _, deferrable := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				name := method + map[bool]string{false: "/nondeferrable", true: "/deferrable"}[deferrable] + map[bool]string{false: "/rollback", true: "/commit"}[commit]
				t.Run(name, func(t *testing.T) {
					e := New()
					a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
					properties := ""
					if deferrable {
						properties = " DEFERRABLE"
					}
					sessionSQL(t, a, "CREATE TABLE t(k INT,CONSTRAINT ex EXCLUDE USING "+method+"(k WITH =)"+properties+")")
					sessionSQL(t, a, "INSERT INTO t VALUES(1)")
					ar := transactionBegin(t, a)
					sessionSQL(t, a, "DELETE FROM t")
					at := a.Transactions.Ticket
					projection := e.exclusions.OwnLocked(at)
					parent := roleTableRef(t, e, "public.t")
					index := e.catalog.State.MustRelation(parent.Address).Constraints.Exclusion[0].Index
					if projection == nil || len(projection.Ended[parent.Address]) != 1 {
						t.Fatal("provisional deletion lacks its actual ended version")
					}
					done, _ := uniqueAsyncUnit(t, b, "INSERT INTO t VALUES(1)")
					lockPending(t, e, 1)
					lockState := e.locks.Inspect()
					matched := false
					if deferrable {
						entry := lockState.Resources[lock.CompletionOf(at)]
						matched = entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.TransactionCompletion && entry.Pending[0].Ticket.Session() == b.SessionToken && entry.Holders[at].Mode&lock.TransactionCompletion != 0
					} else {
						// DELETE retains the existing exact-key grant. The eager
						// admission waits here before its dirty completion probe.
						for resource, entry := range lockState.Resources {
							if resource.Key() != nil && resource.Address == index && len(resource.Key().Value().([]sqltype.Value)) == 1 && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.UniqueClaim && entry.Pending[0].Ticket.Session() == b.SessionToken && entry.Holders[at].Mode&lock.UniqueClaim != 0 {
								matched = true
							}
						}
					}
					if !matched {
						t.Fatal("provisional deletion lacks its exact expected owner wait", deferrable)
					}
					transactionEnd(t, a, ar, commit)
					if commit {
						exclusionInsertAwait(t, done, 1, "")
					} else {
						exclusionInsertAwait(t, done, 0, "23P01")
					}
					if e.exclusions.CountLocked() != 0 {
						t.Fatal("provisional deletion retained a completed ticket")
					}
					uniqueClaimsEmpty(t, e)
				})
			}
		}
	}
}

func TestExclusionCompletionDeadlock(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(k INT,CONSTRAINT ex EXCLUDE(k WITH =) DEFERRABLE INITIALLY DEFERRED)")
	ar, br := transactionBegin(t, a), transactionBegin(t, b)
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	sessionSQL(t, b, "INSERT INTO t VALUES(2)")
	sessionSQL(t, a, "INSERT INTO t VALUES(2)")
	sessionSQL(t, b, "INSERT INTO t VALUES(1)")
	at, bt := a.Transactions.Ticket, b.Transactions.Ticket
	if len(ownerExtensionOrZero(a.Transactions).exclusionModes.Events) != 1 || len(ownerExtensionOrZero(b.Transactions).exclusionModes.Events) != 1 {
		t.Fatal("crossed admissions must independently enlist both owners")
	}
	first := exclusionCommitWorker(t, a, ar)
	exclusionCompletionQueued(t, e, bt, at, first)
	second := exclusionCommitWorker(t, b, br)
	var left, right error
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for first != nil || second != nil {
		select {
		case left = <-first:
			first = nil
		case right = <-second:
			second = nil
		case <-timer.C:
			t.Fatal("closed completion cycle did not resolve")
		}
	}
	if (left == nil) == (right == nil) {
		t.Fatal("want exactly one actual deadlock victim", left, right)
	}
	victim := left
	if victim == nil {
		victim = right
	}
	var diagnostic *sqlstate.Error
	if !errors.As(victim, &diagnostic) || diagnostic.Code != "40P01" {
		t.Fatal("completion cycle did not use existing deadlock authority", victim)
	}
	if e.exclusions.CountLocked() != 0 {
		t.Fatal("resolved deadlock retained dirty owners")
	}
	btreeWantKeys(t, e, "ex", "1", "2")
	uniqueClaimsEmpty(t, e)
}

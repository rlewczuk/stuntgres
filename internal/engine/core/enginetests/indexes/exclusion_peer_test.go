package indexes_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
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
				e := engine.New()
				a, b, c := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, a, "CREATE TABLE t(k INT,CONSTRAINT ex EXCLUDE USING "+method+"(k WITH =))")
				ar := enginetest.TransactionBegin(t, a)
				enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
				done, _ := uniqueAsyncUnit(t, b, "INSERT INTO t VALUES(1)")
				enginetest.LockPending(t, e, 1)
				lockState := e.Locks.Inspect()
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
				enginetest.LockPending(t, e, 1)
				enginetest.TransactionEnd(t, a, ar, commit)
				if commit {
					exclusionInsertAwait(t, done, 0, "23P01")
				} else {
					exclusionInsertAwait(t, done, 1, "")
				}
				if e.Exclusions.CountLocked() != 0 {
					t.Fatal("peer outcome retained dirty membership")
				}
				enginetest.UniqueClaimsEmpty(t, e)
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
					e := engine.New()
					a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
					properties := ""
					if deferrable {
						properties = " DEFERRABLE"
					}
					enginetest.SessionSQL(t, a, "CREATE TABLE t(k INT,CONSTRAINT ex EXCLUDE USING "+method+"(k WITH =)"+properties+")")
					enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
					ar := enginetest.TransactionBegin(t, a)
					enginetest.SessionSQL(t, a, "DELETE FROM t")
					at := a.Transactions.Ticket
					projection := e.Exclusions.OwnLocked(at)
					parent := enginetest.RoleTableRef(t, e, "public.t")
					index := e.Catalog.State.MustRelation(parent.Address).Constraints.Exclusion[0].Index
					if projection == nil || len(projection.Ended[parent.Address]) != 1 {
						t.Fatal("provisional deletion lacks its actual ended version")
					}
					done, _ := uniqueAsyncUnit(t, b, "INSERT INTO t VALUES(1)")
					enginetest.LockPending(t, e, 1)
					lockState := e.Locks.Inspect()
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
					enginetest.TransactionEnd(t, a, ar, commit)
					if commit {
						exclusionInsertAwait(t, done, 1, "")
					} else {
						exclusionInsertAwait(t, done, 0, "23P01")
					}
					if e.Exclusions.CountLocked() != 0 {
						t.Fatal("provisional deletion retained a completed ticket")
					}
					enginetest.UniqueClaimsEmpty(t, e)
				})
			}
		}
	}
}

type deleteOutcome struct {
	result core.Result
	err    error
}

func uniqueAsyncUnit(t *testing.T, s *session.Session, query string) (<-chan deleteOutcome, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ast := enginetest.Statement(t, query)
	done := make(chan deleteOutcome, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		op, _, err := s.AcquireCommand(ctx)
		var result core.Result
		if err == nil {
			result, err = core.EngineOf(s).Execute(op, ast, 0, nil)
			err = op.CommandError(err)
			op.Done()
		}
		done <- deleteOutcome{result, err}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("UNIQUE worker did not join")
		}
	})
	return done, cancel
}

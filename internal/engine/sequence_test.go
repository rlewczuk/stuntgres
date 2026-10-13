package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"sync"
	"testing"
)

func sequenceScalar(t *testing.T, s *Session, q string, want int64) {
	t.Helper()
	r := sessionSQL(t, s, q)
	defer r.Rows.Close()
	if got := tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{want}}) {
		t.Fatalf("%s got %v want %d", q, got, want)
	}
}

func TestSequenceAllocationConcurrentSessions(t *testing.T) {
	for _, cache := range []string{"1", "7"} {
		t.Run("cache"+cache, func(t *testing.T) {
			e := New()
			owner := sessionStart(t, e, nil)
			sessionSQL(t, owner, "CREATE SEQUENCE s CACHE "+cache)
			const clients = 8
			const each = 40
			sessions := make([]*Session, clients)
			for i := range sessions {
				sessions[i] = sessionStart(t, e, nil)
			}
			values := make(chan int64, clients*each)
			failures := make(chan error, clients)
			var wg sync.WaitGroup
			for _, s := range sessions {
				wg.Add(1)
				go func(s *Session) {
					defer wg.Done()
					for range each {
						op, err := s.Acquire(context.Background())
						if err != nil {
							failures <- err
							return
						}
						r, err := e.Execute(op, mustLockStatement("SELECT nextval('s')"), 0, nil)
						if err == nil {
							rows := tuples(r)
							r.Rows.Close()
							values <- rows[0][0].(int64)
						}
						op.Done()
						if err != nil {
							failures <- err
							return
						}
					}
				}(s)
			}
			wg.Wait()
			close(values)
			close(failures)
			for err := range failures {
				t.Fatal(err)
			}
			seen := make(map[int64]bool)
			for v := range values {
				if seen[v] {
					t.Fatalf("duplicate %d", v)
				}
				seen[v] = true
			}
			if len(seen) != clients*each {
				t.Fatal("missing allocations", len(seen))
			}
			if cache == "1" {
				for v := int64(1); v <= clients*each; v++ {
					if !seen[v] {
						t.Fatalf("CACHE1 bag missing %d", v)
					}
				}
			}
		})
	}
}

func TestSequenceLocksCachedCallsAndCancellation(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE SEQUENCE s CACHE 4")
	sequenceScalar(t, b, "SELECT nextval('s')", 1)
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "ALTER SEQUENCE s RESTART 20")
	ctx, cancel := context.WithCancel(context.Background())
	done := lockAsync(e, b, ctx, "SELECT nextval('s')", false)
	lockPending(t, e, 1)
	cancel()
	lockAwait(t, done, context.Canceled)
	done = lockAsync(e, b, context.Background(), "SELECT nextval('s')", false)
	lockPending(t, e, 1)
	transactionEnd(t, a, tx, false)
	lockAwait(t, done, nil)
	sequenceScalar(t, b, "SELECT currval('s')", 2)
	tx = transactionBegin(t, a)
	sessionSQL(t, a, "ALTER SEQUENCE s RESTART 30")
	done = lockAsync(e, b, context.Background(), "SELECT currval('s')", false)
	lockPending(t, e, 1)
	transactionEnd(t, a, tx, true)
	lockAwait(t, done, nil)
	sequenceScalar(t, b, "SELECT nextval('s')", 30)
	lockEmpty(t, e)
}

func TestSequenceACLAnyOfAndLateRevocation(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER seq_owner")
	roleExecute(t, e, op, "CREATE USER seq_reader")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_owner")
	roleExecute(t, e, op, "CREATE SEQUENCE s")
	ref := roleTableRef(t, e, "s")
	owner := op.RoleState().CurrentUser
	reader, _ := e.Roles().Lookup("seq_reader")
	grant := func(p authorization.Privileges) {
		t.Helper()
		if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: p}}}); err != nil {
			t.Fatal(err)
		}
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_reader")
	for _, p := range []authorization.Privileges{authorization.UsagePrivilege, authorization.UpdatePrivilege} {
		grant(p)
		r := roleExecute(t, e, op, "SELECT nextval('s')")
		r.Rows.Close()
	}
	for _, tc := range []struct {
		mask    authorization.Privileges
		allowed map[string]bool
	}{
		{authorization.UsagePrivilege, map[string]bool{"SELECT nextval('s')": true, "SELECT currval('s')": true, "SELECT lastval()": true}},
		{authorization.SelectPrivilege, map[string]bool{"SELECT currval('s')": true, "SELECT lastval()": true, "SELECT * FROM s": true}},
		{authorization.UpdatePrivilege, map[string]bool{"SELECT nextval('s')": true, "SELECT setval('s',40)": true}},
		{0, nil},
	} {
		grant(tc.mask)
		for _, q := range []string{"SELECT nextval('s')", "SELECT currval('s')", "SELECT lastval()", "SELECT setval('s',40)", "SELECT * FROM s"} {
			if tc.allowed[q] {
				r := roleExecute(t, e, op, q)
				r.Rows.Close()
			} else {
				roleError(t, e, op, q, "42501", "permission denied for sequence s", "")
			}
		}
	}
	grant(authorization.SelectPrivilege)
	r := roleExecute(t, e, op, "SELECT currval('s')")
	r.Rows.Close()
	roleError(t, e, op, "SELECT nextval('s')", "42501", "permission denied for sequence s", "")
	grant(authorization.UpdatePrivilege)
	r = roleExecute(t, e, op, "SELECT setval('s',40)")
	r.Rows.Close()
	roleError(t, e, op, "SELECT currval('s')", "42501", "permission denied for sequence s", "")
	grant(authorization.UsagePrivilege)
	description, err := e.Describe(op, statement(t, "SELECT nextval('s')"))
	if err != nil {
		t.Fatal(err)
	}
	grant(0)
	if scope, scopeErr := e.StartStatement(op); scopeErr != nil {
		t.Fatal(scopeErr)
	} else {
		scope.Expected = &description
		_, err = scope.Execute(statement(t, "SELECT nextval('s')"), 0, nil)
		err = scope.Finish(err)
		if err == nil {
			t.Fatal("revoked prepared privilege accepted")
		}
	}
}

func TestSequenceRestartLifecycleMatrix(t *testing.T) {
	for _, end := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "failed commit", "physical end", "reset", "restore"} {
		t.Run(end, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE SEQUENCE s")
			if end == "failed commit" {
				sessionSQL(t, a, "CREATE TABLE t(v INT NOT NULL)")
			}
			sequenceScalar(t, b, "SELECT nextval('s')", 1)
			controlsRequire(t, a, "BEGIN")
			sessionSQL(t, a, "ALTER SEQUENCE s RESTART 100")
			queued := lockAsync(e, b, context.Background(), "SELECT nextval('s')", false)
			lockPending(t, e, 1)
			want := int64(2)
			switch end {
			case "COMMIT AND CHAIN":
				controlsRequire(t, a, end)
				want = 100
			case "ROLLBACK AND CHAIN":
				controlsRequire(t, a, end)
			case "failed commit":
				controlsAbort(t, a)
				controlsRequire(t, a, "COMMIT")
			case "physical end":
				a.RequestEnd(CleanClose)
				if err := a.WaitEnd(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "reset":
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "restore":
				if err := e.Restore(context.Background(), strings.NewReader("CREATE SEQUENCE s START 9;")); err != nil {
					t.Fatal(err)
				}
			}
			if end == "reset" || end == "restore" {
				lockAwait(t, queued, ErrCatalogReplaced)
				controlsRequire(t, a, "ROLLBACK")
				if end == "reset" {
					sessionSQL(t, b, "CREATE SEQUENCE s START 9")
				}
				sequenceScalar(t, b, "SELECT nextval('s')", 9)
			} else {
				lockAwait(t, queued, nil)
				sequenceScalar(t, b, "SELECT currval('s')", want)
				if a.TransactionState() == TxActive {
					controlsRequire(t, a, "ROLLBACK")
				}
			}
			lockEmpty(t, e)
		})
	}
}

func TestSequenceRestartBlocksEveryOperation(t *testing.T) {
	for _, q := range []string{"SELECT nextval('s')", "SELECT currval('s')", "SELECT lastval()", "SELECT setval('s',40)"} {
		t.Run(q, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE SEQUENCE s CACHE 7")
			sequenceScalar(t, b, "SELECT nextval('s')", 1)
			tx := transactionBegin(t, a)
			sessionSQL(t, a, "ALTER SEQUENCE s RESTART 100")
			row := sessionSQL(t, b, "SELECT * FROM s")
			row.Rows.Close()
			queued := lockAsync(e, b, context.Background(), q, false)
			lockPending(t, e, 1)
			transactionEnd(t, a, tx, false)
			lockAwait(t, queued, nil)
			lockEmpty(t, e)
		})
	}
}

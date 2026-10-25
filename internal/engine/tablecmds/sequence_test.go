package tablecmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func sequenceScalar(t *testing.T, s *session.Session, q string, want int64) {
	t.Helper()
	r := enginetest.SessionSQL(t, s, q)
	defer r.Rows.Close()
	if got := enginetest.Tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{want}}) {
		t.Fatalf("%s got %v want %d", q, got, want)
	}
}

func TestSequenceAllocationConcurrentSessions(t *testing.T) {
	for _, cache := range []string{"1", "7"} {
		t.Run("cache"+cache, func(t *testing.T) {
			e := engine.New()
			owner := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, owner, "CREATE SEQUENCE s CACHE "+cache)
			const clients = 8
			const each = 40
			sessions := make([]*session.Session, clients)
			for i := range sessions {
				sessions[i] = enginetest.StartSession(t, e, nil)
			}
			values := make(chan int64, clients*each)
			failures := make(chan error, clients)
			var wg sync.WaitGroup
			for _, s := range sessions {
				wg.Add(1)
				go func(s *session.Session) {
					defer wg.Done()
					for range each {
						op, err := s.Acquire(context.Background())
						if err != nil {
							failures <- err
							return
						}
						r, err := e.Execute(op, mustLockStatement("SELECT nextval('s')"), 0, nil)
						if err == nil {
							rows := enginetest.Tuples(r)
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
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE SEQUENCE s CACHE 4")
	sequenceScalar(t, b, "SELECT nextval('s')", 1)
	tx := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "ALTER SEQUENCE s RESTART 20")
	ctx, cancel := context.WithCancel(context.Background())
	done := enginetest.LockAsync(e, b, ctx, "SELECT nextval('s')", false)
	enginetest.LockPending(t, e, 1)
	cancel()
	enginetest.LockAwait(t, done, context.Canceled)
	done = enginetest.LockAsync(e, b, context.Background(), "SELECT nextval('s')", false)
	enginetest.LockPending(t, e, 1)
	enginetest.TransactionEnd(t, a, tx, false)
	enginetest.LockAwait(t, done, nil)
	sequenceScalar(t, b, "SELECT currval('s')", 2)
	tx = enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "ALTER SEQUENCE s RESTART 30")
	done = enginetest.LockAsync(e, b, context.Background(), "SELECT currval('s')", false)
	enginetest.LockPending(t, e, 1)
	enginetest.TransactionEnd(t, a, tx, true)
	enginetest.LockAwait(t, done, nil)
	sequenceScalar(t, b, "SELECT nextval('s')", 30)
	enginetest.LockEmpty(t, e)
}

func TestSequenceACLAnyOfAndLateRevocation(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER seq_owner")
	enginetest.RoleExecute(t, e, op, "CREATE USER seq_reader")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_owner")
	withPublicSchemaUnitOwner(t, e, "seq_owner", func() {
		enginetest.RoleExecute(t, e, op, "CREATE SEQUENCE s")
	})
	ref := enginetest.RoleTableRef(t, e, "s")
	owner := op.RoleState().CurrentUser
	reader, _ := e.Roles().Lookup("seq_reader")
	grant := func(p authorization.Privileges) {
		t.Helper()
		if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: p}}}); err != nil {
			t.Fatal(err)
		}
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_reader")
	for _, p := range []authorization.Privileges{authorization.UsagePrivilege, authorization.UpdatePrivilege} {
		grant(p)
		r := enginetest.RoleExecute(t, e, op, "SELECT nextval('s')")
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
				r := enginetest.RoleExecute(t, e, op, q)
				r.Rows.Close()
			} else {
				enginetest.RoleError(t, e, op, q, "42501", "permission denied for sequence s", "")
			}
		}
	}
	grant(authorization.SelectPrivilege)
	r := enginetest.RoleExecute(t, e, op, "SELECT currval('s')")
	r.Rows.Close()
	enginetest.RoleError(t, e, op, "SELECT nextval('s')", "42501", "permission denied for sequence s", "")
	grant(authorization.UpdatePrivilege)
	r = enginetest.RoleExecute(t, e, op, "SELECT setval('s',40)")
	r.Rows.Close()
	enginetest.RoleError(t, e, op, "SELECT currval('s')", "42501", "permission denied for sequence s", "")
	grant(authorization.UsagePrivilege)
	description, err := e.Describe(op, enginetest.Statement(t, "SELECT nextval('s')"))
	if err != nil {
		t.Fatal(err)
	}
	grant(0)
	if scope, scopeErr := e.StartStatement(op); scopeErr != nil {
		t.Fatal(scopeErr)
	} else {
		scope.Expected = &description
		_, err = scope.Execute(enginetest.Statement(t, "SELECT nextval('s')"), 0, nil)
		err = scope.Finish(err)
		if err == nil {
			t.Fatal("revoked prepared privilege accepted")
		}
	}
}

func TestSequenceRestartLifecycleMatrix(t *testing.T) {
	for _, end := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "failed commit", "physical end", "reset", "restore"} {
		t.Run(end, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE SEQUENCE s")
			if end == "failed commit" {
				enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT NOT NULL)")
			}
			sequenceScalar(t, b, "SELECT nextval('s')", 1)
			controlsRequire(t, a, "BEGIN")
			enginetest.SessionSQL(t, a, "ALTER SEQUENCE s RESTART 100")
			queued := enginetest.LockAsync(e, b, context.Background(), "SELECT nextval('s')", false)
			enginetest.LockPending(t, e, 1)
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
				a.RequestEnd(session.CleanClose)
				if err := a.WaitEnd(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "reset":
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			case "restore":
				if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE SEQUENCE s START 9;")); err != nil {
					t.Fatal(err)
				}
			}
			if end == "reset" || end == "restore" {
				enginetest.LockAwait(t, queued, ids.ErrCatalogReplaced)
				controlsRequire(t, a, "ROLLBACK")
				if end == "reset" {
					enginetest.SessionSQL(t, b, "CREATE SEQUENCE s START 9")
				}
				sequenceScalar(t, b, "SELECT nextval('s')", 9)
			} else {
				enginetest.LockAwait(t, queued, nil)
				sequenceScalar(t, b, "SELECT currval('s')", want)
				if a.TransactionState() == session.TxActive {
					controlsRequire(t, a, "ROLLBACK")
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestSequenceRestartBlocksEveryOperation(t *testing.T) {
	for _, q := range []string{"SELECT nextval('s')", "SELECT currval('s')", "SELECT lastval()", "SELECT setval('s',40)"} {
		t.Run(q, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, a, "CREATE SEQUENCE s CACHE 7")
			sequenceScalar(t, b, "SELECT nextval('s')", 1)
			tx := enginetest.TransactionBegin(t, a)
			enginetest.SessionSQL(t, a, "ALTER SEQUENCE s RESTART 100")
			row := enginetest.SessionSQL(t, b, "SELECT * FROM s")
			row.Rows.Close()
			queued := enginetest.LockAsync(e, b, context.Background(), q, false)
			enginetest.LockPending(t, e, 1)
			enginetest.TransactionEnd(t, a, tx, false)
			enginetest.LockAwait(t, queued, nil)
			enginetest.LockEmpty(t, e)
		})
	}
}

// currvalIs reports whether a session memory holds currval want.
func currvalIs(m *sequence.Memory, want int64) bool {
	v, ok := m.Current()
	return ok && v == want
}

// currvalValid reports whether a session memory holds a currval.
func currvalValid(m *sequence.Memory) bool {
	_, ok := m.Current()
	return ok
}

// currvalValue is the remembered currval value, valid or not.
func currvalValue(m *sequence.Memory) int64 {
	v, _ := m.Current()
	return v
}

// cachedSequencePathIs reports whether the session caches exactly key.
func cachedSequencePathIs(s *session.Session, key sequence.PathKey) bool {
	cached, ok := engine.PayloadOf(s).Sequences.CachedPathKey()
	return ok && cached == key
}

func controlsRequire(t *testing.T, s *session.Session, q string) {
	t.Helper()
	if err := controlsExecute(t, s, q); err != nil {
		t.Fatal(q, err)
	}
}

func controlsAbort(t *testing.T, s *session.Session) {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	_, err := engine.EngineOf(s).Execute(op, enginetest.Statement(t, "INSERT INTO t VALUES(NULL)"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23502" {
		t.Fatal(err)
	}
}

func controlsExecute(t *testing.T, s *session.Session, q string) error {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	r, err := engine.EngineOf(s).Execute(op, enginetest.Statement(t, q), 0, nil)
	if r.Rows != nil {
		r.Rows.Close()
	}
	if err == nil && r.Count != 0 {
		t.Fatal(q, r.Count)
	}
	return err
}

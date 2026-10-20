package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

// The channel closes only after the worker's operation defers have completed.
// Both normal assertions and failure cleanup consume the same owned outcome.
func deleteUsingJoin(t *testing.T, cancel context.CancelFunc, done <-chan deleteOutcome) func() deleteOutcome {
	t.Helper()
	var got deleteOutcome
	received, joined := false, false
	join := func(fatal bool) {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case next, ok := <-done:
				if !ok {
					joined = true
					if !received {
						t.Error("DELETE USING worker exited without an outcome")
					}
					return
				}
				if received {
					if next.result.Rows != nil {
						next.result.Rows.Close()
					}
					t.Error("DELETE USING worker produced more than one outcome")
					continue
				}
				got, received = next, true
			case <-timer.C:
				if fatal {
					t.Fatal("DELETE USING worker did not join")
				}
				t.Error("DELETE USING worker cleanup did not join")
				return
			}
		}
	}
	t.Cleanup(func() {
		cancel()
		if !joined {
			join(false)
		}
		if got.result.Rows != nil {
			got.result.Rows.Close()
		}
	})
	return func() deleteOutcome {
		t.Helper()
		if !joined {
			join(true)
		}
		return got
	}
}

func deleteUsingAsync(t *testing.T, s *Session, ctx context.Context, query string) <-chan deleteOutcome {
	t.Helper()
	ast := statement(t, query)
	done := make(chan deleteOutcome, 1)
	go func() {
		defer close(done)
		op, err := s.Acquire(ctx)
		var result Result
		if err == nil {
			defer op.Done()
			result, err = engineOf(s).Execute(op, ast, 0, nil)
		}
		done <- deleteOutcome{result: result, err: err}
	}()
	return done
}

func TestDeleteUsingSourceAdmissionRefresh(t *testing.T) {
	for _, phase := range []string{"comment", "revoke", "prepare"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			writer := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE USER admission_reader", "CREATE TABLE t(id INT)", "CREATE TABLE s(k INT)", "CREATE TABLE r(k INT)", "INSERT INTO t VALUES(1)", "INSERT INTO s VALUES(1)", "INSERT INTO r VALUES(1)"} {
				sessionSQL(t, peer, q)
			}
			owner, _ := e.Roles().Lookup("postgres")
			actor, _ := e.Roles().Lookup("admission_reader")
			grant := func(name string, mask authorization.Privileges) {
				ref := roleTableRef(t, e, name)
				if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: mask}}}); err != nil {
					t.Fatal(err)
				}
			}
			grant("t", authorization.DeletePrivilege|authorization.SelectPrivilege)
			grant("s", authorization.SelectPrivilege)
			grant("r", authorization.SelectPrivilege)
			sessionSQL(t, writer, "SET SESSION AUTHORIZATION admission_reader")
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "ALTER TABLE r ALTER COLUMN k SET NOT NULL")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			q := "DELETE FROM t USING s JOIN r USING(k) WHERE t.id=s.k"
			var done <-chan deleteOutcome
			if phase == "prepare" {
				ast := statement(t, q)
				prepared := make(chan deleteOutcome, 1)
				done = prepared
				go func() {
					defer close(prepared)
					op, err := writer.Acquire(ctx)
					if err == nil {
						defer op.Done()
						_, err = e.Describe(op, ast)
						op.Done()
					}
					prepared <- deleteOutcome{err: err}
				}()
			} else {
				done = deleteUsingAsync(t, writer, ctx, q)
			}
			await := deleteUsingJoin(t, cancel, done)
			lockPending(t, e, 1)
			target := roleTableRef(t, e, "t")
			source := roleTableRef(t, e, "s")
			blocked := roleTableRef(t, e, "r")
			lockState := e.locks.Inspect()
			var targetRX, sourceAS, sourceWait bool
			for resource, entry := range lockState.Resources {
				if resource.ObjectRef == target && resource.Row == 0 {
					for _, mode := range entry.Holders {
						targetRX = targetRX || mode.Mode&lock.RowExclusive != 0
					}
				}
				if resource.ObjectRef == source && resource.Row == 0 {
					for _, mode := range entry.Holders {
						sourceAS = sourceAS || mode.Mode&lock.AccessShare != 0
					}
				}
				if resource.ObjectRef == blocked && resource.Row == 0 {
					sourceWait = len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessShare
				}
			}
			if !targetRX || !sourceAS || !sourceWait {
				t.Fatal("wrong source admission order/grants", targetRX, sourceAS, sourceWait)
			}
			if phase == "revoke" {
				grant("s", 0)
			} else {
				sessionSQL(t, peer, "COMMENT ON TABLE s IS 'compatible while waiting'")
			}
			transactionEnd(t, holder, tx, false)
			got := await()
			if phase == "revoke" {
				var diagnostic *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table s"}
				if got.result.Count != 0 || got.result.Rows != nil || !errors.As(got.err, &diagnostic) || *diagnostic != want {
					t.Fatal(got, diagnostic)
				}
			} else {
				count := int64(1)
				if phase == "prepare" {
					count = 0
				}
				if got.err != nil || got.result.Count != count || got.result.Rows != nil {
					t.Fatal(got)
				}
			}
			if phase == "comment" {
				transactionRows(t, peer, "SELECT id FROM t")
			} else {
				transactionRows(t, peer, "SELECT id FROM t", 1)
			}
			transactionRows(t, peer, "SELECT k FROM s", 1)
			transactionRows(t, peer, "SELECT k FROM r", 1)
			lockEmpty(t, e)
		})
	}
}

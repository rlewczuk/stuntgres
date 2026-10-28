package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSubscriptionTransitions(t *testing.T) {
	p := subscriptionScenario(t, "sub_transitions")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_transitions CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)", false)
			for _, prepared := range []bool{false, true} {
				subscriptionError(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_transitions ENABLE", "55000", "cannot enable subscription that does not have a slot name", prepared)
				subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_transitions SET (two_phase=true)", prepared)
				subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT subtwophasestate,subslotname FROM pg_subscription WHERE subname='sub_transitions'", []string{"subtwophasestate", "subslotname"}, []string{"CHAR", "NAME"}, [][]any{{"p", nil}}))
				for _, name := range []string{"two_phase", "failover"} {
					subscriptionError(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_transitions SET ("+name+"=false)", "55000", "cannot set option \""+name+"\" for a subscription that does not have a slot name", prepared)
				}
				subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_transitions SKIP (lsn='1/ABC')", prepared)
				subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT subskiplsn FROM pg_subscription WHERE subname='sub_transitions'", []string{"subskiplsn"}, []string{"PG_LSN"}, [][]any{{"1/ABC"}}))
				subscriptionError(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_transitions SKIP (lsn='0/0')", "22023", "invalid WAL location (LSN): 0/0", prepared)
				subscriptionError(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_transitions SKIP (lsn='invalid')", "22P02", `invalid input syntax for type pg_lsn: "invalid"`, prepared)
				subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT subskiplsn FROM pg_subscription WHERE subname='sub_transitions'", []string{"subskiplsn"}, []string{"PG_LSN"}, [][]any{{"1/ABC"}}))
				subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_transitions SKIP (lsn=NONE)", prepared)
			}
			subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION sub_transitions SET (slot_name='transition_slot')", false)
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			subscriptionExec(t, p, b, tx, "ALTER SUBSCRIPTION sub_transitions ENABLE", true)
			subscriptionList(t, p, b, tx, "sub_transitions", "{p}", true)
			// Keep the oracle's desired-enabled state private and immediately disable it;
			// no apply-worker behavior is asserted or required.
			subscriptionExec(t, p, b, tx, "ALTER SUBSCRIPTION sub_transitions DISABLE", true)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			subscriptionDetach(t, p, b, b.DB, "sub_transitions")
		})
	}
}
func TestReferenceSubscriptionTransactionsCancellation(t *testing.T) {
	p := subscriptionScenario(t, "sub_transactions", "sub_tx_created")
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_transactions CONNECTION '' PUBLICATION p WITH (connect=false)", false)
			for _, commit := range []bool{false, true} {
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer tx.Rollback()
					subscriptionExec(t, p, b, tx, "CREATE SUBSCRIPTION sub_tx_created CONNECTION '' PUBLICATION q WITH (connect=false,slot_name=NONE)", true)
					subscriptionExec(t, p, b, tx, "ALTER SUBSCRIPTION sub_transactions SET PUBLICATION private_p WITH (refresh=false)", true)
					subscriptionList(t, p, b, tx, "sub_transactions", "{private_p}", false)
					subscriptionList(t, p, b, b.DB, "sub_transactions", "{p}", false)
					subscriptionExec(t, p, b, tx, "SAVEPOINT sub_before", false)
					subscriptionExec(t, p, b, tx, "ALTER SUBSCRIPTION sub_transactions ADD PUBLICATION rolled_back WITH (refresh=false)", true)
					subscriptionExec(t, p, b, tx, "ROLLBACK TO SAVEPOINT sub_before", false)
					subscriptionList(t, p, b, tx, "sub_transactions", "{private_p}", false)
					if commit {
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
				}()
				if commit {
					subscriptionList(t, p, b, b.DB, "sub_transactions", "{private_p}", false)
					subscriptionExec(t, p, b, b.DB, "DROP SUBSCRIPTION sub_tx_created", false)
				} else {
					subscriptionList(t, p, b, b.DB, "sub_transactions", "{p}", false)
				}
			}
			// A prepared utility resolves current state, not the state at Describe.
			query := "ALTER SUBSCRIPTION sub_transactions SET (binary=true)"
			held, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			subscriptionDetach(t, p, b, b.DB, "sub_transactions")
			step := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic("42704", `subscription "sub_transactions" does not exist`))
			step.ErrorStage = "execute"
			subscriptionRun(t, p, b, fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: query}, step)
			held.Close()
			subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION sub_transactions CONNECTION '' PUBLICATION p WITH (connect=false)", false)
			// These are database/sql admission failures on both adapters, not
			// PostgreSQL server-cancellation diagnostics.
			expired, expire := context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
			defer expire()
			canceledPrepared, cancelPrepared := context.WithCancel(p.Ctx)
			cancelPrepared()
			prepared, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()
			for _, tc := range []struct {
				ctx  context.Context
				want error
			}{{expired, context.DeadlineExceeded}, {canceledPrepared, context.Canceled}} {
				if _, err := prepared.ExecContext(tc.ctx); !errors.Is(err, tc.want) {
					t.Fatalf("prepared pre-admission error: got %v, want %v", err, tc.want)
				}
			}
			prepared.Close()
			subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT subbinary FROM pg_subscription WHERE subname='sub_transactions'", []string{"subbinary"}, []string{"BOOL"}, [][]any{{false}}))
			for _, tc := range []struct{ query, message string }{
				{"CREATE SUBSCRIPTION sub_tx_created CONNECTION '' PUBLICATION p", "CREATE SUBSCRIPTION ... WITH (create_slot = true) cannot run inside a transaction block"},
				{"DROP SUBSCRIPTION sub_transactions", "DROP SUBSCRIPTION cannot run inside a transaction block"},
			} {
				tx, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				subscriptionError(t, p, b, tx, tc.query, "25001", tc.message, true)
				tx.Rollback()
			}
			a, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			waiter, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer waiter.Close()
			holderPID := int(subscriptionNumber(t, p, a, "SELECT pg_backend_pid()"))
			waiterPID := int(subscriptionNumber(t, p, waiter, "SELECT pg_backend_pid()"))
			object := subscriptionNumber(t, p, a, "SELECT oid::bigint FROM pg_subscription WHERE subname='sub_transactions'")
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			subscriptionExec(t, p, b, tx, "ALTER SUBSCRIPTION sub_transactions SET (binary=true)", false)
			blocked, err := waiter.PrepareContext(p.Ctx, "ALTER SUBSCRIPTION sub_transactions SET (binary=false)")
			if err != nil {
				t.Fatal(err)
			}
			defer blocked.Close()
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			finished := make(chan struct{})
			join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(finished)
				result, err := blocked.ExecContext(ctx)
				return fixture.RelationOutcome{Result: result, Err: err}
			})
			fixture.ObjectLockPending(t, p, b, waiterPID, holderPID, 6100, object, "AccessExclusiveLock", finished)
			cancel()
			outcome := join()
			if b.PG {
				want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
				fixture.AssertDiagnostic(t, outcome.Err, &want)
			} else if !errors.Is(outcome.Err, context.Canceled) {
				t.Fatal(outcome.Err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT subbinary FROM pg_subscription WHERE subname='sub_transactions'", []string{"subbinary"}, []string{"BOOL"}, [][]any{{false}}))
			if !b.PG {
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				_, err := b.DB.ExecContext(canceled, "ALTER SUBSCRIPTION sub_transactions DISABLE")
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			blocked.Close()
			waiter.Close()
			a.Close()
			subscriptionDetach(t, p, b, b.DB, "sub_transactions")
		})
	}
}

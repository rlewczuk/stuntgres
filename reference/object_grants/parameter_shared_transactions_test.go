package reference_test

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceParameterACLSharedTransactions(t *testing.T) {
	t.Run("visibility", func(t *testing.T) {
	parameterSharedScenario(t, "transactions", func(t *testing.T, p *fixture.Pair, a, b fixture.Backend) {
		ra, rb := parameterSharedRun(t, p, a), parameterSharedRun(t, p, b)
		c, err := a.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer c.Close()
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, a.Recorder, s) }
		peer := func(want bool) { rb(parameterSharedInquiry("ps_reader", "lc_messages", "SET", want)) }
		own := func(want bool) { run(parameterSharedInquiry("ps_reader", "lc_messages", "SET", want)) }
		run(fixture.Exec("BEGIN ISOLATION LEVEL READ COMMITTED", 0))
		run(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO ps_reader", 0))
		own(true); peer(false)
		run(fixture.Exec("SET LOCAL ROLE ps_reader", 0))
		parameterSharedConsumers(t, p, a, c, true)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SAVEPOINT ps_before_revoke", 0))
		run(fixture.Exec("REVOKE SET ON PARAMETER lc_messages FROM ps_reader", 0))
		own(false); peer(false)
		run(fixture.Exec("ROLLBACK TO SAVEPOINT ps_before_revoke", 0))
		own(true); peer(false)
		run(fixture.Exec("COMMIT", 0)); peer(true)
		run(fixture.Exec("BEGIN ISOLATION LEVEL READ COMMITTED", 0))
		run(fixture.Exec("REVOKE SET ON PARAMETER lc_messages FROM ps_reader", 0))
		own(false); peer(true)
		run(fixture.Exec("ROLLBACK", 0)); peer(true)
		for _, failure := range []struct{ query, code, message string }{
			{"GRANT SET ON PARAMETER ps_atomic.value,no_such_guc TO ps_reader", "42704", `unrecognized configuration parameter "no_such_guc"`},
			{"GRANT SET ON PARAMETER ps_atomic.value TO ps_reader,ps_nonexistent", "42704", `role "ps_nonexistent" does not exist`},
		} {
			run(fixture.Exec("BEGIN", 0)); run(fixture.Exec("SAVEPOINT ps_failure", 0))
			s := fixture.ImplicitError(failure.query, fixture.Diagnostic{Code: failure.code, Message: failure.message})
			s.ErrorStage = "execute"
			run(s)
			peer(true)
			run(fixture.Exec("ROLLBACK TO SAVEPOINT ps_failure", 0))
			run(parameterSharedInquiry("ps_reader", "ps_atomic.value", "SET", false))
			run(fixture.Exec("COMMIT", 0))
			rb(parameterSharedInquiry("ps_reader", "ps_atomic.value", "SET", false))
		}
		ra(fixture.Exec("GRANT SET ON PARAMETER lc_messages TO ps_reader WITH GRANT OPTION", 0))
		delegator := parameterSharedConn(t, p, b, "ps_reader")
		fixture.RunStep(t, p.Ctx, delegator, b.Recorder, fixture.Exec("GRANT SET ON PARAMETER lc_messages TO ps_delegate", 0))
		run(fixture.Exec("BEGIN", 0)); run(fixture.Exec("SAVEPOINT ps_restrict", 0))
		restrict := fixture.ImplicitError("REVOKE GRANT OPTION FOR SET ON PARAMETER lc_messages FROM ps_reader RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."})
		restrict.ErrorStage = "execute"
		run(restrict)
		rb(parameterSharedInquiry("ps_delegate", "lc_messages", "SET", true))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT ps_restrict", 0))
		run(parameterSharedInquiry("ps_reader", "lc_messages", "SET WITH GRANT OPTION", true))
		run(fixture.Exec("COMMIT", 0))
		// Disjoint names are staged simultaneously; reverse publication order may
		// not publish a stale peer root over the other successful commit.
		d, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer d.Close()
		defer d.ExecContext(p.Ctx, "ROLLBACK")
		other := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, d, b.Recorder, s) }
		run(fixture.Exec("BEGIN", 0)); other(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("GRANT SET ON PARAMETER ps_disjoint.first TO ps_reader", 0))
		other(fixture.Exec("GRANT ALTER SYSTEM ON PARAMETER ps_disjoint.second TO ps_delegate", 0))
		other(fixture.Exec("COMMIT", 0)); run(fixture.Exec("COMMIT", 0))
		for _, view := range []fixture.Backend{a, b} {
			check := parameterSharedRun(t, p, view)
			check(parameterSharedInquiry("ps_reader", "ps_disjoint.first", "SET", true))
			check(parameterSharedInquiry("ps_delegate", "ps_disjoint.second", "ALTER SYSTEM", true))
		}
		other(fixture.Exec("BEGIN", 0))
		other(fixture.Exec("CREATE TABLE ps_stale_local (v int)", 0))
		ra(fixture.Exec("GRANT SET ON PARAMETER ps_stale.value TO ps_reader", 0))
		other(fixture.Exec("COMMIT", 0))
		rb(parameterSharedInquiry("ps_reader", "ps_stale.value", "SET", true))
		// Ordinary native context cancellation remains an adapter error; admitted
		// cancellation below additionally proves publication atomicity.
		if !a.PG {
			ctx, cancel := context.WithCancel(p.Ctx); cancel()
			if _, err := c.ExecContext(ctx, "GRANT SET ON PARAMETER ps_canceled.value TO ps_reader"); !errors.Is(err, context.Canceled) { t.Fatal(err) }
			rb(parameterSharedInquiry("ps_reader", "ps_canceled.value", "SET", false))
		}
	})
	})
	parameterSharedConcurrentTransactions(t)
}

// These full conflict outcomes were independently reasoned from catalog tuple/
// unique-index arbitration and resolved against the pinned 18.6 oracle. Each
// backend must expose a real exact-holder wait before release or cancellation.
func parameterSharedConcurrentTransactions(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, cancelWaiter := range []bool{false, true} {
			name := "first_insert"
			if existing { name = "existing_tuple" }
			if cancelWaiter { name += "_cancel" }
			t.Run(name, func(t *testing.T) {
				parameterSharedScenario(t, name, func(t *testing.T, p *fixture.Pair, a, b fixture.Backend) {
					ra := parameterSharedRun(t, p, a)
					const parameter = "ps_conflict.value"
					if existing { ra(fixture.Exec("GRANT SET ON PARAMETER "+parameter+" TO ps_parent", 0)) }
					holder, err := a.DB.Conn(p.Ctx)
					if err != nil { t.Fatal(err) }
					defer holder.Close()
					defer holder.ExecContext(p.Ctx, "ROLLBACK")
					waiter, err := b.DB.Conn(p.Ctx)
					if err != nil { t.Fatal(err) }
					defer waiter.Close()
					holderPID := fixture.RoleTransactionPID(t, p, a, holder)
					waiterPID := fixture.RoleTransactionPID(t, p, b, waiter)
					for _, q := range []string{"BEGIN", "GRANT SET ON PARAMETER "+parameter+" TO ps_reader"} { fixture.RunStep(t, p.Ctx, holder, a.Recorder, fixture.Exec(q, 0)) }
					stmt, err := waiter.PrepareContext(p.Ctx, "GRANT ALTER SYSTEM ON PARAMETER "+parameter+" TO ps_delegate")
					if err != nil { t.Fatal("prepare contention statement", err) }
					defer stmt.Close()
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					done := make(chan struct{})
					var workerErr error
					go func() { defer close(done); _, workerErr = stmt.ExecContext(ctx) }()
					defer func() { cancel(); holder.ExecContext(p.Ctx, "ROLLBACK"); <-done }()
					if a.PG {
						fixture.TransactionLockPending(t, p, waiterPID, holderPID, done)
					} else {
						fixture.RoleTransactionWait(t, p, b, waiterPID, holderPID, done)
					}
					if cancelWaiter { cancel() } else { fixture.RunStep(t, p.Ctx, holder, a.Recorder, fixture.Exec("COMMIT", 0)) }
					<-done
					if cancelWaiter {
						if !a.PG {
							if !errors.Is(workerErr, context.Canceled) { t.Fatal("admitted native cancellation", workerErr) }
						} else if !existing {
							fixture.DefinitionIndexWaitDiagnostic(t, workerErr, "57014", "canceling statement due to user request", "pg_parameter_acl_parname_index", true)
						} else {
							got := fixture.ExtractDiagnostic(t, workerErr)
							if !regexp.MustCompile(`^while updating tuple \([0-9]+,[1-9][0-9]*\) in relation "pg_parameter_acl"$`).MatchString(got.Context) { t.Fatalf("parameter tuple cancellation Context: %q", got.Context) }
							want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
							want.Context = got.Context
							fixture.AssertDiagnostic(t, workerErr, &want)
						}
					} else {
						want := fixture.ControlsReferenceDiagnostic("XX000", "tuple concurrently updated")
						if !existing {
							want = fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "pg_parameter_acl_parname_index"`)
							want.Detail = "Key (parname)=(ps_conflict.value) already exists."
							want.Schema, want.Table, want.Constraint = "pg_catalog", "pg_parameter_acl", "pg_parameter_acl_parname_index"
						}
						fixture.AssertDiagnostic(t, workerErr, &want)
					}
					if cancelWaiter { fixture.RunStep(t, p.Ctx, holder, a.Recorder, fixture.Exec("COMMIT", 0)) }
					for _, view := range []fixture.Backend{a, b} {
						run := parameterSharedRun(t, p, view)
						run(parameterSharedInquiry("ps_reader", parameter, "SET", true))
						run(parameterSharedInquiry("ps_delegate", parameter, "ALTER SYSTEM", false))
					}
				})
			})
		}
	}
}

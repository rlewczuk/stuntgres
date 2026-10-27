package reference_test

import (
	"context"
	"regexp"
	"testing"

	"stuntgres/reference/fixture"
)

// Physical tuple coordinates vary with catalog history; every semantic field is exact.
func rsCatalogUpdateCanceled(t *testing.T, err error, relation string) {
	t.Helper()
	got := fixture.ExtractDiagnostic(t, err)
	pattern := `^while updating tuple \([0-9]+,[1-9][0-9]*\) in relation "` + regexp.QuoteMeta(relation) + `"$`
	if !regexp.MustCompile(pattern).MatchString(got.Context) {
		t.Fatalf("catalog update cancellation Context = %q, want %s", got.Context, pattern)
	}
	want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
	want.Context = got.Context // Only the independently checked heap coordinates vary.
	fixture.AssertDiagnostic(t, err, &want)
}

func rsDatabaseRows(value string, count int64) fixture.Step {
	return fixture.Select("SELECT count(*) AS n FROM pg_db_role_setting s JOIN pg_roles r ON r.oid=s.setrole WHERE r.rolname='rsd_a' AND 'application_name="+value+"'=ANY(s.setconfig)", []string{"n"}, []string{"INT8"}, [][]any{{count}})
}

func TestReferenceRoleSettingsTransactionPublication(t *testing.T) {
	for _, commit := range []bool{false, true} {
		name := "rollback"
		if commit {
			name = "commit"
		}
		t.Run(name, func(t *testing.T) {
			rsDatabaseCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
				owner, _ := fixture.RoleTransactionConn(t, p, b)
				peer, _ := rsDatabasePool(t, p, b, "rsd_two", "")
				run := func(e fixture.Executor, s fixture.Step) { rsDatabaseRun(t, p, b, e, route, s) }
				for _, q := range []string{"ALTER ROLE rsd_a SET application_name='before'", "ALTER ROLE rsd_a IN DATABASE rsd_one SET application_name='before'"} {
					run(owner, fixture.Exec(q, 0))
				}
				run(owner, fixture.Exec("BEGIN", 0))
				for _, q := range []string{"ALTER ROLE rsd_a SET application_name='committed'", "ALTER ROLE rsd_a IN DATABASE rsd_one SET application_name='committed'"} {
					run(owner, fixture.Exec(q, 0))
				}
				run(owner, rsDatabaseRows("committed", 2))
				rsDatabaseRun(t, p, peer, peer.DB, route, rsDatabaseRows("before", 2))
				rsDatabaseRun(t, p, peer, peer.DB, route, rsDatabaseRows("committed", 0))
				run(owner, fixture.Exec("SAVEPOINT suffix", 0))
				run(owner, fixture.Exec("ALTER ROLE rsd_a RESET ALL", 0))
				run(owner, fixture.Exec("ALTER ROLE rsd_a IN DATABASE rsd_one SET application_name='suffix'", 0))
				run(owner, rsDatabaseRows("committed", 0))
				run(owner, fixture.Exec("ROLLBACK TO suffix", 0))
				run(owner, rsDatabaseRows("committed", 2))
				run(owner, fixture.Exec("RELEASE suffix", 0))
				end, want := "ROLLBACK", "before"
				if commit {
					end, want = "COMMIT", "committed"
				}
				run(owner, fixture.Exec(end, 0))
				rsDatabaseRun(t, p, peer, peer.DB, route, rsDatabaseRows(want, 2))
				for _, database := range []string{"rsd_one", "rsd_two"} {
					fresh, _ := rsDatabasePool(t, p, b, database, "rsd_a")
					rsDatabaseRun(t, p, fresh, fresh.DB, route, rsDatabaseApplication(want))
				}
				// Failed suffix aborts without partially replacing either committed row.
				run(owner, fixture.Exec("BEGIN", 0))
				run(owner, fixture.Exec("ALTER ROLE rsd_a SET application_name='unpublished'", 0))
				s := fixture.RoleReferenceError("ALTER ROLE rsd_a IN DATABASE rsd_missing SET application_name='bad'", "3D000", "database \"rsd_missing\" does not exist", "", false, "execute")
				run(owner, s)
				run(owner, fixture.Exec("ROLLBACK", 0))
				rsDatabaseRun(t, p, peer, peer.DB, route, rsDatabaseRows(want, 2))
				rsDatabaseRun(t, p, peer, peer.DB, route, rsDatabaseRows("unpublished", 0))
			})
		})
	}
}

func TestReferenceRoleSettingsTransactionCanceledWrite(t *testing.T) {
	rsDatabaseCases(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, route string) {
		holder, hpid := fixture.RoleTransactionConn(t, p, b)
		waiter, wpid := fixture.RoleTransactionConn(t, p, b)
		run := func(e fixture.Executor, s fixture.Step) { rsDatabaseRun(t, p, b, e, route, s) }
		run(holder, fixture.Exec("ALTER ROLE rsd_a SET application_name='before'", 0))
		run(holder, fixture.Exec("BEGIN", 0))
		run(holder, fixture.Exec("ALTER ROLE rsd_a SET application_name='holder'", 0))
		query := "ALTER ROLE rsd_a SET application_name='canceled'"
		var e fixture.Executor = waiter
		if route != "direct" {
			stmt, err := waiter.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			e = fixture.HeldStatement{Executor: waiter, Statement: stmt, SQL: query}
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		completed := make(chan struct{})
		join := fixture.LockOperation(t, ctx, func(worker context.Context) fixture.RelationOutcome {
			defer close(completed)
			return fixture.RowExecute(worker, e, query, false)
		})
		// A real catalog-row conflict, not a sleep or a worker-start signal.
		fixture.RoleTransactionWait(t, p, b, wpid, hpid, completed)
		if b.PG {
			var canceled bool
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", wpid).Scan(&canceled); err != nil || !canceled {
				t.Fatal(canceled, err)
			}
		} else {
			cancel()
		}
		out := join()
		if b.PG {
			rsCatalogUpdateCanceled(t, out.Err, "pg_db_role_setting")
		} else {
			fixture.TransactionOrdinary(t, out.Err, context.Canceled.Error())
		}
		run(holder, fixture.Exec("ROLLBACK", 0))
		run(holder, rsDatabaseRows("before", 1))
		run(holder, rsDatabaseRows("canceled", 0))
		fresh, _ := rsDatabasePool(t, p, b, "rsd_one", "rsd_a")
		rsDatabaseRun(t, p, fresh, fresh.DB, route, rsDatabaseApplication("before"))
	})
}

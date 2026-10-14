package reference_test

import (
	"context"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDriverBatchRefreshPromotion(t *testing.T) {
	for _, private := range []bool{false, true} {
		for _, finish := range []string{"COMMIT", "ROLLBACK"} {
			t.Run(map[bool]string{false: "existing_", true: "private_"}[private]+finish, func(t *testing.T) {
				batchReviewControlsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
					batchRefreshSeed(t, p, b, f.A)
					name := "batch_refresh_mv"
					prefix := "SELECT i,s FROM batch_refresh_mv; "
					if private {
						name = "batch_refresh_promoted"
						prefix += "CREATE MATERIALIZED VIEW " + name + " AS SELECT i,s FROM batch_refresh_base WITH DATA; "
					}
					four := append(batchRefreshOld(), []any{int64(3), "c"})
					q := prefix + "INSERT INTO batch_refresh_base VALUES(3,'c'); REFRESH MATERIALIZED VIEW " + name + "; SELECT i,s FROM " + name + "; BEGIN; SELECT i,s FROM " + name
					batchSets(t, batchQuery(t, p, f.A, q), batchRefreshSet(batchRefreshOld()...), batchRefreshSet(four...), batchRefreshSet(four...))
					batchRefreshRows(t, p, b, f.B, "batch_refresh_base", batchRefreshOld())
					if private {
						fixture.RunStep(t, p.Ctx, f.B, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='batch_refresh_promoted'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					} else if b.PG {
						batchExec(t, p, b, f.B, "SET lock_timeout='100ms'", 0)
						w := batchDiag("55P03", "canceling statement due to lock timeout")
						w.Position = "17"
						s := fixture.ErrorDiagnostic("SELECT i,s FROM batch_refresh_mv", w)
						s.SelectRows = true
						fixture.RunStep(t, p.Ctx, f.B, b.Recorder, s)
						batchExec(t, p, b, f.B, "RESET lock_timeout", 0)
					} else {
						fixture.ViewNativeBlocked(t, p.Ctx, f.B, "SELECT i,s FROM batch_refresh_mv", false)
					}
					batchExec(t, p, b, f.A, finish, 0)
					want := batchRefreshOld()
					if finish == "COMMIT" {
						want = four
						batchRefreshRows(t, p, b, f.B, name, four)
						batchRefreshPopulation(t, p, b, f.B, name, true)
					} else if private {
						fixture.RunStep(t, p.Ctx, f.B, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='batch_refresh_promoted'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					} else {
						batchRefreshRows(t, p, b, f.B, name, want)
					}
					batchRefreshRows(t, p, b, f.B, "batch_refresh_base", want)
				})
			})
		}
	}
}

func TestReferenceDriverBatchRefreshAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"batch_refresh_actor", "batch_refresh_owner", "batch_refresh_undone"})
	batchReviewControlsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		batchRefreshSeed(t, p, b, f.A)
		if b.PG {
			batchExec(t, p, b, f.A, "GRANT USAGE ON SCHEMA public TO PUBLIC", 0)
		}
		batchExec(t, p, b, f.A, "INSERT INTO batch_refresh_base VALUES(3,'c')", 1)
		four := append(batchRefreshOld(), []any{int64(3), "c"})
		q := "CREATE ROLE batch_refresh_actor; GRANT MAINTAIN,SELECT ON batch_refresh_mv TO batch_refresh_actor; SET ROLE batch_refresh_actor; REFRESH MATERIALIZED VIEW batch_refresh_mv; SELECT CURRENT_USER AS who; SELECT i,s FROM batch_refresh_mv; RESET ROLE"
		batchSets(t, batchQuery(t, p, f.A, q), batchSet{[]string{"who"}, []string{"NAME"}, [][]any{{"batch_refresh_actor"}}, false}, batchRefreshSet(four...))
		batchRefreshRows(t, p, b, f.B, "batch_refresh_mv", four)
		// MAINTAIN grants target access, not caller access to the source. Population
		// succeeds as the MV owner; the caller's own ordinary source consumer fails.
		batchExec(t, p, b, f.A, "SET ROLE batch_refresh_actor", 0)
		batchError(t, p, f.A, "SELECT i FROM batch_refresh_base", batchDiag("42501", "permission denied for table batch_refresh_base"))
		batchExec(t, p, b, f.A, "RESET ROLE", 0)
		batchExec(t, p, b, f.A, "INSERT INTO batch_refresh_base VALUES(4,'d')", 1)
		five := append(append(batchRefreshOld(), []any{int64(3), "c"}), []any{int64(4), "d"})
		// This batch creates its own intended owner. Keep role creation and the
		// actor's CREATE in the same batch, with real scoped schema ownership.
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := admin.Close(); err != nil {
				t.Error("batch public ownership connection close", err)
			}
		}()
		for _, reset := range []string{"RESET SESSION AUTHORIZATION", "RESET ROLE"} {
			batchExec(t, p, b, admin, reset, 0)
		}
		var previous string
		if err := admin.QueryRowContext(p.Ctx, "SELECT r.rolname FROM pg_catalog.pg_namespace n JOIN pg_catalog.pg_roles r ON r.oid=n.nspowner WHERE n.nspname='public'").Scan(&previous); err != nil {
			t.Fatal(err)
		}
		restore := "ALTER SCHEMA public OWNER TO \"" + strings.ReplaceAll(previous, "\"", "\"\"") + "\""
		func() {
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if _, err := admin.ExecContext(ctx, restore); err != nil {
					t.Error("batch public ownership restore", err)
				}
			}()
			q = "CREATE ROLE batch_refresh_owner; ALTER SCHEMA public OWNER TO batch_refresh_owner; GRANT SELECT ON batch_refresh_base TO batch_refresh_owner; SET ROLE batch_refresh_owner; CREATE MATERIALIZED VIEW batch_refresh_owned AS SELECT i,s FROM batch_refresh_base WITH NO DATA; REFRESH MATERIALIZED VIEW batch_refresh_owned WITH DATA; SELECT i,s FROM batch_refresh_owned; RESET ROLE; " + restore
			batchSets(t, batchQuery(t, p, f.A, q), batchRefreshSet(five...))
		}()
		fixture.RunStep(t, p.Ctx, f.B, b.Recorder, fixture.Select("SELECT matviewowner FROM pg_matviews WHERE matviewname='batch_refresh_owned'", []string{"matviewowner"}, []string{"NAME"}, [][]any{{"batch_refresh_owner"}}))
		batchExec(t, p, b, f.A, "INSERT INTO batch_refresh_base VALUES(5,'e')", 1)
		q = "CREATE ROLE batch_refresh_undone; GRANT MAINTAIN,SELECT ON batch_refresh_mv TO batch_refresh_undone; SET ROLE batch_refresh_undone; REFRESH MATERIALIZED VIEW batch_refresh_mv; SELECT i,s FROM batch_refresh_mv; RESET ROLE; SELECT missing FROM batch_refresh_base"
		r := batchQuery(t, p, f.A, q)
		six := append(append(append(batchRefreshOld(), []any{int64(3), "c"}), []any{int64(4), "d"}), []any{int64(5), "e"})
		batchReadSet(t, r, batchRefreshSet(six...))
		if r.NextResultSet() {
			t.Fatal("failed role/refresh suffix returned a result")
		}
		w := batchDiag("42703", `column "missing" does not exist`)
		w.Position = batchPosition(q, "missing")
		fixture.AssertDiagnostic(t, r.Err(), &w)
		r.Close()
		batchRefreshRows(t, p, b, f.B, "batch_refresh_mv", four)
		batchRefreshRows(t, p, b, f.B, "batch_refresh_owned", five)
		fixture.RunStep(t, p.Ctx, f.B, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_roles WHERE rolname='batch_refresh_undone'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		batchRefreshPopulation(t, p, b, f.B, "batch_refresh_mv", true)
	})
}

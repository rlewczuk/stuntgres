package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterStatisticsAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, q := range []string{"CREATE TABLE statistics_t(a integer)", "ALTER TABLE statistics_t ALTER a SET STATISTICS 3, ALTER a SET(n_distinct=2)"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	p.Step(t, fixture.Exec("INSERT INTO statistics_t VALUES(1),(2)", 2))
	for _, q := range []string{
		"ALTER TABLE statistics_t ALTER a SET STATISTICS 7, ALTER missing SET STATISTICS 4",
		"ALTER TABLE statistics_t ALTER a SET STATISTICS 7, ALTER a SET(bogus=1)",
		"ALTER TABLE statistics_t ALTER a SET(n_distinct=4), ALTER a SET(n_distinct=-2)",
	} {
		code, message, detail := "22023", `unrecognized parameter "bogus"`, ""
		if q == "ALTER TABLE statistics_t ALTER a SET STATISTICS 7, ALTER missing SET STATISTICS 4" {
			code, message = "42703", `column "missing" of relation "statistics_t" does not exist`
		}
		if q == "ALTER TABLE statistics_t ALTER a SET(n_distinct=4), ALTER a SET(n_distinct=-2)" {
			message, detail = `value -2 out of bounds for option "n_distinct"`, statisticsBounds
		}
		p.Step(t, fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Detail: detail}))
		p.Step(t, statisticsMetadata("statistics_t", []any{"a", int64(3), "{n_distinct=2}"}))
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("ALTER TABLE statistics_t ALTER a SET STATISTICS 5", 0))
			run(statisticsMetadata("statistics_t", []any{"a", int64(5), "{n_distinct=2}"}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, statisticsMetadata("statistics_t", []any{"a", int64(3), "{n_distinct=2}"}))
			run(fixture.Exec("SAVEPOINT statistics_s", 0))
			run(fixture.Exec("ALTER TABLE statistics_t ALTER a SET(n_distinct=-0.5)", 0))
			run(fixture.Exec("ROLLBACK TO statistics_s", 0))
			run(statisticsMetadata("statistics_t", []any{"a", int64(5), "{n_distinct=2}"}))
			run(fixture.Exec("ROLLBACK", 0))
			run(statisticsMetadata("statistics_t", []any{"a", int64(3), "{n_distinct=2}"}))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("ALTER TABLE statistics_t ALTER a SET STATISTICS 6", 0))
			run(fixture.Exec("COMMIT", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, statisticsMetadata("statistics_t", []any{"a", int64(6), "{n_distinct=2}"}))
			for _, completion := range []string{"ROLLBACK", "COMMIT"} {
				gid := "statistics_prepared_" + completion
				fixture.PreparedReferenceCleanup(t, b, gid)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("ALTER TABLE statistics_t ALTER a SET STATISTICS 8", 0))
				run(fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, statisticsMetadata("statistics_t", []any{"a", int64(6), "{n_distinct=2}"}))
				run(fixture.Exec(completion+" PREPARED '"+gid+"'", 0))
				want := int64(6)
				if completion == "COMMIT" {
					want = 8
				}
				run(statisticsMetadata("statistics_t", []any{"a", want, "{n_distinct=2}"}))
			}
			run(fixture.Select("SELECT a FROM statistics_t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	}
}

func TestReferenceAlterStatisticsAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"statistics_owner", "statistics_reader", "statistics_member"})
	p := fixture.NewScenario(t)
	for _, q := range []string{"CREATE ROLE statistics_owner", "CREATE ROLE statistics_reader", "CREATE ROLE statistics_member", "GRANT statistics_owner TO statistics_member", "CREATE TABLE statistics_t(a integer)", "ALTER TABLE statistics_t OWNER TO statistics_owner", "GRANT SELECT,UPDATE,MAINTAIN ON statistics_t TO statistics_reader"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	// Reach table-owner authorization rather than search_path schema hiding.
	// The oracle fixture recreates public without its default PUBLIC USAGE.
	p.Step(t, fixture.Exec("GRANT USAGE ON SCHEMA public TO statistics_owner,statistics_reader,statistics_member", 0))
	for _, q := range []string{"CREATE TABLE statistics_parent(a integer)", "CREATE TABLE statistics_child() INHERITS(statistics_parent)", "ALTER TABLE statistics_parent ALTER a SET STATISTICS 3", "ALTER TABLE statistics_parent OWNER TO statistics_owner", "ALTER TABLE statistics_child OWNER TO statistics_reader"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("SET ROLE statistics_reader", 0))
			for _, action := range []string{"SET STATISTICS 3", "SET(n_distinct=2)", "RESET(n_distinct)"} {
				run(statisticsError("ALTER TABLE statistics_t ALTER a "+action, "42501", "must be owner of table statistics_t"))
			}
			run(fixture.Exec("RESET ROLE", 0))
			run(statisticsMetadata("statistics_t", []any{"a", nil, nil}))
			for _, role := range []string{"statistics_owner", "statistics_member"} {
				run(fixture.Exec("SET ROLE "+role, 0))
				run(fixture.Exec("ALTER TABLE statistics_t ALTER a SET STATISTICS 4, ALTER a SET(n_distinct=2)", 0))
				run(fixture.Exec("RESET ROLE", 0))
			}
			run(statisticsMetadata("statistics_t", []any{"a", int64(4), "{n_distinct=2}"}))
			run(fixture.Exec("SET ROLE statistics_owner", 0))
			run(statisticsError("ALTER TABLE statistics_parent ALTER a SET STATISTICS 7, ALTER a SET(n_distinct=2)", "42501", "must be owner of table statistics_child"))
			run(fixture.Exec("RESET ROLE", 0))
			run(statisticsMetadata("statistics_parent", []any{"a", int64(3), nil}))
			run(statisticsMetadata("statistics_child", []any{"a", int64(3), nil}))
		})
	}
}

func TestReferenceAlterStatisticsCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE statistics_t(a integer)", 0))
	p.Step(t, fixture.Exec("ALTER TABLE statistics_t ALTER a SET STATISTICS 3, ALTER a SET(n_distinct=2)", 0))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			q := "ALTER TABLE statistics_t ALTER a SET STATISTICS 7, ALTER a SET(n_distinct=4)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			waiterPID, holderPID := 0, 0
			var oid int64
			if b.PG {
				waiterPID = fixture.LockPID(t, p.Ctx, c)
				holderPID = fixture.LockPID(t, p.Ctx, holder)
				oid = fixture.LockOID(t, p.Ctx, "statistics_t")
			}
			block, err := holder.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer block.Rollback()
			fixture.RunStep(t, p.Ctx, block, b.Recorder, fixture.Exec("LOCK TABLE ONLY statistics_t IN SHARE UPDATE EXCLUSIVE MODE", 0))
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			finished := make(chan struct{})
			await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				result, err := stmt.ExecContext(ctx)
				close(finished)
				return fixture.RelationOutcome{Result: result, Err: err}
			})
			fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, "ShareUpdateExclusiveLock", finished)
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, statisticsMetadata("statistics_t", []any{"a", int64(3), "{n_distinct=2}"}))
			if b.PG {
				var ok bool
				if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", waiterPID).Scan(&ok); err != nil || !ok {
					t.Fatal(ok, err)
				}
			} else {
				cancel()
			}
			out := await()
			if out.Result != nil {
				t.Fatal("canceled ALTER returned result")
			}
			if b.PG {
				want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
				fixture.AssertDiagnostic(t, out.Err, &want)
			} else if !errors.Is(out.Err, context.Canceled) {
				t.Fatal(out.Err)
			}
			if err := block.Commit(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, statisticsMetadata("statistics_t", []any{"a", int64(3), "{n_distinct=2}"}))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				var r sql.Result
				r, err = c.ExecContext(ctx, q)
				if r != nil || !errors.Is(err, context.Canceled) {
					t.Fatal(r, err)
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, statisticsMetadata("statistics_t", []any{"a", int64(7), "{n_distinct=4}"}))
		})
	}
}

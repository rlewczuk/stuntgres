package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVacuumFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"vacuum_smoke_actor"})
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, rec, s) }
		run(fixture.Exec("CREATE TABLE vacuum_smoke(id INT,v TEXT)", 0))
		run(fixture.Exec("INSERT INTO vacuum_smoke VALUES(1,'a'),(2,'b'),(3,NULL)", 3))
		run(fixture.Exec("DELETE FROM vacuum_smoke WHERE id=2", 1))
		run(fixture.Exec("VACUUM vacuum_smoke", 0))
		q := "VACUUM (FREEZE,PARALLEL 2) vacuum_smoke"
		h := vacuumHeld(t, p, c, q)
		fixture.RunStep(t, p.Ctx, h, rec, fixture.Exec(q, 0))
		vacuumRows(t, p, b, c, "vacuum_smoke", 1, 3)
		run(fixture.Select("SELECT id,v FROM vacuum_smoke ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), nil}}))
		vacuumCount(t, p.Ctx, c, "vacuum_smoke", 2)
		run(fixture.Select("SELECT reltuples FROM pg_class WHERE oid='vacuum_smoke'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(2)}}))
		run(fixture.Select("SELECT n_ins_since_vacuum FROM pg_stat_all_tables WHERE relid='vacuum_smoke'::regclass", []string{"n_ins_since_vacuum"}, []string{"INT8"}, [][]any{{int64(0)}}))
		vacuumFreezeBound(t, p, b, c, "vacuum_smoke")
		vacuumProgressEmpty(t, p, b, c)
		run(fixture.Exec("BEGIN", 0))
		fixture.RunStep(t, p.Ctx, h, rec, fixture.ImplicitError(q, fixture.Diagnostic{Code: "25001", Message: "VACUUM cannot run inside a transaction block"}))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("CREATE ROLE vacuum_smoke_actor", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO vacuum_smoke_actor", 0))
		run(fixture.Exec("SET ROLE vacuum_smoke_actor", 0))
		notices = nil
		fixture.RunStep(t, p.Ctx, h, rec, fixture.Exec(q, 0))
		vacuumWarning(t, notices, "01000", `permission denied to vacuum "vacuum_smoke", skipping it`)
		run(fixture.Exec("RESET ROLE", 0))
		vacuumCount(t, p.Ctx, c, "vacuum_smoke", 2)
		run(fixture.Exec("GRANT MAINTAIN ON vacuum_smoke TO vacuum_smoke_actor", 0))
		run(fixture.Exec("SET ROLE vacuum_smoke_actor", 0))
		notices = nil
		fixture.RunStep(t, p.Ctx, h, rec, fixture.Exec(q, 0))
		if len(notices) != 0 {
			t.Fatalf("granted smoke warnings %#v", notices)
		}
		run(fixture.Exec("RESET ROLE", 0))
		vacuumCount(t, p.Ctx, c, "vacuum_smoke", 3)
		// Observe a real queued command before cancellation. A fast pq utility
		// can complete despite a pre-canceled context; that race is not proof.
		holder := vacuumConn(t, p, b)
		vacuumRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
		defer holder.ExecContext(p.Ctx, "ROLLBACK")
		vacuumRun(t, p, b, holder, fixture.Exec("LOCK TABLE vacuum_smoke IN SHARE MODE", 0))
		fixture.RelationLockActiveCancel(t, p, b, c, q, func(ctx context.Context) fixture.RelationOutcome {
			result, err := h.Statement.ExecContext(ctx)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		vacuumRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
		vacuumCount(t, p.Ctx, c, "vacuum_smoke", 3)
		vacuumRows(t, p, b, c, "vacuum_smoke", 1, 3)
		fixture.RunStep(t, p.Ctx, h, rec, fixture.Exec(q, 0))
		vacuumCount(t, p.Ctx, c, "vacuum_smoke", 4)
		vacuumProgressEmpty(t, p, b, c)
		if !b.PG {
			// Permission coverage deliberately leaves an explicit ACL behind.
			// Exercise logical replay on independent ACL-free state; do not
			// silently expand Dump's separate ACL serialization boundary.
			controls := fixture.NewFixture(t, false, true)
			controlBackend := controls.Backends()[0]
			controlConn := vacuumConn(t, controls, controlBackend)
			controlRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, controlConn, controlBackend.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE vacuum_smoke(id INT,v TEXT)", 0),
				fixture.Exec("INSERT INTO vacuum_smoke VALUES(1,'a'),(2,'b'),(3,NULL)", 3),
				fixture.Exec("DELETE FROM vacuum_smoke WHERE id=2", 1),
				fixture.Exec("VACUUM (FREEZE) vacuum_smoke", 0),
			} {
				controlRun(s)
			}
			owned, err := controlConn.QueryContext(p.Ctx, "SELECT id FROM vacuum_smoke ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer owned.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, controls.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, controls.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, controls.Native, &dump); err != nil {
				t.Fatal(err)
			}
			controlRun(fixture.Exec("VACUUM (FREEZE) vacuum_smoke", 0))
			vacuumRows(t, p, controlBackend, controlConn, "vacuum_smoke", 1, 3)
			controlRun(fixture.Select("SELECT id,v FROM vacuum_smoke ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), nil}}))
			for _, want := range []int{1, 3} {
				var got int
				if !owned.Next() {
					t.Fatal("smoke control lost owned result")
				}
				if err := owned.Scan(&got); err != nil || got != want {
					t.Fatalf("owned smoke %d err=%v want%d", got, err, want)
				}
			}
			if owned.Next() || owned.Err() != nil {
				t.Fatal("owned smoke tail", owned.Err())
			}
		}
	})
}

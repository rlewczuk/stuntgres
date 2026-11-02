package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckGINControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"gin_control_reader"})
	fixture.SchemaReferenceCleanup(t, "gin_tools", "gin_moved")
	heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE SCHEMA gin_tools", "CREATE EXTENSION amcheck SCHEMA gin_tools", "CREATE TABLE gin_control(n integer,a integer[],v tsvector)", "CREATE INDEX gin_control_i ON gin_control USING gin(a,v)", "CREATE VIEW gin_verified AS SELECT gin_tools.gin_index_check('gin_control_i') IS NULL AS is_null", "CREATE ROLE gin_control_reader", "GRANT USAGE ON SCHEMA public,gin_tools TO gin_control_reader", "GRANT EXECUTE ON FUNCTION gin_tools.gin_index_check(regclass) TO gin_control_reader WITH GRANT OPTION"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO gin_control VALUES(1,'{1,1,2}','cat dog'),(2,'{}',''),(3,NULL,NULL)", 3))
		const q = "SELECT gin_tools.gin_index_check($1::regclass) AS ok"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		check := func() {
			t.Helper()
			fixture.RunStep(t, p.Ctx, held, b.Recorder, amcheckSelect(q, false, "public.gin_control_i"))
			heapBoth(t, p, b, c, fixture.Select("SELECT is_null FROM gin_verified", []string{"is_null"}, []string{"BOOL"}, [][]any{{false}}))
			heapBoth(t, p, b, c, fixture.Select("SELECT n FROM gin_control ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
		}
		check()
		if !b.PG {
			var oldOID int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'gin_control_i'::regclass::oid::bigint").Scan(&oldOID); err != nil {
				t.Fatal(err)
			}
			old, err := c.QueryContext(p.Ctx, "SELECT n FROM gin_control ORDER BY n")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			// Retained TEMP authority forces a destination allocation beyond the old
			// persistent identity; equal public values alone do not prove reminting.
			run(fixture.Exec("CREATE TEMP TABLE gin_keep(n integer)", 0))
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			var newOID int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'gin_control_i'::regclass::oid::bigint").Scan(&newOID); err != nil {
				t.Fatal(err)
			}
			if newOID == oldOID {
				t.Fatal("control fixture did not distinguish reminted index identity")
			}
			fixture.DeleteReferenceConsume(t, false, old, nil, fixture.Select("SELECT n FROM gin_control ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
			check()
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"\nCREATE TABLE broken (")); err == nil {
				t.Fatal("malformed replay published")
			}
			check()
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/isolated")
			if err != nil {
				t.Fatal(err)
			}
			isolated := sql.OpenDB(connector)
			defer isolated.Close()
			var count int64
			if err := isolated.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_extension WHERE extname='amcheck'").Scan(&count); err != nil || count != 0 {
				t.Fatal("independent driver inherited installation", count, err)
			}
		}
		check()
		run(fixture.Exec("SET ROLE gin_control_reader", 0))
		heapBoth(t, p, b, c, amcheckSelect("SELECT gin_tools.gin_index_check('public.gin_control_i') AS ok", false))
		heapBoth(t, p, b, c, heapError("SELECT a FROM gin_control", heapDiagnostic("42501", "permission denied for table gin_control")))
		run(fixture.Exec("RESET ROLE", 0))
		for _, dml := range []string{"INSERT INTO gin_control VALUES(4,'{9,9}','cat')", "UPDATE gin_control SET a='{7}',v='dog' WHERE n=4", "DELETE FROM gin_control WHERE n=4"} {
			run(fixture.Exec(dml, 1))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, amcheckSelect(q, false, "gin_control_i"))
		}
		run(fixture.Exec("ALTER FUNCTION gin_tools.gin_index_check(regclass) RENAME TO gin_installed", 0))
		run(fixture.Exec("CREATE SCHEMA gin_moved", 0))
		run(fixture.Exec("ALTER EXTENSION amcheck SET SCHEMA gin_moved", 0))
		heapBoth(t, p, b, c, amcheckSelect("SELECT gin_moved.gin_installed('gin_control_i') AS ok", false))
		heapBoth(t, p, b, c, fixture.Select("SELECT is_null FROM gin_verified", []string{"is_null"}, []string{"BOOL"}, [][]any{{false}}))
		run(fixture.Exec("DROP VIEW gin_verified", 0))
		run(fixture.Exec("DROP EXTENSION amcheck", 0))
		run(fixture.Exec("CREATE FUNCTION gin_moved.gin_installed(regclass) RETURNS integer LANGUAGE SQL AS $$ SELECT 42 $$", 0))
		heapBoth(t, p, b, c, fixture.Select("SELECT gin_moved.gin_installed('gin_control_i') AS ordinary", []string{"ordinary"}, []string{"INT4"}, [][]any{{int64(42)}}))
		run(fixture.Exec("CREATE EXTENSION amcheck SCHEMA gin_tools", 0))
		heapBoth(t, p, b, c, amcheckSelect("SELECT gin_tools.gin_index_check('gin_control_i') AS ok", false))
	})
}

func TestReferenceAmcheckGINFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"gin_smoke_reader"})
	p := fixture.NewScenario(t)
	ginAmcheckSetup(t, p)
	for _, q := range []string{"CREATE ROLE gin_smoke_reader", "GRANT USAGE ON SCHEMA public TO gin_smoke_reader"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	p.Step(t, amcheckSelect("SELECT gin_index_check(index => 'am_idx') AS ok", false))
	p.Step(t, fixture.Prepared(amcheckSelect("SELECT gin_index_check($1::regclass) AS ok", true, nil)))
	d := heapDiagnostic("0A000", `expected "gin" index as targets for verification`)
	d.Detail = `Relation "am_bt" is a btree index.`
	p.Step(t, heapError("SELECT gin_index_check('am_bt')", d))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("SET ROLE gin_smoke_reader", 0))
			run(heapError("SELECT gin_index_check('am_idx')", heapDiagnostic("42501", "permission denied for function gin_index_check")))
			run(amcheckSelect("SELECT gin_index_check(NULL::regclass) AS ok", true))
			run(fixture.Exec("RESET ROLE", 0))
			ginAmcheckLockRound(t, p, b, false)
			a, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
			defer a.Close()
			defer waiter.Close()
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE am_heap IN ACCESS EXCLUSIVE MODE", 0)
			fixture.RelationLockActiveCancel(t, p, b, waiter, "SELECT gin_index_check('am_idx')", func(ctx context.Context) fixture.RelationOutcome {
				r, err := waiter.ExecContext(ctx, "SELECT gin_index_check('am_idx')")
				return fixture.RelationOutcome{Result: r, Err: err}
			})
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			for _, q := range []string{"INSERT INTO am_heap VALUES(4,'{8,8}','cat')", "UPDATE am_heap SET a='{9}',v='dog' WHERE n=4", "DELETE FROM am_heap WHERE n=4"} {
				run(fixture.Exec(q, 1))
				run(amcheckSelect("SELECT gin_index_check('am_idx') AS ok", false))
			}
		})
	}
}

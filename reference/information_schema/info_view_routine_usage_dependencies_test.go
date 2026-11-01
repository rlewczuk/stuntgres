package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewRoutineUsageDependencies(t *testing.T) {
	vruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vruSeed(run)
		// Retire non-table objects explicitly before the shared schema cleanup.
		// In particular, schema retirement does not own procedure teardown.
		t.Cleanup(func() {
			for _, q := range []string{
				"DROP VIEW IF EXISTS vru_scope.rule_v CASCADE",
				"DROP MATERIALIZED VIEW IF EXISTS vru_scope.materialized_v CASCADE",
				"DROP OPERATOR IF EXISTS vru_scope.## (integer,integer) CASCADE",
				"DROP AGGREGATE IF EXISTS vru_scope.agg(integer) CASCADE",
				"DROP PROCEDURE IF EXISTS vru_scope.p() CASCADE",
			} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Errorf("cleanup %s: %v", q, err)
				}
			}
		})
		for _, q := range []string{
			"CREATE TABLE vru_scope.t(a integer)",
			"CREATE FUNCTION vru_scope.step(integer,integer) RETURNS integer RETURN $1+$2",
			"CREATE AGGREGATE vru_scope.agg(integer)(SFUNC=vru_scope.step,STYPE=integer,INITCOND='0')",
			"CREATE FUNCTION vru_scope.op(integer,integer) RETURNS integer RETURN $1+$2",
			"CREATE OPERATOR vru_scope.## (LEFTARG=integer,RIGHTARG=integer,FUNCTION=vru_scope.op)",
			"CREATE PROCEDURE vru_scope.p() BEGIN ATOMIC SELECT 1; END",
		} {
			run(fixture.Exec(q, 0))
		}
		// These are prescribed direct targets. Neither usage rows nor pg_depend
		// determines the expected bag; pg_proc resolves only opaque identities.
		targets := []struct{ name, inputs string }{{"f", ""}, {"g", ""}, {"null_f", ""}, {"overloaded", "integer"}, {"overloaded", "bigint"}, {"step", "integer,integer"}, {"agg", "integer"}, {"op", "integer,integer"}, {"p", ""}}
		ids := map[string]string{}
		for _, target := range targets {
			ids[target.name+"("+target.inputs+")"] = rsuIdentity(t, p, c, "vru_scope", target.name, target.inputs)
		}
		tests := []struct {
			name, sql string
			targets   []string
		}{
			{"repeated", "SELECT vru_scope.f()+vru_scope.f() AS x,vru_scope.overloaded(1) AS y,vru_scope.overloaded(1::bigint) AS z", []string{"f()", "overloaded(integer)", "overloaded(bigint)"}},
			{"second", "SELECT vru_scope.f() AS x", []string{"f()"}},
			{"outer_v", "SELECT x FROM vru_scope.v", nil},
			{"dead", "SELECT CASE WHEN FALSE THEN vru_scope.f() ELSE 1 END AS x", []string{"f()"}},
			{"derived", "SELECT q.x FROM (SELECT vru_scope.f() AS x) q", []string{"f()"}},
			{"scalar", "SELECT (SELECT vru_scope.f()) AS x", []string{"f()"}},
			{"predicate", "SELECT a FROM vru_scope.t WHERE a=vru_scope.f()", []string{"f()"}},
			{"join_v", "SELECT x.a FROM vru_scope.t x JOIN vru_scope.t y ON x.a=y.a+vru_scope.f()", []string{"f()"}},
			{"group_v", "SELECT vru_scope.f() AS x,count(*) AS n FROM vru_scope.t GROUP BY vru_scope.f() HAVING vru_scope.f()>0 ORDER BY vru_scope.f()", []string{"f()"}},
			{"group_only", "SELECT count(*) AS n FROM vru_scope.t GROUP BY vru_scope.f()", []string{"f()"}},
			{"having_only", "SELECT a FROM vru_scope.t GROUP BY a HAVING vru_scope.g()>0", []string{"g()"}},
			{"order_only", "SELECT a FROM vru_scope.t ORDER BY vru_scope.f()", []string{"f()"}},
			{"null_call", "SELECT vru_scope.null_f() AS x", []string{"null_f()"}},
			{"constant", "SELECT 1 AS x,NULL::integer AS y,'vru_scope.f()'::text AS z,abs(-1) AS a", nil},
			{"regprocedure_v", "SELECT 'vru_scope.f()'::regprocedure AS x", []string{"f()"}},
			{"regproc_v", "SELECT 'vru_scope.f'::regproc AS x", []string{"f()"}},
			{"procedure_v", "SELECT 'vru_scope.p()'::regprocedure AS x", []string{"p()"}},
			{"aggregate_v", "SELECT vru_scope.agg(a) AS x FROM vru_scope.t", []string{"agg(integer)"}},
			{"aggregate_explicit", "SELECT vru_scope.agg(a)+vru_scope.step(1,2) AS x FROM vru_scope.t", []string{"agg(integer)", "step(integer,integer)"}},
			{"operator_v", "SELECT a OPERATOR(vru_scope.##) 1 AS x FROM vru_scope.t", nil},
			{"operator_explicit", "SELECT (a OPERATOR(vru_scope.##) 1)+vru_scope.op(1,2) AS x FROM vru_scope.t", []string{"op(integer,integer)"}},
			{"rule_v", "SELECT 1 AS x", nil},
		}
		want := vruSeedRows(t, p, c, database)
		var edges [][]any
		edges = append(edges, []any{"v", "f"})
		for _, tc := range tests {
			run(fixture.Exec("CREATE VIEW vru_scope."+tc.name+" AS "+tc.sql, 0))
			for _, target := range tc.targets {
				want = append(want, vruRow(database, "vru_scope", tc.name, "vru_scope", ids[target]))
				// The secondary edge projection intentionally retains overload
				// multiplicity, while the primary bag uses distinct identities.
				name := target[:strings.IndexByte(target, '(')]
				edges = append(edges, []any{tc.name, name})
			}
		}
		run(fixture.Exec("CREATE MATERIALIZED VIEW vru_scope.materialized_v AS SELECT vru_scope.f() AS x WITH NO DATA", 0))
		run(fixture.Exec("CREATE TABLE vru_scope.sink(x integer)", 0))
		run(fixture.Exec("CREATE RULE vru_extra_insert AS ON INSERT TO vru_scope.rule_v DO INSTEAD INSERT INTO vru_scope.sink VALUES(vru_scope.g())", 0))
		s := vruProjection(b.PG, "table_schema='vru_scope'", want)
		run(s)
		run(fixture.Prepared(s))
		vruHeld(t, p, c, r, s.Query)(s)
		control := fixture.Select(`SELECT v.relname,p.proname FROM pg_catalog.pg_class v JOIN pg_catalog.pg_rewrite r ON r.ev_class=v.oid JOIN pg_catalog.pg_depend d ON d.objid=r.oid JOIN pg_catalog.pg_proc p ON p.oid=d.refobjid WHERE v.relnamespace='vru_scope'::regnamespace AND v.relkind='v' AND r.rulename='_RETURN' AND d.classid='pg_catalog.pg_rewrite'::regclass AND d.refclassid='pg_catalog.pg_proc'::regclass`, []string{"relname", "proname"}, []string{"NAME", "NAME"}, edges)
		control.Bag = true
		run(control)
		run(fixture.Prepared(control))
		// pg_catalog is not a blanket exclusion: this real, unpinned object
		// must appear, unlike abs above. Cleanup is registered before creation.
		t.Cleanup(func() {
			if _, err := c.ExecContext(p.Ctx, "DROP FUNCTION IF EXISTS pg_catalog.vru_unpinned() CASCADE"); err != nil {
				t.Error(err)
			}
		})
		run(fixture.Exec("CREATE FUNCTION pg_catalog.vru_unpinned() RETURNS integer RETURN 5", 0))
		run(fixture.Exec("CREATE VIEW vru_scope.catalog_v AS SELECT pg_catalog.vru_unpinned() AS x", 0))
		want = append(want, vruRow(database, "vru_scope", "catalog_v", "pg_catalog", rsuIdentity(t, p, c, "pg_catalog", "vru_unpinned", "")))
		run(vruProjection(b.PG, "table_schema='vru_scope'", want))
	})
}

func TestReferenceInfoViewRoutineUsageTemporary(t *testing.T) {
	vruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vruSeed(run)
		run(fixture.Exec("CREATE ROLE vru_a", 0))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer func() {
			_, err := peer.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION; DROP VIEW IF EXISTS pg_temp.vru_peer")
			if err != nil {
				t.Error(err)
			}
		}()
		peerRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, peer, b.Recorder, s) }
		peerRun(fixture.Exec("GRANT USAGE ON SCHEMA vru_scope TO vru_a", 0))
		peerRun(fixture.Exec("SET SESSION AUTHORIZATION vru_a", 0))
		peerRun(fixture.Exec("CREATE TEMP VIEW vru_peer AS SELECT vru_scope.f() AS x", 0))
		var schema string
		if err := peer.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='vru_peer'::regclass").Scan(&schema); err != nil {
			t.Fatal(err)
		}
		want := [][]any{vruRow(database, schema, "vru_peer", "vru_scope", rsuIdentity(t, p, c, "vru_scope", "f", ""))}
		s := vruProjection(b.PG, "table_name='vru_peer'", want)
		run(s)
		run(fixture.Prepared(s))
		vruHeld(t, p, c, r, s.Query)(s)
		peerRun(vruProjection(b.PG, "table_name='vru_peer'", nil))
	})
}

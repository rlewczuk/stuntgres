package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewTableUsageDependencies(t *testing.T) {
	vtuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vtuSeed(run)
		// Retire this scenario's extra dependency kinds before the shared
		// schema cleanup; native DROP SCHEMA does not admit SQL routines.
		t.Cleanup(func() {
			for _, q := range []string{
				"DROP RULE IF EXISTS extra_insert ON vtu_scope.v",
				"DROP VIEW IF EXISTS vtu_scope.routine_v",
				"DROP FUNCTION IF EXISTS vtu_scope.f()",
				"DROP VIEW IF EXISTS vtu_scope.self_v",
				"DROP VIEW IF EXISTS vtu_scope.recursive_v",
				"DROP VIEW IF EXISTS vtu_scope.foreign_v",
				"DROP FOREIGN TABLE IF EXISTS vtu_scope.ft",
				"DROP SERVER IF EXISTS vtu_server",
				"DROP FOREIGN DATA WRAPPER IF EXISTS vtu_fdw",
			} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Errorf("cleanup %s: %v", q, err)
				}
			}
		})
		run(fixture.Exec("CREATE FOREIGN DATA WRAPPER vtu_fdw NO HANDLER NO VALIDATOR", 0))
		for _, q := range []string{
			"CREATE SCHEMA vtu_other", "CREATE TABLE vtu_other.t(a integer)",
			"CREATE VIEW vtu_scope.same_name AS SELECT x.a FROM vtu_scope.t x JOIN vtu_other.t y ON x.a=y.a",
			"CREATE SEQUENCE vtu_scope.s",
			"CREATE VIEW vtu_scope.sequence_v AS SELECT nextval('vtu_scope.s'::regclass) AS n",
			"CREATE VIEW vtu_scope.sequence_source AS SELECT last_value FROM vtu_scope.s",
			"CREATE MATERIALIZED VIEW vtu_scope.mv AS SELECT a FROM vtu_scope.t WITH NO DATA",
			"CREATE VIEW vtu_scope.materialized_v AS SELECT a FROM vtu_scope.mv",
			"CREATE TABLE vtu_scope.part(a integer) PARTITION BY RANGE(a)",
			"CREATE TABLE vtu_scope.leaf PARTITION OF vtu_scope.part FOR VALUES FROM(0) TO(10)",
			"CREATE VIEW vtu_scope.partition_v AS SELECT a FROM vtu_scope.part",
			"CREATE SERVER vtu_server FOREIGN DATA WRAPPER vtu_fdw",
			"CREATE FOREIGN TABLE vtu_scope.ft(a integer) SERVER vtu_server",
			"CREATE VIEW vtu_scope.foreign_v AS SELECT a FROM vtu_scope.ft",
			"CREATE VIEW vtu_scope.self_v AS SELECT a FROM vtu_scope.t",
			"CREATE OR REPLACE VIEW vtu_scope.self_v AS SELECT a FROM vtu_scope.self_v",
			"CREATE VIEW vtu_scope.text_v AS SELECT 'vtu_scope.t'::text AS name",
			"CREATE FUNCTION vtu_scope.f() RETURNS integer BEGIN ATOMIC SELECT a FROM vtu_other.t; END",
			"CREATE VIEW vtu_scope.routine_v AS SELECT vtu_scope.f() AS a",
			"CREATE VIEW vtu_scope.regclass_v AS SELECT 'vtu_scope.u'::regclass AS r",
			"CREATE VIEW vtu_scope.unexecuted_v AS SELECT 1/0 AS a FROM vtu_scope.t",
			"CREATE TABLE vtu_scope.sink(a integer,b integer,again integer)",
			"CREATE RULE extra_insert AS ON INSERT TO vtu_scope.v DO INSTEAD INSERT INTO vtu_scope.sink VALUES(NEW.a,NEW.b,NEW.again)",
			"CREATE RECURSIVE VIEW vtu_scope.recursive_v(a) AS SELECT a FROM vtu_scope.t UNION SELECT a FROM recursive_v",
		} {
			run(fixture.Exec(q, 0))
		}
		want := append(vtuSeedRows(database),
			vtuRow(database, "vtu_scope", "same_name", "vtu_scope", "t"),
			vtuRow(database, "vtu_scope", "same_name", "vtu_other", "t"),
			vtuRow(database, "vtu_scope", "partition_v", "vtu_scope", "part"),
			vtuRow(database, "vtu_scope", "foreign_v", "vtu_scope", "ft"),
			vtuRow(database, "vtu_scope", "regclass_v", "vtu_scope", "u"),
			vtuRow(database, "vtu_scope", "unexecuted_v", "vtu_scope", "t"),
			vtuRow(database, "vtu_scope", "recursive_v", "vtu_scope", "t"))
		s := vtuProjection(b.PG, "view_schema='vtu_scope'", want)
		run(s)
		run(fixture.Prepared(s))

		// Bootstrap tables are pinned, but system views were created after
		// pinning ended. These independently expected edges decide the seam.
		run(fixture.Exec("CREATE VIEW vtu_scope.sys_v AS SELECT viewname FROM pg_catalog.pg_views", 0))
		run(fixture.Exec("CREATE VIEW vtu_scope.sys_t AS SELECT relname FROM pg_catalog.pg_class", 0))
		s = vtuProjection(b.PG, "view_schema='vtu_scope' AND view_name IN ('sys_v','sys_t')", [][]any{vtuRow(database, "vtu_scope", "sys_v", "pg_catalog", "pg_views")})
		run(s)
		run(fixture.Prepared(s))
		edges := fixture.Select(`SELECT DISTINCT v.relname AS view_name,t.relname AS table_name FROM pg_catalog.pg_rewrite r JOIN pg_catalog.pg_class v ON v.oid=r.ev_class JOIN pg_catalog.pg_depend d ON d.classid='pg_catalog.pg_rewrite'::regclass AND d.objid=r.oid JOIN pg_catalog.pg_class t ON d.refclassid='pg_catalog.pg_class'::regclass AND d.refobjid=t.oid WHERE r.rulename='_RETURN' AND v.relnamespace='vtu_scope'::regnamespace AND v.relname IN ('sys_v','sys_t') AND t.oid IN ('pg_catalog.pg_views'::regclass,'pg_catalog.pg_class'::regclass)`, []string{"view_name", "table_name"}, []string{"NAME", "NAME"}, [][]any{{"sys_v", "pg_views"}})
		edges.Bag = true
		run(edges)
		run(fixture.Prepared(edges))

		// Own TEMP identity is the actual generated namespace, not pg_temp.
		run(fixture.Exec("CREATE TEMP TABLE vtu_temp_t(a integer)", 0))
		run(fixture.Exec("CREATE TEMP VIEW vtu_temp_v AS SELECT a FROM vtu_temp_t", 0))
		var tempSchema string
		if err := c.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='vtu_temp_t'::regclass").Scan(&tempSchema); err != nil {
			t.Fatal(err)
		}
		s = vtuProjection(b.PG, "view_name='vtu_temp_v' AND view_schema='"+tempSchema+"'", [][]any{vtuRow(database, tempSchema, "vtu_temp_v", tempSchema, "vtu_temp_t")})
		run(s)
		run(fixture.Prepared(s))
	})
}

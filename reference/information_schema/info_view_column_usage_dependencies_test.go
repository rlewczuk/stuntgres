package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewColumnUsageDependencies(t *testing.T) {
	vcuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vcuSeed(run)
		// SQL routines and foreign objects need explicit retirement before the
		// shared schema cleanup; never invoke a foreign handler to inquire.
		t.Cleanup(func() {
			for _, q := range []string{
				"DROP RULE IF EXISTS extra_insert ON vcu_scope.v",
				"DROP VIEW IF EXISTS vcu_scope.routine_v",
				"DROP FUNCTION IF EXISTS vcu_scope.f()",
				"DROP VIEW IF EXISTS vcu_scope.self_v",
				"DROP VIEW IF EXISTS vcu_scope.recursive_v",
				"DROP VIEW IF EXISTS vcu_scope.foreign_v",
				"DROP FOREIGN TABLE IF EXISTS vcu_scope.ft",
				"DROP SERVER IF EXISTS vcu_server",
				"DROP FOREIGN DATA WRAPPER IF EXISTS vcu_fdw",
			} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Errorf("cleanup %s: %v", q, err)
				}
			}
		})
		for _, q := range []string{
			"CREATE SCHEMA vcu_other", "CREATE TABLE vcu_other.t(a integer)",
			"CREATE VIEW vcu_scope.same_name AS SELECT x.a FROM vcu_scope.t x JOIN vcu_other.t y ON x.a=y.a",
			"CREATE SEQUENCE vcu_scope.s",
			"CREATE VIEW vcu_scope.sequence_v AS SELECT nextval('vcu_scope.s'::regclass) AS n",
			"CREATE VIEW vcu_scope.sequence_source AS SELECT last_value FROM vcu_scope.s",
			"CREATE MATERIALIZED VIEW vcu_scope.mv AS SELECT a FROM vcu_scope.t WITH NO DATA",
			"CREATE VIEW vcu_scope.materialized_v AS SELECT a FROM vcu_scope.mv",
			"CREATE TABLE vcu_scope.part(a integer) PARTITION BY RANGE(a)",
			"CREATE TABLE vcu_scope.leaf PARTITION OF vcu_scope.part FOR VALUES FROM(0) TO(10)",
			"CREATE VIEW vcu_scope.partition_v AS SELECT a FROM vcu_scope.part",
			"CREATE FOREIGN DATA WRAPPER vcu_fdw NO HANDLER NO VALIDATOR",
			"CREATE SERVER vcu_server FOREIGN DATA WRAPPER vcu_fdw",
			"CREATE FOREIGN TABLE vcu_scope.ft(a integer) SERVER vcu_server",
			"CREATE VIEW vcu_scope.foreign_v AS SELECT a FROM vcu_scope.ft",
			"CREATE VIEW vcu_scope.self_v AS SELECT a FROM vcu_scope.t",
			"CREATE OR REPLACE VIEW vcu_scope.self_v AS SELECT a FROM vcu_scope.self_v",
			"CREATE RECURSIVE VIEW vcu_scope.recursive_v(a) AS SELECT a FROM vcu_scope.t UNION SELECT a FROM recursive_v",
			"CREATE FUNCTION vcu_scope.f() RETURNS integer BEGIN ATOMIC SELECT a FROM vcu_other.t; END",
			"CREATE VIEW vcu_scope.routine_v AS SELECT vcu_scope.f() AS a",
			"CREATE TABLE vcu_scope.sink(a integer,b integer,again integer)",
			"CREATE RULE extra_insert AS ON INSERT TO vcu_scope.v DO INSTEAD INSERT INTO vcu_scope.sink VALUES(NEW.a,NEW.b,NEW.again)",
		} {
			run(fixture.Exec(q, 0))
		}
		// The extra INSERT rule does not belong to the view's defining SELECT.
		// Neither materialized defining objects nor sequence/materialized targets
		// satisfy the release's v / r,v,f,p kind predicates.
		want := append(vcuSeedRows(database),
			vcuRow(database, "vcu_scope", "same_name", "vcu_scope", "t", "a"),
			vcuRow(database, "vcu_scope", "same_name", "vcu_other", "t", "a"),
			vcuRow(database, "vcu_scope", "partition_v", "vcu_scope", "part", "a"),
			vcuRow(database, "vcu_scope", "foreign_v", "vcu_scope", "ft", "a"),
			vcuRow(database, "vcu_scope", "recursive_v", "vcu_scope", "t", "a"))
		s := vcuProjection(b.PG, "view_schema='vcu_scope'", want)
		run(s)
		run(fixture.Prepared(s))

		// Expectations describe parse-time column references, not executed or
		// folded data. Whole-row Vars and whole-table addresses have no attnum;
		// star expansion has two. The dead CASE arm still binds column b.
		for _, tc := range []struct {
			name    string
			body    string
			columns []string
		}{
			{"constant_v", "SELECT 1 AS x", nil},
			{"null_v", "SELECT NULL::integer AS x", nil},
			{"whole_table_v", "SELECT 1 AS x FROM vcu_scope.t", nil},
			{"count_v", "SELECT count(*) AS n FROM vcu_scope.t", nil},
			{"regclass_v", "SELECT 'vcu_scope.t'::regclass AS x", nil},
			{"text_v", "SELECT 'vcu_scope.t'::text AS x", nil},
			{"whole_row_v", "SELECT t AS row FROM vcu_scope.t", nil},
			{"whole_field_v", "SELECT (t).a AS picked FROM vcu_scope.t", []string{"a"}},
			{"whole_field_b_v", "SELECT (t).b AS picked FROM vcu_scope.t", []string{"b"}},
			{"alias_field_v", "SELECT (q).b AS picked FROM vcu_scope.t q", []string{"b"}},
			{"row_field_v", "SELECT (ROW(t.a,t.b)).f1 AS picked FROM vcu_scope.t", []string{"a", "b"}},
			{"star_v", "SELECT * FROM vcu_scope.t", []string{"a", "b"}},
			{"unexecuted_v", "SELECT 1/0 AS bad FROM vcu_scope.t WHERE a=1", []string{"a"}},
			{"predicate_v", "SELECT 1 AS x FROM vcu_scope.t WHERE b=1", []string{"b"}},
			{"order_v", "SELECT a FROM vcu_scope.t ORDER BY b", []string{"a", "b"}},
			{"group_v", "SELECT count(*) AS n FROM vcu_scope.t GROUP BY b HAVING b>0", []string{"b"}},
			{"dead_case_v", "SELECT CASE WHEN FALSE THEN b ELSE 1 END AS x FROM vcu_scope.t", []string{"b"}},
			{"tableoid_v", "SELECT tableoid,a FROM vcu_scope.t", []string{"tableoid", "a"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec("CREATE VIEW vcu_scope."+tc.name+" AS "+tc.body, 0))
				var rows [][]any
				for _, column := range tc.columns {
					rows = append(rows, vcuRow(database, "vcu_scope", tc.name, "vcu_scope", "t", column))
				}
				s := vcuProjection(b.PG, "view_schema='vcu_scope' AND view_name='"+tc.name+"'", rows)
				run(s)
				run(fixture.Prepared(s))
			})
		}
		// Pinned system TABLES have no recorded dependency. System views are
		// unpinned: excluding the whole pg_catalog namespace would be wrong.
		run(fixture.Exec("CREATE VIEW vcu_scope.sys_v AS SELECT viewname FROM pg_catalog.pg_views", 0))
		run(fixture.Exec("CREATE VIEW vcu_scope.sys_t AS SELECT relname FROM pg_catalog.pg_class", 0))
		s = vcuProjection(b.PG, "view_schema='vcu_scope' AND view_name IN ('sys_v','sys_t')", [][]any{vcuRow(database, "vcu_scope", "sys_v", "pg_catalog", "pg_views", "viewname")})
		run(s)
		run(fixture.Prepared(s))
		edges := fixture.Select(`SELECT DISTINCT v.relname AS view_name,t.relname AS table_name,a.attname AS column_name FROM pg_catalog.pg_rewrite r JOIN pg_catalog.pg_class v ON v.oid=r.ev_class JOIN pg_catalog.pg_depend d ON d.classid='pg_catalog.pg_rewrite'::regclass AND d.objid=r.oid JOIN pg_catalog.pg_class t ON d.refclassid='pg_catalog.pg_class'::regclass AND d.refobjid=t.oid JOIN pg_catalog.pg_attribute a ON a.attrelid=t.oid AND a.attnum=d.refobjsubid WHERE r.rulename='_RETURN' AND v.relnamespace='vcu_scope'::regnamespace AND v.relname IN ('sys_v','sys_t') AND t.oid IN ('pg_catalog.pg_views'::regclass,'pg_catalog.pg_class'::regclass)`, []string{"view_name", "table_name", "column_name"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"sys_v", "pg_views", "viewname"}})
		edges.Bag = true
		run(edges)
		run(fixture.Prepared(edges))
		run(fixture.Exec("CREATE TEMP TABLE vcu_temp_t(a integer)", 0))
		run(fixture.Exec("CREATE TEMP VIEW vcu_temp_v AS SELECT a FROM vcu_temp_t", 0))
		var tempSchema string
		if err := c.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='vcu_temp_t'::regclass").Scan(&tempSchema); err != nil {
			t.Fatal(err)
		}
		s = vcuProjection(b.PG, "view_name='vcu_temp_v' AND view_schema='"+tempSchema+"'", [][]any{vcuRow(database, tempSchema, "vcu_temp_v", tempSchema, "vcu_temp_t", "a")})
		run(s)
		run(fixture.Prepared(s))
	})
}

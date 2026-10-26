package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnDomainUsagePopulation(t *testing.T) {
	cduScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		cduSeed(run)
		where := "table_schema='cdu'"
		held := cduHold(t, p, c, cduProjection(b.PG, where, nil).Query)
		want := cduSeedRows(database)
		check := func() { cduCheck(t, p, c, r, held, cduProjection(b.PG, where, want)) }
		check()
		run(fixture.Exec("INSERT INTO cdu.t VALUES(NULL,NULL,NULL)", 1))
		check()
		run(fixture.Exec("DELETE FROM cdu.t", 1))
		check()
		for _, q := range []string{
			"CREATE DOMAIN cdu.arr AS integer[]",
			"CREATE TABLE cdu.controls(a cdu.parent[],b cdu.arr,c integer DEFAULT (1::cdu.parent)::integer,d integer CHECK((d::cdu.parent)>0),e cdu.parent,f cdu.parent)",
			"CREATE VIEW cdu.expression AS SELECT a::integer AS primitive FROM cdu.t",
			"CREATE TYPE cdu.composite AS (member cdu.parent)",
			"CREATE SEQUENCE cdu.seq",
			"CREATE INDEX cdu_idx ON cdu.t(a)",
			"CREATE TABLE cdu.partitioned(k integer,v cdu.parent) PARTITION BY RANGE(k)",
			"CREATE TABLE cdu.partition_child PARTITION OF cdu.partitioned FOR VALUES FROM(0) TO(10)",
			"CREATE FOREIGN DATA WRAPPER cdu_fdw",
			"CREATE SERVER cdu_server FOREIGN DATA WRAPPER cdu_fdw",
			"CREATE FOREIGN TABLE cdu.foreign_t(v cdu.child) SERVER cdu_server",
			"CREATE FUNCTION cdu.cast_only() RETURNS integer LANGUAGE SQL RETURN (1::cdu.parent)::integer",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("CREATE MATERIALIZED VIEW cdu.materialized AS SELECT a FROM cdu.t", 0))
		want = append(want, cduRow(database, "cdu", "arr", "cdu", "controls", "b"), cduRow(database, "cdu", "parent", "cdu", "controls", "e"), cduRow(database, "cdu", "parent", "cdu", "controls", "f"), cduRow(database, "cdu", "parent", "cdu", "partitioned", "v"), cduRow(database, "cdu", "parent", "cdu", "partition_child", "v"), cduRow(database, "cdu", "child", "cdu", "foreign_t", "v"))
		check()
		// The stored direct column/type dependency is the same authority used
		// by the projection; generated object identifiers are never golden data.
		run(fixture.Select(`SELECT a.attname,t.typname,d.deptype FROM pg_catalog.pg_depend d JOIN pg_catalog.pg_attribute a ON a.attrelid=d.objid AND a.attnum=d.objsubid JOIN pg_catalog.pg_type t ON t.oid=d.refobjid WHERE d.classid='pg_catalog.pg_class'::regclass AND d.refclassid='pg_catalog.pg_type'::regclass AND d.objid='cdu.t'::regclass AND d.refobjid IN ('cdu.parent'::regtype,'cdu.child'::regtype) ORDER BY a.attnum`, []string{"attname", "typname", "deptype"}, []string{"NAME", "NAME", "CHAR"}, [][]any{{"a", "parent", "n"}, {"b", "child", "n"}}))
		// Neither this session's nor another session's genuine temporary
		// attributes are excluded by the released relkind predicate.
		for i, name := range []string{"cdu_temp_self", "cdu_temp_peer"} {
			owner := c
			if i == 1 {
				var err error
				owner, err = b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
			}
			fixture.RunStep(t, p.Ctx, owner, r, fixture.Exec("CREATE TEMP TABLE "+name+"(v cdu.parent)", 0))
			defer owner.ExecContext(p.Ctx, "DROP TABLE IF EXISTS "+name)
			var namespace string
			if err := owner.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='"+name+"'::regclass").Scan(&namespace); err != nil || namespace == "" {
				t.Fatalf("temp namespace=%q error=%v", namespace, err)
			}
			run(cduProjection(b.PG, "table_schema='"+namespace+"' AND table_name='"+name+"'", [][]any{cduRow(database, "cdu", "parent", namespace, name, "v")}))
		}
	})
}

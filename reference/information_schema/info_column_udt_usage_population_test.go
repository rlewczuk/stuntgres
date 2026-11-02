package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnUdtUsagePopulation(t *testing.T) {
	cuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE SCHEMA cu_scope",
			"CREATE TYPE cu_scope.e AS ENUM('x')",
			"CREATE TYPE cu_scope.rec AS (v integer)",
			"CREATE DOMAIN cu_scope.p AS integer",
			"CREATE DOMAIN cu_scope.c AS cu_scope.p",
			"CREATE DOMAIN cu_scope.over_array AS integer[]",
			"CREATE TABLE cu_scope.t(i integer,txt text,vc varchar(12),ai integer[],en cu_scope.e,comp cu_scope.rec,dp cu_scope.p,dc cu_scope.c,ad cu_scope.p[],da cu_scope.over_array,ident information_schema.sql_identifier,i2 integer)",
		} {
			run(fixture.Exec(q, 0))
		}
		var want [][]any
		for _, x := range []struct{ column, schema, typ string }{
			{"i", "pg_catalog", "int4"}, {"txt", "pg_catalog", "text"}, {"vc", "pg_catalog", "varchar"}, {"ai", "pg_catalog", "_int4"}, {"en", "cu_scope", "e"}, {"comp", "cu_scope", "rec"}, {"dp", "pg_catalog", "int4"}, {"dc", "cu_scope", "p"}, {"ad", "cu_scope", "_p"}, {"da", "pg_catalog", "_int4"}, {"ident", "pg_catalog", "name"}, {"i2", "pg_catalog", "int4"},
		} {
			want = append(want, cuRow(database, x.schema, x.typ, "cu_scope", "t", x.column))
		}
		where := "table_schema='cu_scope'"
		held := cduHold(t, p, c, cuProjection(b.PG, where, nil).Query)
		check := func() { cuCheck(t, p, c, r, held, cuProjection(b.PG, where, want)) }
		check()
		// Array identity is observed relationally, not by allocator coincidence.
		run(fixture.Select(`SELECT a.typname,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link FROM pg_catalog.pg_type t JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE t.oid='cu_scope.p'::regtype`, []string{"typname", "element_link", "array_link"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"_p", true, true}}))
		for _, q := range []string{
			"CREATE TYPE cu_scope.unused_enum AS ENUM('unused')",
			"CREATE DOMAIN cu_scope.unused_domain AS integer",
			"CREATE TYPE cu_scope.unused_composite AS (member cu_scope.p)",
			"CREATE TABLE cu_scope.empty()",
			"CREATE FUNCTION cu_scope.cast_only(v cu_scope.p) RETURNS cu_scope.p LANGUAGE SQL RETURN v",
			"CREATE TABLE cu_scope.expressions(d integer DEFAULT (1::cu_scope.p)::integer,g integer GENERATED ALWAYS AS ((d::cu_scope.p)::integer) STORED)",
			"CREATE VIEW cu_scope.v AS SELECT dp,dc,i FROM cu_scope.t",
			"CREATE VIEW cu_scope.cast_v AS SELECT dp::integer AS primitive FROM cu_scope.t",
			"CREATE SEQUENCE cu_scope.seq",
			"CREATE INDEX cu_idx ON cu_scope.t(i)",
			"CREATE TABLE cu_scope.partitioned(k integer,v cu_scope.e) PARTITION BY RANGE(k)",
			"CREATE TABLE cu_scope.partition_child PARTITION OF cu_scope.partitioned FOR VALUES FROM(0) TO(10)",
			"CREATE FOREIGN DATA WRAPPER cu_fdw",
			"CREATE SERVER cu_server FOREIGN DATA WRAPPER cu_fdw",
			"CREATE FOREIGN TABLE cu_scope.foreign_t(k integer,v cu_scope.e) SERVER cu_server",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("CREATE MATERIALIZED VIEW cu_scope.materialized AS SELECT dp FROM cu_scope.t", 0))
		for _, x := range []struct{ table, column, schema, typ string }{
			{"expressions", "d", "pg_catalog", "int4"}, {"expressions", "g", "pg_catalog", "int4"},
			{"v", "dp", "pg_catalog", "int4"}, {"v", "dc", "cu_scope", "p"}, {"v", "i", "pg_catalog", "int4"}, {"cast_v", "primitive", "pg_catalog", "int4"},
			{"partitioned", "k", "pg_catalog", "int4"}, {"partitioned", "v", "cu_scope", "e"}, {"partition_child", "k", "pg_catalog", "int4"}, {"partition_child", "v", "cu_scope", "e"}, {"foreign_t", "k", "pg_catalog", "int4"}, {"foreign_t", "v", "cu_scope", "e"},
		} {
			want = append(want, cuRow(database, x.schema, x.typ, "cu_scope", x.table, x.column))
		}
		check()
		run(fixture.Exec("INSERT INTO cu_scope.t(i,i2) VALUES(1,2)", 1))
		check()
		run(fixture.Exec("DELETE FROM cu_scope.t", 1))
		check()
		for i, name := range []string{"cu_temp_self", "cu_temp_peer"} {
			owner := c
			if i == 1 {
				var err error
				owner, err = b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
			}
			fixture.RunStep(t, p.Ctx, owner, r, fixture.Exec("CREATE TEMP TABLE "+name+"(v cu_scope.c)", 0))
			defer owner.ExecContext(p.Ctx, "DROP TABLE IF EXISTS "+name)
			var namespace string
			if err := owner.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='"+name+"'::regclass").Scan(&namespace); err != nil || namespace == "" {
				t.Fatalf("temp namespace=%q error=%v", namespace, err)
			}
			s := cuProjection(b.PG, "table_schema='"+namespace+"' AND table_name='"+name+"'", [][]any{cuRow(database, "cu_scope", "p", namespace, name, "v")})
			cuCheck(t, p, c, r, cduHold(t, p, c, s.Query), s)
		}
	})
}

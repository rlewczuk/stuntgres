package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Oid-equality probes of pg_type and pg_proc return exactly the matching rows:
// literal, cast and parameter probes, NULL and unmatched probes, a non-unique
// oid column, derived subqueries joined to other catalogs, a scroll cursor, a
// transaction-private catalog and probes held across DDL.
func TestReferencePGCatalogKeyProbes(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ckp_ns")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{
				"CREATE SCHEMA ckp_ns",
				"CREATE DOMAIN ckp_ns.d1 AS integer",
				"CREATE FUNCTION ckp_ns.f1() RETURNS integer LANGUAGE sql AS 'SELECT 1'",
				"CREATE FUNCTION ckp_ns.f2(integer) RETURNS integer LANGUAGE sql AS 'SELECT $1'",
			} {
				run(fixture.Exec(q, 0))
			}
			var d1, f1 int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ckp_ns.d1'::regtype::oid::int8,'ckp_ns.f1()'::regprocedure::oid::int8").Scan(&d1, &f1); err != nil {
				t.Fatal(err)
			}
			names := func(values ...string) [][]any {
				rows := [][]any{}
				for _, v := range values {
					rows = append(rows, []any{v})
				}
				return rows
			}
			both := func(s fixture.Step) {
				t.Helper()
				run(s)
				run(fixture.Prepared(s))
			}
			held := func(q string) fixture.HeldStatement {
				t.Helper()
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { stmt.Close() })
				return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			}
			typeByParam := held("SELECT typname FROM pg_type WHERE oid=$1::oid")
			procByParam := held("SELECT proname FROM pg_proc WHERE oid=$1")
			namespaceProcs := "SELECT proname FROM pg_proc WHERE pronamespace='ckp_ns'::regnamespace ORDER BY proname"
			heldNamespace := held(namespaceProcs)
			typeParam := func(arg any, want ...string) {
				t.Helper()
				s := fixture.Select(typeByParam.SQL, []string{"typname"}, []string{"NAME"}, names(want...), arg)
				both(s)
				fixture.RunStep(t, p.Ctx, typeByParam, b.Recorder, s)
			}
			procParam := func(arg any, want ...string) {
				t.Helper()
				s := fixture.Select(procByParam.SQL, []string{"proname"}, []string{"NAME"}, names(want...), arg)
				both(s)
				fixture.RunStep(t, p.Ctx, procByParam, b.Recorder, s)
			}
			namespace := func(want ...string) {
				t.Helper()
				s := fixture.Select(namespaceProcs, []string{"proname"}, []string{"NAME"}, names(want...))
				both(s)
				fixture.RunStep(t, p.Ctx, heldNamespace, b.Recorder, s)
			}

			both(fixture.Select("SELECT typname FROM pg_type WHERE oid='ckp_ns.d1'::regtype::oid", []string{"typname"}, []string{"NAME"}, names("d1")))
			both(fixture.Select("SELECT typname FROM pg_type WHERE oid=23", []string{"typname"}, []string{"NAME"}, names("int4")))
			both(fixture.Select("SELECT proname FROM pg_proc WHERE oid=0", []string{"proname"}, []string{"NAME"}, names()))
			both(fixture.Select("SELECT proname FROM pg_proc WHERE oid=NULL::oid", []string{"proname"}, []string{"NAME"}, names()))
			typeParam(d1, "d1")
			typeParam(int64(0))
			typeParam(nil)
			procParam(f1, "f1")
			namespace("f1", "f2")
			both(fixture.Select("SELECT p.proname,l.lanname FROM (SELECT * FROM pg_proc WHERE oid='ckp_ns.f2(integer)'::regprocedure::oid) p JOIN (SELECT * FROM pg_language WHERE lanname='sql') l ON l.oid=p.prolang", []string{"proname", "lanname"}, []string{"NAME", "NAME"}, [][]any{{"f2", "sql"}}))
			both(fixture.Select("SELECT t.typname,b.typname AS base FROM (SELECT * FROM pg_type WHERE oid='ckp_ns.d1'::regtype::oid) t JOIN (SELECT * FROM pg_type WHERE oid=23) b ON b.oid=t.typbasetype", []string{"typname", "base"}, []string{"NAME", "NAME"}, [][]any{{"d1", "int4"}}))
			both(fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE oid='ckp_ns.f1()'::regprocedure::oid AND proname='f1'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))

			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DECLARE ckp_cursor SCROLL CURSOR FOR "+namespaceProcs, 0))
			for _, step := range []struct{ fetch, want string }{{"FETCH LAST FROM ckp_cursor", "f2"}, {"FETCH PRIOR FROM ckp_cursor", "f1"}, {"FETCH ABSOLUTE 2 FROM ckp_cursor", "f2"}} {
				run(fixture.Select(step.fetch, []string{"proname"}, []string{"NAME"}, names(step.want)))
			}
			run(fixture.Exec("CLOSE ckp_cursor", 0))
			run(fixture.Exec("COMMIT", 0))

			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("CREATE DOMAIN ckp_ns.d2 AS text", 0))
			var d2 int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ckp_ns.d2'::regtype::oid::int8").Scan(&d2); err != nil {
				t.Fatal(err)
			}
			typeParam(d2, "d2")
			run(fixture.Exec("ROLLBACK", 0))
			typeParam(d2)
			typeParam(d1, "d1")

			run(fixture.Exec("DROP FUNCTION ckp_ns.f1()", 0))
			procParam(f1)
			namespace("f2")
			run(fixture.Exec("DROP SCHEMA ckp_ns CASCADE", 0))
			typeParam(d1)
			run(fixture.Error("SELECT proname FROM pg_proc WHERE pronamespace='ckp_ns'::regnamespace", "3F000"))
		})
	}
}

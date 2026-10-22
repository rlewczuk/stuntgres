package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterNotNullLifecycle(t *testing.T) {
	domainAlterValueScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{"CREATE DOMAIN domain_s.d AS integer", "CREATE DOMAIN domain_s.child AS domain_s.d", "CREATE DOMAIN domain_s.grandchild AS domain_s.child", "CREATE TABLE domain_s.t(v domain_s.d,w domain_s.grandchild)", "CREATE TABLE domain_s.other(v domain_s.child)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.t VALUES(NULL,NULL)", 1))
		run(fixture.Exec("INSERT INTO domain_s.other VALUES(NULL)", 1))
		q := "ALTER DOMAIN domain_s.d SET NOT NULL"
		checkAbsent := func() {
			for _, s := range domainAlterOwnNull("d", false, nil) {
				run(s)
			}
		}
		for _, tc := range []struct{ table, column, repair string }{
			{"t", "v", "UPDATE domain_s.t SET v=2"},
			{"t", "w", "UPDATE domain_s.t SET w=2"},
			{"other", "v", "UPDATE domain_s.other SET v=2"},
		} {
			t.Run(tc.table+tc.column, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, domainAlterNullFailure(q, tc.table, tc.column)) })
			checkAbsent()
			run(fixture.Exec(tc.repair, 1))
		}
		// An unrelated historical NOT VALID CHECK violation must not be recast.
		run(fixture.Exec("ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>10) NOT VALID", 0))
		run(fixture.Prepared(fixture.Exec(q, 0)))
		for _, s := range domainAlterOwnNull("d", true, [][]any{{"d_not_null"}}) {
			run(s)
		}
		for _, s := range domainAlterOwnNull("child", false, nil) {
			run(s)
		}
		run(fixture.Select("SELECT v,w FROM domain_s.t", []string{"v", "w"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(2)}}))
		for _, prepared := range []bool{false, true} {
			for _, sql := range []string{"INSERT INTO domain_s.t VALUES(NULL,20)", "INSERT INTO domain_s.t(w) VALUES(20)", "INSERT INTO domain_s.t VALUES(20,20),(NULL,20)"} {
				run(domainDefaultFailure(sql, domainNotNullDiagnostic("domain_s", "d"), prepared, false))
			}
		}
		run(fixture.Select("SELECT count(*) AS n FROM domain_s.t", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Exec("ALTER DOMAIN domain_s.d DROP NOT NULL", 0))
		run(fixture.Prepared(fixture.Exec("ALTER DOMAIN domain_s.d DROP NOT NULL", 0)))
		checkAbsent()
		run(fixture.Exec("INSERT INTO domain_s.t VALUES(NULL,NULL)", 1))
		// Empty physical consumers, ordinary views, partitions and materialized
		// views use the same admitted discovery paths as CHECK validation.
		for _, sql := range []string{
			"CREATE DOMAIN domain_s.empty AS integer",
			"CREATE TABLE domain_s.part(id integer,v domain_s.empty) PARTITION BY RANGE(id)",
			"CREATE TABLE domain_s.part0 PARTITION OF domain_s.part FOR VALUES FROM(0) TO(10)",
			"CREATE VIEW domain_s.unstored AS SELECT NULL::domain_s.empty AS v FROM public.one",
			"CREATE MATERIALIZED VIEW domain_s.mat AS SELECT v FROM domain_s.part",
			"ALTER DOMAIN domain_s.empty SET NOT NULL",
			"ALTER DOMAIN domain_s.empty DROP NOT NULL",
		} {
			run(fixture.Exec(sql, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.part VALUES(1,NULL)", 1))
		t.Run("partition", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, domainAlterNullFailure("ALTER DOMAIN domain_s.empty SET NOT NULL", "part0", "v"))
		})
		// Repair through admitted physical operations, not adjacent partition
		// hierarchy UPDATE support. The same leaf remains a validating use.
		run(fixture.Exec("TRUNCATE domain_s.part", 0))
		run(fixture.Exec("INSERT INTO domain_s.part VALUES(1,1)", 1))
		run(fixture.Exec("DROP MATERIALIZED VIEW domain_s.mat", 0))
		run(fixture.Exec("CREATE MATERIALIZED VIEW domain_s.mat AS SELECT NULL::domain_s.empty AS v FROM public.one", 1))
		t.Run("materialized", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, domainAlterNullFailure("ALTER DOMAIN domain_s.empty SET NOT NULL", "mat", "v"))
		})
		run(fixture.Exec("DROP MATERIALIZED VIEW domain_s.mat", 0))
		run(fixture.Exec("ALTER DOMAIN domain_s.empty SET NOT NULL", 0))
	})
}

func TestReferenceDomainAlterNotNullEdges(t *testing.T) {
	domainAlterValueScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{"CREATE DOMAIN domain_s.d AS integer", "CREATE DOMAIN domain_s.child AS domain_s.d NULL", "CREATE TABLE domain_s.t(id integer,v domain_s.d)", "ALTER DOMAIN domain_s.d SET NOT NULL"} {
			run(fixture.Exec(q, 0))
		}
		id := domainAlterOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid AND contype='n'")
		run(fixture.Exec("INSERT INTO domain_s.t VALUES(1,2)", 1))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT r.v FROM public.one o LEFT JOIN domain_s.t r ON false", []string{"v"}, []string{"INT4"}, [][]any{{nil}})
			s.Prepared = prepared
			run(s)
			s = fixture.Select("SELECT (SELECT v FROM domain_s.t WHERE false)::domain_s.d AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{nil}})
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec("INSERT INTO domain_s.t SELECT 2,r.v FROM public.one o LEFT JOIN domain_s.t r ON false", 1))
		run(fixture.Exec("INSERT INTO domain_s.t SELECT 3,(SELECT v FROM domain_s.t WHERE false)", 1))
		run(fixture.Exec("ALTER DOMAIN domain_s.d SET NOT NULL", 0))
		if got := domainAlterOID(t, p, c, "SELECT oid FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid AND contype='n'"); got != id {
			t.Fatalf("repeat SET identity %d -> %d", id, got)
		}
		run(fixture.Exec("ALTER DOMAIN domain_s.child DROP NOT NULL", 0))
		run(domainDefaultFailure("SELECT NULL::domain_s.child AS v FROM public.one", domainNotNullDiagnostic("domain_s", "child"), false, true))
		run(fixture.Exec("ALTER DOMAIN domain_s.d DROP NOT NULL", 0))
		t.Run("rescan_after_drop", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, domainAlterNullFailure("ALTER DOMAIN domain_s.d SET NOT NULL", "t", "v"))
		})
		for _, s := range domainAlterOwnNull("d", false, nil) {
			run(s)
		}
		run(fixture.Select("SELECT id,v FROM domain_s.t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), nil}, {int64(3), nil}}))
		run(fixture.Exec("UPDATE domain_s.t SET v=2 WHERE v IS NULL", 2))
		run(fixture.Exec("ALTER DOMAIN domain_s.d SET NOT NULL", 0))
		for _, q := range []string{"CREATE DOMAIN domain_s.own AS domain_s.d NOT NULL", "CREATE TABLE domain_s.column_nn(v domain_s.d NOT NULL)", "ALTER DOMAIN domain_s.own DROP NOT NULL", "ALTER DOMAIN domain_s.d DROP NOT NULL"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Select("SELECT NULL::domain_s.own AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
		d := fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "column_nn" violates not-null constraint`)
		d.Schema, d.Table, d.Column, d.Detail = "domain_s", "column_nn", "v", "Failing row contains (null)."
		run(fixture.ErrorDiagnostic("INSERT INTO domain_s.column_nn VALUES(NULL)", d))
		for _, q := range []string{"CREATE TABLE domain_s.holder(v domain_s.d[])", "ALTER DOMAIN domain_s.d SET DEFAULT 7", "ALTER DOMAIN domain_s.d DROP DEFAULT", "ALTER DOMAIN domain_s.d DROP NOT NULL"} {
			run(fixture.Exec(q, 0))
		}
		t.Run("array_carrier", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, domainAlterContainerFailure("ALTER DOMAIN domain_s.d SET NOT NULL", "d", "holder", "v"))
		})
		run(fixture.Exec("DROP TABLE domain_s.holder", 0))
		for _, q := range []string{"CREATE TYPE domain_s.box AS(v domain_s.d)", "CREATE TABLE domain_s.holder(v domain_s.box)"} {
			run(fixture.Exec(q, 0))
		}
		t.Run("composite_carrier", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, domainAlterContainerFailure("ALTER DOMAIN domain_s.d SET NOT NULL", "d", "holder", "v"))
		})
	})
}

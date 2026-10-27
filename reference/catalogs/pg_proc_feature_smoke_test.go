package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGProcFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgProcShapeStep(b, "SELECT * FROM pg_catalog.pg_proc WHERE oid=1397", [][]any{pgProcAbsRow()}))
			pgProcBoth(t, p, b, b.DB, "SELECT abs(-7::integer) AS value", []string{"value"}, []string{"INT4"}, [][]any{{int64(7)}})
			pgProcBoth(t, p, b, b.DB, "SELECT prosupport::oid,proleakproof FROM pg_proc WHERE oid=3696", []string{"prosupport", "proleakproof"}, []string{"OID", "BOOL"}, [][]any{{int64(6242), true}})
			pgProcExec(t, p, b, b.DB, "CREATE FUNCTION public.pgproc_smoke(a integer DEFAULT 7) RETURNS integer LANGUAGE SQL RETURN a")
			const q = "SELECT proname,prosrc,pg_get_expr(proargdefaults,0) AS defaults,prosqlbody IS NOT NULL AS body FROM pg_proc WHERE oid='pgproc_smoke(integer)'::regprocedure"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			probe := func(defaults string, value int64) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"proname", "prosrc", "defaults", "body"}, []string{"NAME", "TEXT", "TEXT", "BOOL"}, [][]any{{"pgproc_smoke", "", defaults, true}}))
				pgProcBoth(t, p, b, b.DB, "SELECT p.proargtypes,p.prorettype,p.prokind,p.pronargdefaults,l.lanname,p.proargdefaults IS NOT NULL AS defaults,p.prosqlbody IS NOT NULL AS body FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid='pgproc_smoke(integer)'::regprocedure", []string{"proargtypes", "prorettype", "prokind", "pronargdefaults", "lanname", "defaults", "body"}, []string{"OIDVECTOR", "OID", "CHAR", "INT2", "NAME", "BOOL", "BOOL"}, [][]any{{"23", int64(23), "f", int64(1), "sql", true, true}})
				pgProcBoth(t, p, b, b.DB, "SELECT pgproc_smoke() AS value", []string{"value"}, []string{"INT4"}, [][]any{{value}})
			}
			probe("7", 7)
			old, err := b.DB.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			metadata, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			pgProcExec(t, p, b, b.DB, "CREATE OR REPLACE FUNCTION public.pgproc_smoke(a integer DEFAULT 9) RETURNS integer LANGUAGE SQL RETURN a")
			probe("9", 9)
			pgProcRetained(t, old, metadata, "pgproc_smoke", "7")
			d := fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}
			s := fixture.ImplicitError("SELECT missing FROM pg_catalog.pg_proc", d)
			s.SelectRows = true
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			probe("9", 9)
			const empty = "SELECT * FROM pg_proc WHERE oid=$1::oid"
			emptyStmt, err := b.DB.PrepareContext(p.Ctx, empty)
			if err != nil {
				t.Fatal(err)
			}
			defer emptyStmt.Close()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: emptyStmt, SQL: empty}, b.Recorder, pgProcShapeStep(b, empty, nil, int64(0)))
		})
	}
}

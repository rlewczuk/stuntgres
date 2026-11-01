package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewRoutineUsageLifecycle(t *testing.T) {
	vruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vruSeed(run)
		run(fixture.Exec("CREATE SCHEMA vru_moved", 0))
		run(fixture.Exec("CREATE VIEW vru_scope.w AS SELECT x FROM vru_scope.v", 0))
		where := "table_schema IN ('vru_scope','vru_moved','vru_renamed')"
		held := vruHeld(t, p, c, r, vruProjection(b.PG, where, nil).Query)
		check := func(want [][]any) { s := vruProjection(b.PG, where, want); run(s); run(fixture.Prepared(s)); held(s) }
		check(vruSeedRows(t, p, c, database))
		g := rsuIdentity(t, p, c, "vru_scope", "g", "")
		run(fixture.Exec("CREATE OR REPLACE VIEW vru_scope.v AS SELECT vru_scope.g() AS x", 0))
		check([][]any{vruRow(database, "vru_scope", "v", "vru_scope", g)})
		definition := " SELECT vru_scope.g() AS x;"
		run(fixture.Select("SELECT definition FROM pg_catalog.pg_views WHERE schemaname='vru_scope' AND viewname='v'", []string{"definition"}, []string{"TEXT"}, [][]any{{definition}}))
		run(fixture.Select("SELECT pg_get_viewdef('vru_scope.v'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{definition}}))
		run(vtuProjection(b.PG, "view_schema='vru_scope'", [][]any{vtuRow(database, "vru_scope", "w", "vru_scope", "v")}))
		run(vcuProjection(b.PG, "view_schema='vru_scope'", [][]any{vcuRow(database, "vru_scope", "w", "vru_scope", "v", "x")}))
		run(fixture.Exec("ALTER FUNCTION vru_scope.g() RENAME TO renamed_g", 0))
		renamed := rsuIdentity(t, p, c, "vru_scope", "renamed_g", "")
		if g[strings.LastIndex(g, "_"):] != renamed[strings.LastIndex(renamed, "_"):] {
			t.Fatalf("rename changed identity: %s => %s", g, renamed)
		}
		check([][]any{vruRow(database, "vru_scope", "v", "vru_scope", renamed)})
		run(fixture.Exec("ALTER FUNCTION vru_scope.renamed_g() SET SCHEMA vru_moved", 0))
		if moved := rsuIdentity(t, p, c, "vru_moved", "renamed_g", ""); moved != renamed {
			t.Fatalf("move changed identity: %s => %s", renamed, moved)
		}
		check([][]any{vruRow(database, "vru_scope", "v", "vru_moved", renamed)})
		run(fixture.Exec("ALTER VIEW vru_scope.v RENAME TO renamed_v", 0))
		check([][]any{vruRow(database, "vru_scope", "renamed_v", "vru_moved", renamed)})
		run(fixture.Exec("ALTER VIEW vru_scope.renamed_v SET SCHEMA vru_moved", 0))
		check([][]any{vruRow(database, "vru_moved", "renamed_v", "vru_moved", renamed)})
		run(fixture.Exec("ALTER SCHEMA vru_moved RENAME TO vru_renamed", 0))
		want := [][]any{vruRow(database, "vru_renamed", "renamed_v", "vru_renamed", renamed)}
		check(want)
		long := strings.Repeat("雪", 21)
		run(fixture.Exec(`CREATE FUNCTION vru_scope."`+long+`"() RETURNS integer RETURN 4`, 0))
		run(fixture.Exec(`CREATE VIEW vru_scope.long_v AS SELECT vru_scope."`+long+`"() AS x,vru_scope.overloaded(1) AS y,vru_scope.overloaded(1::bigint) AS z`, 0))
		longID := rsuIdentity(t, p, c, "vru_scope", long, "")
		if len(longID) > 63 || !utf8.ValidString(longID) || !strings.HasPrefix(longID, "雪") {
			t.Fatalf("clipped identity=%q", longID)
		}
		want = append(want, vruRow(database, "vru_scope", "long_v", "vru_scope", longID), vruRow(database, "vru_scope", "long_v", "vru_scope", rsuIdentity(t, p, c, "vru_scope", "overloaded", "integer")), vruRow(database, "vru_scope", "long_v", "vru_scope", rsuIdentity(t, p, c, "vru_scope", "overloaded", "bigint")))
		check(want)
		run(fixture.Exec("DROP VIEW vru_scope.long_v", 0))
		want = want[:1]
		check(want)
		run(fixture.Exec("DROP VIEW vru_renamed.renamed_v CASCADE", 0))
		check(nil)
		run(fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relnamespace='vru_scope'::regnamespace AND relname='w'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("DROP FUNCTION vru_renamed.renamed_g()", 0))
		run(fixture.Exec("CREATE FUNCTION vru_renamed.renamed_g() RETURNS integer RETURN 10", 0))
		fresh := rsuIdentity(t, p, c, "vru_renamed", "renamed_g", "")
		if fresh == renamed {
			t.Fatal("drop/recreate reused specific identity", fresh)
		}
		run(fixture.Exec("CREATE VIEW vru_scope.v AS SELECT vru_renamed.renamed_g() AS x", 0))
		check([][]any{vruRow(database, "vru_scope", "v", "vru_renamed", fresh)})
		run(fixture.Exec("DROP FUNCTION vru_renamed.renamed_g() CASCADE", 0))
		check(nil)
	})
}

func vruMissingColumn(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
	d.Position = "8"
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.view_routine_usage", d)
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, c, r, s)
	}
}

func TestReferenceInfoViewRoutineUsageDiagnostics(t *testing.T) {
	vruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vruMissingColumn(t, p, c, r)
		run(fixture.Exec("CREATE FUNCTION vru_diag_f() RETURNS integer RETURN 1", 0))
		run(fixture.Exec("CREATE VIEW vru_diag_v AS SELECT vru_diag_f() AS x", 0))
		identity := rsuIdentity(t, p, c, "public", "vru_diag_f", "")
		s := vruProjection(b.PG, "table_schema='public' AND table_name='vru_diag_v'", [][]any{vruRow(database, "public", "vru_diag_v", "public", identity)})
		held := vruHeld(t, p, c, r, s.Query)
		definition := " SELECT vru_diag_f() AS x;"
		check := func() {
			run(s)
			run(fixture.Prepared(s))
			held(s)
			run(fixture.Select("SELECT definition FROM pg_catalog.pg_views WHERE schemaname='public' AND viewname='vru_diag_v'", []string{"definition"}, []string{"TEXT"}, [][]any{{definition}}))
			if got := rsuIdentity(t, p, c, "public", "vru_diag_f", ""); got != identity {
				t.Fatalf("failed DDL changed identity: %s => %s", identity, got)
			}
		}
		check()
		d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function vru_diag_f() because other objects depend on it")
		d.Detail = "view vru_diag_v depends on function vru_diag_f()"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		for _, prepared := range []bool{false, true} {
			failure := fixture.ErrorDiagnostic("DROP FUNCTION vru_diag_f() RESTRICT", d)
			failure.Prepared = prepared
			failure.ErrorStage = "execute"
			run(failure)
			check()
		}
		q := "CREATE OR REPLACE VIEW vru_diag_v AS SELECT missing"
		d = fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		d.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "missing")]) + 1)
		for _, prepared := range []bool{false, true} {
			failure := fixture.ErrorDiagnostic(q, d)
			failure.Prepared = prepared
			failure.ErrorStage = "execute"
			run(failure)
			check()
		}
	})
}

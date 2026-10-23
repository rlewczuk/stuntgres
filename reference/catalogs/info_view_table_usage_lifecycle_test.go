package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewTableUsageLifecycle(t *testing.T) {
	vtuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vtuSeed(run)
		run(fixture.Exec("CREATE SCHEMA vtu_moved", 0))
		where := "view_schema IN ('vtu_scope','vtu_moved','vtu_renamed')"
		held := vtuHeld(t, p, c, r, vtuProjection(b.PG, where, nil).Query)
		check := func(want [][]any) {
			s := vtuProjection(b.PG, where, want)
			held(s)
			run(s)
			run(fixture.Prepared(s))
		}
		check(vtuSeedRows(database))
		run(fixture.Exec("ALTER TABLE vtu_scope.t RENAME COLUMN b TO label", 0))
		check(vtuSeedRows(database))
		run(fixture.Exec("ALTER TABLE vtu_scope.t RENAME TO bound_t", 0))
		run(fixture.Exec("CREATE TABLE vtu_scope.t(a integer,b integer)", 0))
		want := [][]any{vtuRow(database, "vtu_scope", "v", "vtu_scope", "bound_t"), vtuRow(database, "vtu_scope", "v", "vtu_scope", "u"), vtuRow(database, "vtu_scope", "w", "vtu_scope", "v")}
		check(want)
		run(fixture.Exec("ALTER TABLE vtu_scope.bound_t SET SCHEMA vtu_moved", 0))
		want[0] = vtuRow(database, "vtu_scope", "v", "vtu_moved", "bound_t")
		check(want)
		run(fixture.Exec("ALTER VIEW vtu_scope.v RENAME TO renamed_v", 0))
		want = [][]any{vtuRow(database, "vtu_scope", "renamed_v", "vtu_moved", "bound_t"), vtuRow(database, "vtu_scope", "renamed_v", "vtu_scope", "u"), vtuRow(database, "vtu_scope", "w", "vtu_scope", "renamed_v")}
		check(want)
		run(fixture.Exec("ALTER VIEW vtu_scope.renamed_v SET SCHEMA vtu_moved", 0))
		want = [][]any{vtuRow(database, "vtu_moved", "renamed_v", "vtu_moved", "bound_t"), vtuRow(database, "vtu_moved", "renamed_v", "vtu_scope", "u"), vtuRow(database, "vtu_scope", "w", "vtu_moved", "renamed_v")}
		check(want)
		run(fixture.Exec("ALTER SCHEMA vtu_moved RENAME TO vtu_renamed", 0))
		want = [][]any{vtuRow(database, "vtu_renamed", "renamed_v", "vtu_renamed", "bound_t"), vtuRow(database, "vtu_renamed", "renamed_v", "vtu_scope", "u"), vtuRow(database, "vtu_scope", "w", "vtu_renamed", "renamed_v")}
		check(want)
		run(fixture.ImplicitError("CREATE OR REPLACE VIEW vtu_renamed.renamed_v AS SELECT missing FROM vtu_scope.u", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "56"}))
		check(want)
		run(fixture.Exec("CREATE OR REPLACE VIEW vtu_renamed.renamed_v AS SELECT a,a AS b,a AS again FROM vtu_scope.u", 0))
		want = want[1:]
		check(want)
		run(fixture.Exec("DROP TABLE vtu_renamed.bound_t", 0))
		check(want)
		run(fixture.Exec("DROP VIEW vtu_scope.w", 0))
		check(want[:1])
		run(fixture.Exec("DROP VIEW vtu_renamed.renamed_v", 0))
		check(nil)
		run(fixture.Exec("CREATE VIEW vtu_renamed.renamed_v AS SELECT a FROM vtu_scope.t", 0))
		check([][]any{vtuRow(database, "vtu_renamed", "renamed_v", "vtu_scope", "t")})
		run(fixture.Exec("CREATE VIEW vtu_scope.w AS SELECT a FROM vtu_renamed.renamed_v", 0))
		run(fixture.Exec("CREATE VIEW vtu_scope.keep_v AS SELECT a FROM vtu_scope.u", 0))
		run(fixture.Exec("DROP TABLE vtu_scope.t CASCADE", 0))
		check([][]any{vtuRow(database, "vtu_scope", "keep_v", "vtu_scope", "u")})
	})
}

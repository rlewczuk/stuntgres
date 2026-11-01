package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewColumnUsageLifecycle(t *testing.T) {
	vcuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vcuSeed(run)
		run(fixture.Exec("CREATE SCHEMA vcu_moved", 0))
		where := "view_schema IN ('vcu_scope','vcu_moved','vcu_renamed')"
		held := vcuHeld(t, p, c, r, vcuProjection(b.PG, where, nil).Query)
		check := func(want [][]any) {
			s := vcuProjection(b.PG, where, want)
			held(s)
			run(s)
			run(fixture.Prepared(s))
		}
		check(vcuSeedRows(database))
		run(fixture.Exec("ALTER TABLE vcu_scope.t RENAME COLUMN b TO label", 0))
		want := vcuSeedRows(database)
		want[1] = vcuRow(database, "vcu_scope", "v", "vcu_scope", "t", "label")
		check(want)
		// Dropping a physical prefix must not confuse a stable attnum with its
		// new dense slot. The output alias b is not the target's current name.
		run(fixture.Exec("CREATE TABLE vcu_scope.compact(dead integer,a integer,b integer)", 0))
		run(fixture.Exec("CREATE VIEW vcu_scope.compact_v AS SELECT b FROM vcu_scope.compact", 0))
		compact := vcuRow(database, "vcu_scope", "compact_v", "vcu_scope", "compact", "b")
		check(append(want, compact))
		run(fixture.Exec("ALTER TABLE vcu_scope.compact DROP COLUMN dead", 0))
		check(append(want, compact))
		run(fixture.Exec("ALTER TABLE vcu_scope.compact RENAME COLUMN b TO label", 0))
		check(append(want, vcuRow(database, "vcu_scope", "compact_v", "vcu_scope", "compact", "label")))
		run(fixture.Exec("DROP VIEW vcu_scope.compact_v", 0))
		run(fixture.Exec("DROP TABLE vcu_scope.compact", 0))
		check(want)
		run(fixture.Exec("ALTER TABLE vcu_scope.t RENAME TO bound_t", 0))
		run(fixture.Exec("CREATE TABLE vcu_scope.t(a integer,b integer)", 0))
		want[0] = vcuRow(database, "vcu_scope", "v", "vcu_scope", "bound_t", "a")
		want[1] = vcuRow(database, "vcu_scope", "v", "vcu_scope", "bound_t", "label")
		check(want)
		run(fixture.Exec("ALTER TABLE vcu_scope.bound_t SET SCHEMA vcu_moved", 0))
		want[0] = vcuRow(database, "vcu_scope", "v", "vcu_moved", "bound_t", "a")
		want[1] = vcuRow(database, "vcu_scope", "v", "vcu_moved", "bound_t", "label")
		check(want)
		run(fixture.Exec("ALTER VIEW vcu_scope.v RENAME TO renamed_v", 0))
		want = [][]any{
			vcuRow(database, "vcu_scope", "renamed_v", "vcu_moved", "bound_t", "a"),
			vcuRow(database, "vcu_scope", "renamed_v", "vcu_moved", "bound_t", "label"),
			vcuRow(database, "vcu_scope", "renamed_v", "vcu_scope", "u", "a"),
			vcuRow(database, "vcu_scope", "w", "vcu_scope", "renamed_v", "a"),
		}
		check(want)
		run(fixture.Exec("ALTER VIEW vcu_scope.renamed_v SET SCHEMA vcu_moved", 0))
		want = [][]any{
			vcuRow(database, "vcu_moved", "renamed_v", "vcu_moved", "bound_t", "a"),
			vcuRow(database, "vcu_moved", "renamed_v", "vcu_moved", "bound_t", "label"),
			vcuRow(database, "vcu_moved", "renamed_v", "vcu_scope", "u", "a"),
			vcuRow(database, "vcu_scope", "w", "vcu_moved", "renamed_v", "a"),
		}
		check(want)
		run(fixture.Exec("ALTER SCHEMA vcu_moved RENAME TO vcu_renamed", 0))
		want = [][]any{
			vcuRow(database, "vcu_renamed", "renamed_v", "vcu_renamed", "bound_t", "a"),
			vcuRow(database, "vcu_renamed", "renamed_v", "vcu_renamed", "bound_t", "label"),
			vcuRow(database, "vcu_renamed", "renamed_v", "vcu_scope", "u", "a"),
			vcuRow(database, "vcu_scope", "w", "vcu_renamed", "renamed_v", "a"),
		}
		check(want)
		var definition string
		definitionQuery := "SELECT definition FROM pg_catalog.pg_views WHERE schemaname='vcu_renamed' AND viewname='renamed_v'"
		if err := c.QueryRowContext(p.Ctx, definitionQuery).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		// Replay the stored definition after identity changes, prescribing its
		// direct target columns independently of the renderer's spelling.
		run(fixture.Exec("CREATE VIEW vcu_scope.replay_v AS "+definition, 0))
		check(append(want,
			vcuRow(database, "vcu_scope", "replay_v", "vcu_renamed", "bound_t", "a"),
			vcuRow(database, "vcu_scope", "replay_v", "vcu_renamed", "bound_t", "label"),
			vcuRow(database, "vcu_scope", "replay_v", "vcu_scope", "u", "a")))
		run(fixture.Exec("DROP VIEW vcu_scope.replay_v", 0))
		q := "CREATE OR REPLACE VIEW vcu_renamed.renamed_v AS SELECT missing FROM vcu_scope.u"
		d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		d.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "missing")]) + 1)
		run(fixture.ErrorDiagnostic(q, d))
		check(want)
		run(fixture.Select(definitionQuery, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}}))
		run(fixture.Exec("CREATE OR REPLACE VIEW vcu_renamed.renamed_v AS SELECT a,a AS b,a AS again FROM vcu_scope.u", 0))
		want = want[2:]
		check(want)
		run(fixture.Exec("DROP TABLE vcu_renamed.bound_t", 0))
		check(want)
		run(fixture.Exec("DROP VIEW vcu_scope.w", 0))
		check(want[:1])
		run(fixture.Exec("DROP VIEW vcu_renamed.renamed_v", 0))
		check(nil)
		run(fixture.Exec("CREATE VIEW vcu_renamed.renamed_v AS SELECT a FROM vcu_scope.t", 0))
		check([][]any{vcuRow(database, "vcu_renamed", "renamed_v", "vcu_scope", "t", "a")})
		run(fixture.Exec("CREATE VIEW vcu_scope.w AS SELECT a FROM vcu_renamed.renamed_v", 0))
		run(fixture.Exec("CREATE VIEW vcu_scope.keep_v AS SELECT a FROM vcu_scope.u", 0))
		run(fixture.Exec("DROP TABLE vcu_scope.t CASCADE", 0))
		check([][]any{vcuRow(database, "vcu_scope", "keep_v", "vcu_scope", "u", "a")})
	})
}

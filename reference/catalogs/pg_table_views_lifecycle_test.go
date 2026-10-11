package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

const pgViewProbe = "SELECT * FROM pg_catalog.pg_views WHERE schemaname=$1 AND viewname=$2"

// Definitions are executable structural reconstruction, not a whitespace golden.
// Identity, shape, raw transport, cardinality, demanded names and actual replay
// are independently checked; the fixture recorder also owns its complete stream.
func pgViewSummary(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, schema, name, owner string, required ...string) string {
	t.Helper()
	rows, err := db.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), pgViewProbe, schema, name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	pgTableViewMetadata(t, types, pgViewColumns, pgViewTypes, b.PG)
	if !rows.Next() {
		t.Fatal("missing view summary", rows.Err())
	}
	var raw [4]any
	if err := rows.Scan(&raw[0], &raw[1], &raw[2], &raw[3]); err != nil {
		t.Fatal(err)
	}
	normalized := make([]any, 4)
	for i, v := range raw {
		fixture.AssertRawValue(t, b.PG, pgViewTypes[i], v)
		normalized[i] = fixture.Normalize(v)
	}
	if !reflect.DeepEqual(normalized[:3], []any{schema, name, owner}) {
		t.Fatal("view identity", normalized)
	}
	definition, ok := normalized[3].(string)
	if !ok || !strings.HasSuffix(definition, ";") {
		t.Fatal("definition", normalized[3])
	}
	for _, text := range required {
		if !strings.Contains(definition, text) {
			t.Fatalf("definition missing %q: %s", text, definition)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra view summary", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.ViewRecorded(t, b.Recorder, pgViewProbe, pgViewColumns, pgViewTypes, [][]any{normalized}, false)
	return definition
}
func pgTableRetained(t *testing.T, rows *sql.Rows, types []*sql.ColumnType, want []any) {
	t.Helper()
	defer rows.Close()
	pgTableViewMetadata(t, types, pgTableColumns, pgTableTypes, false)
	if !rows.Next() {
		t.Fatal("retained table row missing", rows.Err())
	}
	got := make([]any, 8)
	dest := make([]any, 8)
	for i := range dest {
		dest[i] = &got[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	for i, v := range got {
		fixture.AssertRawValue(t, false, pgTableTypes[i], v)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained cardinality", rows.Err())
	}
	pgTableViewMetadata(t, types, pgTableColumns, pgTableTypes, false)
}

func TestReferencePGTableViewsLifecycle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgtv_owner", "pgtv Quoted Owner"})
	fixture.SchemaReferenceCleanup(t, "pgtv_moved", "pgtv Quoted Schema")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(q string) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0)) }
			for _, q := range []string{"CREATE ROLE pgtv_owner", "CREATE SCHEMA pgtv_moved", "CREATE TABLE pgtv_life(n INT)", "CREATE VIEW pgtv_life_view AS SELECT n FROM pgtv_life"} {
				run(q)
			}
			pgTableCheck(t, p, b, b.DB, "pgtv_life", pgTableRow("public", "pgtv_life", pgTableViewsOwner(b), false))
			pgViewSummary(t, p, b, b.DB, "public", "pgtv_life_view", pgTableViewsOwner(b), "pgtv_life", "n")
			for _, q := range []string{"CREATE INDEX pgtv_life_index ON pgtv_life(n)", "DROP INDEX pgtv_life_index", "ALTER TABLE pgtv_life RENAME TO pgtv_renamed", "ALTER TABLE pgtv_renamed RENAME COLUMN n TO renamed_n", "ALTER TABLE pgtv_renamed SET SCHEMA pgtv_moved"} {
				run(q)
			}
			tableOwner := pgTableViewsOwner(b)
			ownerSQL := "ALTER TABLE pgtv_moved.pgtv_renamed OWNER TO pgtv_owner"
			if b.PG {
				run(ownerSQL)
				tableOwner = "pgtv_owner"
			} else {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(ownerSQL, fixture.Diagnostic{Code: "0A000", Message: "ALTER TABLE action is unsupported", Position: "37"})
					s.Prepared = prepared
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
			for _, q := range []string{"ALTER VIEW pgtv_life_view RENAME TO pgtv_renamed_view", "ALTER VIEW pgtv_renamed_view SET SCHEMA pgtv_moved", "ALTER VIEW pgtv_moved.pgtv_renamed_view OWNER TO pgtv_owner"} {
				run(q)
			}
			pgTableCheck(t, p, b, b.DB, "pgtv_life", nil)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgTableViewSelect(b, "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='pgtv_moved' AND tablename='pgtv_renamed'", pgTableColumns, pgTableTypes, [][]any{pgTableRow("pgtv_moved", "pgtv_renamed", tableOwner, true)}))
			pgViewSummary(t, p, b, b.DB, "pgtv_moved", "pgtv_renamed_view", "pgtv_owner", "pgtv_moved", "pgtv_renamed")
			run("CREATE OR REPLACE VIEW pgtv_moved.pgtv_renamed_view AS SELECT renamed_n+1 AS n FROM pgtv_moved.pgtv_renamed")
			pgViewSummary(t, p, b, b.DB, "pgtv_moved", "pgtv_renamed_view", "pgtv_owner", "renamed_n", "+ 1")
			run("DROP TABLE pgtv_moved.pgtv_renamed CASCADE")
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgTableViewSelect(b, "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='pgtv_moved'", pgTableColumns, pgTableTypes, nil))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgTableViewSelect(b, "SELECT * FROM pg_catalog.pg_views WHERE schemaname='pgtv_moved'", pgViewColumns, pgViewTypes, nil))
			for _, q := range []string{`CREATE ROLE "pgtv Quoted Owner"`, `CREATE SCHEMA "pgtv Quoted Schema" AUTHORIZATION "pgtv Quoted Owner"`, `SET SESSION AUTHORIZATION "pgtv Quoted Owner"`, `CREATE TABLE "pgtv Quoted Schema"."Odd Table"(n INT)`, `CREATE VIEW "pgtv Quoted Schema"."Odd View" AS SELECT n FROM "pgtv Quoted Schema"."Odd Table"`, "RESET SESSION AUTHORIZATION"} {
				run(q)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, pgTableViewSelect(b, "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='pgtv Quoted Schema'", pgTableColumns, pgTableTypes, [][]any{pgTableRow("pgtv Quoted Schema", "Odd Table", "pgtv Quoted Owner", false)}))
			pgViewSummary(t, p, b, b.DB, "pgtv Quoted Schema", "Odd View", "pgtv Quoted Owner", `"pgtv Quoted Schema"`, `"Odd Table"`)
		})
	}
}

func TestReferencePGTableViewsPreparedRetention(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(q string) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0)) }
			held := fixture.ViewHold(t, p, b, pgTableProbe)
			viewHeld := fixture.ViewHold(t, p, b, pgViewProbe)
			pgTableCheck(t, p, b, held, "pgtv_retained", nil)
			run("CREATE TABLE pgtv_retained(n INT)")
			run("CREATE VIEW pgtv_retained_view AS SELECT n FROM pgtv_retained")
			original := pgTableRow("public", "pgtv_retained", pgTableViewsOwner(b), false)
			pgTableCheck(t, p, b, held, "pgtv_retained", original)
			originalDefinition := pgViewSummary(t, p, b, viewHeld, "public", "pgtv_retained_view", pgTableViewsOwner(b), "pgtv_retained")
			var old *sql.Rows
			var descriptions []*sql.ColumnType
			if !b.PG {
				var err error
				old, err = b.DB.QueryContext(p.Ctx, pgTableProbe, "pgtv_retained")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				descriptions, err = old.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
			}
			var oldView *sql.Rows
			var viewDescriptions []*sql.ColumnType
			if !b.PG {
				var err error
				oldView, err = b.DB.QueryContext(p.Ctx, pgViewProbe, "public", "pgtv_retained_view")
				if err != nil {
					t.Fatal(err)
				}
				defer oldView.Close()
				viewDescriptions, err = oldView.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
			}
			run("CREATE INDEX pgtv_retained_index ON pgtv_retained(n)")
			pgTableCheck(t, p, b, held, "pgtv_retained", pgTableRow("public", "pgtv_retained", pgTableViewsOwner(b), true))
			run("ALTER TABLE pgtv_retained RENAME TO pgtv_retained_new")
			pgTableCheck(t, p, b, held, "pgtv_retained", nil)
			pgTableCheck(t, p, b, held, "pgtv_retained_new", pgTableRow("public", "pgtv_retained_new", pgTableViewsOwner(b), true))
			run("CREATE OR REPLACE VIEW pgtv_retained_view AS SELECT n+2 AS n FROM pgtv_retained_new")
			pgViewSummary(t, p, b, viewHeld, "public", "pgtv_retained_view", pgTableViewsOwner(b), "pgtv_retained_new", "+ 2")
			run("DROP TABLE pgtv_retained_new CASCADE")
			pgTableCheck(t, p, b, held, "pgtv_retained_new", nil)
			fixture.RunStep(t, p.Ctx, viewHeld, b.Recorder, pgTableViewSelect(b, pgViewProbe, pgViewColumns, pgViewTypes, nil, "public", "pgtv_retained_view"))
			if old != nil {
				pgTableRetained(t, old, descriptions, original)
			}
			if oldView != nil {
				pgTableViewMetadata(t, viewDescriptions, pgViewColumns, pgViewTypes, false)
				if !oldView.Next() {
					t.Fatal("retained view missing", oldView.Err())
				}
				var got [4]any
				if err := oldView.Scan(&got[0], &got[1], &got[2], &got[3]); err != nil {
					t.Fatal(err)
				}
				for i, v := range got {
					fixture.AssertRawValue(t, false, pgViewTypes[i], v)
				}
				if !reflect.DeepEqual(got, [4]any{"public", "pgtv_retained_view", pgTableViewsOwner(b), originalDefinition}) {
					t.Fatal("retained definition changed", got)
				}
				if oldView.Next() || oldView.Err() != nil {
					t.Fatal("retained view cardinality", oldView.Err())
				}
				pgTableViewMetadata(t, viewDescriptions, pgViewColumns, pgViewTypes, false)
			}
		})
	}
}

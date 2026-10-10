package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

const pgViewProbe = "SELECT * FROM pg_catalog.pg_views WHERE schemaname=$1 AND viewname=$2"

// Definitions are executable structural reconstruction, not a whitespace golden.
// Identity, shape, raw transport, cardinality, demanded names and actual replay
// are independently checked; the fixture recorder also owns its complete stream.
func pgViewSummary(t *testing.T, p *referencePair, b referenceBackend, db referenceExecutor, schema, name, owner string, required ...string) string {
	t.Helper()
	rows, err := db.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder), pgViewProbe, schema, name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	pgTableViewMetadata(t, types, pgViewColumns, pgViewTypes, b.pg)
	if !rows.Next() {
		t.Fatal("missing view summary", rows.Err())
	}
	var raw [4]any
	if err := rows.Scan(&raw[0], &raw[1], &raw[2], &raw[3]); err != nil {
		t.Fatal(err)
	}
	normalized := make([]any, 4)
	for i, v := range raw {
		referenceAssertRawValue(t, b.pg, pgViewTypes[i], v)
		normalized[i] = referenceNormalize(v)
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
	referenceViewRecorded(t, b.recorder, pgViewProbe, pgViewColumns, pgViewTypes, [][]any{normalized}, false)
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
		referenceAssertRawValue(t, false, pgTableTypes[i], v)
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
	roleReferenceCleanup(t, []string{"pgtv_owner", "pgtv Quoted Owner"})
	schemaReferenceCleanup(t, "pgtv_moved", "pgtv Quoted Schema")
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(q string) { referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(q, 0)) }
			for _, q := range []string{"CREATE ROLE pgtv_owner", "CREATE SCHEMA pgtv_moved", "CREATE TABLE pgtv_life(n INT)", "CREATE VIEW pgtv_life_view AS SELECT n FROM pgtv_life"} {
				run(q)
			}
			pgTableCheck(t, p, b, b.db, "pgtv_life", pgTableRow("public", "pgtv_life", pgTableViewsOwner(b), false))
			pgViewSummary(t, p, b, b.db, "public", "pgtv_life_view", pgTableViewsOwner(b), "pgtv_life", "n")
			for _, q := range []string{"CREATE INDEX pgtv_life_index ON pgtv_life(n)", "DROP INDEX pgtv_life_index", "ALTER TABLE pgtv_life RENAME TO pgtv_renamed", "ALTER TABLE pgtv_renamed RENAME COLUMN n TO renamed_n", "ALTER TABLE pgtv_renamed SET SCHEMA pgtv_moved"} {
				run(q)
			}
			tableOwner := pgTableViewsOwner(b)
			ownerSQL := "ALTER TABLE pgtv_moved.pgtv_renamed OWNER TO pgtv_owner"
			if b.pg {
				run(ownerSQL)
				tableOwner = "pgtv_owner"
			} else {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(ownerSQL, referenceDiagnostic{Code: "0A000", Message: "ALTER TABLE action is unsupported", Position: "37"})
					s.prepared = prepared
					if prepared {
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				}
			}
			for _, q := range []string{"ALTER VIEW pgtv_life_view RENAME TO pgtv_renamed_view", "ALTER VIEW pgtv_renamed_view SET SCHEMA pgtv_moved", "ALTER VIEW pgtv_moved.pgtv_renamed_view OWNER TO pgtv_owner"} {
				run(q)
			}
			pgTableCheck(t, p, b, b.db, "pgtv_life", nil)
			referenceRunStep(t, p.ctx, b.db, b.recorder, pgTableViewSelect(b, "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='pgtv_moved' AND tablename='pgtv_renamed'", pgTableColumns, pgTableTypes, [][]any{pgTableRow("pgtv_moved", "pgtv_renamed", tableOwner, true)}))
			pgViewSummary(t, p, b, b.db, "pgtv_moved", "pgtv_renamed_view", "pgtv_owner", "pgtv_moved", "pgtv_renamed")
			run("CREATE OR REPLACE VIEW pgtv_moved.pgtv_renamed_view AS SELECT renamed_n+1 AS n FROM pgtv_moved.pgtv_renamed")
			pgViewSummary(t, p, b, b.db, "pgtv_moved", "pgtv_renamed_view", "pgtv_owner", "renamed_n", "+ 1")
			run("DROP TABLE pgtv_moved.pgtv_renamed CASCADE")
			referenceRunStep(t, p.ctx, b.db, b.recorder, pgTableViewSelect(b, "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='pgtv_moved'", pgTableColumns, pgTableTypes, nil))
			referenceRunStep(t, p.ctx, b.db, b.recorder, pgTableViewSelect(b, "SELECT * FROM pg_catalog.pg_views WHERE schemaname='pgtv_moved'", pgViewColumns, pgViewTypes, nil))
			for _, q := range []string{`CREATE ROLE "pgtv Quoted Owner"`, `CREATE SCHEMA "pgtv Quoted Schema" AUTHORIZATION "pgtv Quoted Owner"`, `SET SESSION AUTHORIZATION "pgtv Quoted Owner"`, `CREATE TABLE "pgtv Quoted Schema"."Odd Table"(n INT)`, `CREATE VIEW "pgtv Quoted Schema"."Odd View" AS SELECT n FROM "pgtv Quoted Schema"."Odd Table"`, "RESET SESSION AUTHORIZATION"} {
				run(q)
			}
			referenceRunStep(t, p.ctx, b.db, b.recorder, pgTableViewSelect(b, "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='pgtv Quoted Schema'", pgTableColumns, pgTableTypes, [][]any{pgTableRow("pgtv Quoted Schema", "Odd Table", "pgtv Quoted Owner", false)}))
			pgViewSummary(t, p, b, b.db, "pgtv Quoted Schema", "Odd View", "pgtv Quoted Owner", `"pgtv Quoted Schema"`, `"Odd Table"`)
		})
	}
}

func TestReferencePGTableViewsPreparedRetention(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(q string) { referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(q, 0)) }
			held := referenceViewHold(t, p, b, pgTableProbe)
			viewHeld := referenceViewHold(t, p, b, pgViewProbe)
			pgTableCheck(t, p, b, held, "pgtv_retained", nil)
			run("CREATE TABLE pgtv_retained(n INT)")
			run("CREATE VIEW pgtv_retained_view AS SELECT n FROM pgtv_retained")
			original := pgTableRow("public", "pgtv_retained", pgTableViewsOwner(b), false)
			pgTableCheck(t, p, b, held, "pgtv_retained", original)
			originalDefinition := pgViewSummary(t, p, b, viewHeld, "public", "pgtv_retained_view", pgTableViewsOwner(b), "pgtv_retained")
			var old *sql.Rows
			var descriptions []*sql.ColumnType
			if !b.pg {
				var err error
				old, err = b.db.QueryContext(p.ctx, pgTableProbe, "pgtv_retained")
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
			if !b.pg {
				var err error
				oldView, err = b.db.QueryContext(p.ctx, pgViewProbe, "public", "pgtv_retained_view")
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
			referenceRunStep(t, p.ctx, viewHeld, b.recorder, pgTableViewSelect(b, pgViewProbe, pgViewColumns, pgViewTypes, nil, "public", "pgtv_retained_view"))
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
					referenceAssertRawValue(t, false, pgViewTypes[i], v)
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

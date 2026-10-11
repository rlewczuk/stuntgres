package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryTableCreateNamespaces(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			fixture.QueryTableCreateSource(run)
			run(fixture.ImplicitError("CREATE TABLE qc_absent_schema.qc_dest AS SELECT i FROM qc_source", fixture.Diagnostic{Code: "3F000", Message: `schema "qc_absent_schema" does not exist`}))
			run(fixture.Exec("SET search_path TO ''", 0))
			run(fixture.ImplicitError("CREATE TABLE qc_dest AS SELECT 1 AS i", fixture.Diagnostic{Code: "3F000", Message: "no schema has been selected to create in"}))
			run(fixture.Exec("RESET search_path", 0))
			fixture.QueryTableCreateAbsent(t, run, b.PG, "qc_dest")
			run(fixture.Exec("CREATE TYPE qc_type_collision AS ENUM('x')", 0))
			want := fixture.ControlsReferenceDiagnostic("42710", `type "qc_type_collision" already exists`)
			want.Hint = "A relation has an associated type of the same name, so you must use a name that doesn't conflict with any existing type."
			run(fixture.ErrorDiagnostic("CREATE TABLE qc_type_collision AS SELECT 1 AS i", want))
			run(fixture.Select("SELECT relname AS n FROM pg_class WHERE relname='qc_type_collision'", []string{"n"}, []string{"NAME"}, nil))
			run(fixture.Select("SELECT 'x'::qc_type_collision::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"x"}}))
			run(fixture.QueryTableCreateBag("qc_source"))
		})
	}
}

func TestReferenceQueryTableCreateRelationNamespaceSkip(t *testing.T) {
	for _, kind := range []string{"view", "sequence", "index"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, conn, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, record, s) }
					fixture.QueryTableCreateSource(run)
					switch kind {
					case "view":
						run(fixture.Exec("CREATE VIEW qc_occupied AS SELECT i,s FROM qc_source", 0))
					case "sequence":
						run(fixture.Exec("CREATE SEQUENCE qc_occupied START 10", 0))
					case "index":
						run(fixture.Exec("CREATE INDEX qc_occupied ON qc_source(i)", 0))
					}
					run(fixture.Exec("CREATE TABLE IF NOT EXISTS qc_occupied AS SELECT 1/0 AS x", 0))
					want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "qc_occupied" already exists, skipping`}
					if len(notices) != 1 || notices[0] != want {
						t.Fatalf("namespace skip notices %#v want %#v", notices, want)
					}
					run(fixture.Select("SELECT relkind AS k FROM pg_class WHERE relname='qc_occupied'", []string{"k"}, []string{"CHAR"}, [][]any{{map[string]string{"view": "v", "sequence": "S", "index": "i"}[kind]}}))
					if kind == "view" {
						run(fixture.QueryTableCreateBag("qc_occupied"))
					}
					if kind == "sequence" {
						run(fixture.Select("SELECT nextval('qc_occupied') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
					}
					run(fixture.QueryTableCreateBag("qc_source"))
				})
			}
		})
	}
}

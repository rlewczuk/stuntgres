package reference_test

import "testing"

func TestReferenceQueryTableCreateNamespaces(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			_, run := queryTableCreateSession(t, p, b)
			queryTableCreateSource(run)
			run(referenceImplicitError("CREATE TABLE qc_absent_schema.qc_dest AS SELECT i FROM qc_source", referenceDiagnostic{Code: "3F000", Message: `schema "qc_absent_schema" does not exist`}))
			run(referenceExec("SET search_path TO ''", 0))
			run(referenceImplicitError("CREATE TABLE qc_dest AS SELECT 1 AS i", referenceDiagnostic{Code: "3F000", Message: "no schema has been selected to create in"}))
			run(referenceExec("RESET search_path", 0))
			queryTableCreateAbsent(t, run, b.pg, "qc_dest")
			run(referenceExec("CREATE TYPE qc_type_collision AS ENUM('x')", 0))
			want := controlsReferenceDiagnostic("42710", `type "qc_type_collision" already exists`)
			want.Hint = "A relation has an associated type of the same name, so you must use a name that doesn't conflict with any existing type."
			run(referenceErrorDiagnostic("CREATE TABLE qc_type_collision AS SELECT 1 AS i", want))
			run(referenceSelect("SELECT relname AS n FROM pg_class WHERE relname='qc_type_collision'", []string{"n"}, []string{"NAME"}, nil))
			run(referenceSelect("SELECT 'x'::qc_type_collision::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"x"}}))
			run(queryTableCreateBag("qc_source"))
		})
	}
}

func TestReferenceQueryTableCreateRelationNamespaceSkip(t *testing.T) {
	for _, kind := range []string{"view", "sequence", "index"} {
		t.Run(kind, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					var notices []referenceDiagnostic
					_, conn, record := noticeReferencePool(t, p, b.pg, &notices)
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, record, s) }
					queryTableCreateSource(run)
					switch kind {
					case "view":
						run(referenceExec("CREATE VIEW qc_occupied AS SELECT i,s FROM qc_source", 0))
					case "sequence":
						run(referenceExec("CREATE SEQUENCE qc_occupied START 10", 0))
					case "index":
						run(referenceExec("CREATE INDEX qc_occupied ON qc_source(i)", 0))
					}
					run(referenceExec("CREATE TABLE IF NOT EXISTS qc_occupied AS SELECT 1/0 AS x", 0))
					want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "qc_occupied" already exists, skipping`}
					if len(notices) != 1 || notices[0] != want {
						t.Fatalf("namespace skip notices %#v want %#v", notices, want)
					}
					run(referenceSelect("SELECT relkind AS k FROM pg_class WHERE relname='qc_occupied'", []string{"k"}, []string{"CHAR"}, [][]any{{map[string]string{"view": "v", "sequence": "S", "index": "i"}[kind]}}))
					if kind == "view" {
						run(queryTableCreateBag("qc_occupied"))
					}
					if kind == "sequence" {
						run(referenceSelect("SELECT nextval('qc_occupied') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
					}
					run(queryTableCreateBag("qc_source"))
				})
			}
		})
	}
}

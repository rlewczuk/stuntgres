package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExplainJSONFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			explainJSONSetup(t, p, b)
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step) }
			for _, query := range []string{"CREATE SEQUENCE ej_smoke_s START 1", "CREATE TABLE ej_smoke_default(id integer DEFAULT nextval('ej_smoke_s'))"} {
				run(fixture.Exec(query, 0))
			}
			const query = "EXPLAIN (FORMAT JSON,VERBOSE,COSTS false) SELECT id FROM explain_json WHERE id>$1 ORDER BY id DESC"
			held, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			var retained explainJSONDocument
			var snapshot string
			for _, bound := range []int64{1, 2} {
				for _, executor := range []fixture.Executor{b.DB, fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: query}} {
					document, _ := explainJSONQuery(t, p, executor, query, bound)
					explainJSONOrdinary(t, b, document)
					explainJSONAbsent(t, document, "Plan Rows", "Logical Cost", "Startup Cost", "Total Cost", "Plan Width")
					explainJSONFind(t, document, "Relation Name", "explain_json")
					filterValue := fmt.Sprint(bound)
					if !b.PG {
						filterValue = "$1"
					}
					explainJSONContains(t, document, "Output", "Sort Key", "DESC", "Filter", filterValue)
					if retained == nil {
						retained = document
						snapshot = fmt.Sprint(document)
					}
					if fmt.Sprint(retained) != snapshot {
						t.Fatal("retained prepared document changed")
					}
				}
			}
			unchanged := func() {
				run(fixture.Select("SELECT id FROM ej_smoke_default", []string{"id"}, []string{"INT4"}, nil))
				run(fixture.Select("SELECT last_value,is_called FROM ej_smoke_s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
				run(fixture.Select("SELECT id,v FROM explain_json ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
			}
			for _, child := range []string{"INSERT INTO ej_smoke_default DEFAULT VALUES", "WITH w AS (INSERT INTO ej_smoke_default DEFAULT VALUES RETURNING id) SELECT * FROM w"} {
				explainJSONRoutes(t, p, b, "EXPLAIN (FORMAT JSON,VERBOSE,COSTS false) "+child, func(document explainJSONDocument, _ string) { explainJSONOrdinary(t, b, document); unchanged() })
			}
			document, _ := explainJSONQuery(t, p, b.DB, "EXPLAIN (FORMAT JSON,ANALYZE,TIMING false,SUMMARY false) VALUES(1),(2),(3)")
			explainJSONActual(t, b, document, 3, false, false)
			for _, prepared := range []bool{false, true} {
				buffers := "EXPLAIN (FORMAT JSON,BUFFERS) SELECT id FROM explain_json"
				if !b.PG {
					want := fixture.ControlsReferenceDiagnostic("0A000", "EXPLAIN option BUFFERS is not supported")
					want.Position = fixture.Position(strings.Index(buffers, "BUFFERS") + 1)
					step := fixture.ErrorDiagnostic(buffers, want)
					step.Prepared, step.SelectRows, step.ErrorStage = prepared, true, "execute"
					run(step)
				} else {
					rows := mustExplainCapabilityRows(t, p, b.DB, buffers, prepared)
					explainJSONRows(t, rows)
				}
				step := fixture.ImplicitError("EXPLAIN (FORMAT JSON,WAL) SELECT id FROM explain_json", fixture.Diagnostic{Code: "22023", Message: "EXPLAIN option WAL requires ANALYZE"})
				step.Prepared, step.SelectRows, step.ErrorStage = prepared, true, "execute"
				run(step)
				unchanged()
			}
		})
	}
}

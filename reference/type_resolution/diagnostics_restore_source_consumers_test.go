package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceErrorFieldsRestoreSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.DriverExecSQL(t, db, "CREATE TABLE diagnostic_rows (id INT NOT NULL,note TEXT)")
			fixture.DriverExecSQL(t, db, "INSERT INTO diagnostic_rows VALUES (7,'kept')")
			var dump bytes.Buffer
			if !backend.PG {
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			want := fixture.ControlsReferenceDiagnostic("23502", `null value in column "id" of relation "diagnostic_rows" violates not-null constraint`)
			want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (null, bad).", "public", "diagnostic_rows", "id"
			result, err := db.Exec("INSERT INTO diagnostic_rows VALUES (NULL,'bad')")
			if result != nil {
				t.Fatal("failed postrestore Exec exposed result")
			}
			fixture.AssertDiagnostic(t, err, &want)
			kept := [][]any{{int64(7), "kept"}}
			fixture.DriverWantTuples(t, db, "SELECT id,note FROM diagnostic_rows ORDER BY id", kept)
			if !backend.PG {
				for _, tc := range []struct {
					script string
					want   fixture.Diagnostic
				}{
					{"CREATE TABLE replacement (x TEXT); INSERT INTO replacement VALUES ('é', )", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: "73"}},
					{"CREATE TABLE staged (id INT NOT NULL); INSERT INTO staged VALUES (NULL)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "id" of relation "staged" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "staged", Column: "id"}},
					{"CREATE TABLE staged (id INT); INSERT INTO staged VALUES ($1)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P02", Message: "parameters are not allowed in restore", Position: "58"}},
				} {
					fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(tc.script)), &tc.want)
					fixture.DriverWantTuples(t, db, "SELECT id,note FROM diagnostic_rows ORDER BY id", kept)
				}
			}
			result, err = db.Exec("INSERT INTO diagnostic_rows VALUES (NULL,'bad')")
			if result != nil {
				t.Fatal("failed final Exec exposed result")
			}
			fixture.AssertDiagnostic(t, err, &want)
			fixture.DriverWantTuples(t, db, "SELECT id,note FROM diagnostic_rows ORDER BY id", kept)
		})
	}
}

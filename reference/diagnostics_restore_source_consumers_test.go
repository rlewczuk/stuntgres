package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
)

func TestReferenceErrorFieldsRestoreSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			driverExecSQL(t, db, "CREATE TABLE diagnostic_rows (id INT NOT NULL,note TEXT)")
			driverExecSQL(t, db, "INSERT INTO diagnostic_rows VALUES (7,'kept')")
			var dump bytes.Buffer
			if !backend.pg {
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			want := controlsReferenceDiagnostic("23502", `null value in column "id" of relation "diagnostic_rows" violates not-null constraint`)
			want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (null, bad).", "public", "diagnostic_rows", "id"
			result, err := db.Exec("INSERT INTO diagnostic_rows VALUES (NULL,'bad')")
			if result != nil {
				t.Fatal("failed postrestore Exec exposed result")
			}
			referenceAssertDiagnostic(t, err, &want)
			kept := [][]any{{int64(7), "kept"}}
			driverWantTuples(t, db, "SELECT id,note FROM diagnostic_rows ORDER BY id", kept)
			if !backend.pg {
				for _, tc := range []struct {
					script string
					want   referenceDiagnostic
				}{
					{"CREATE TABLE replacement (x TEXT); INSERT INTO replacement VALUES ('é', )", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: "73"}},
					{"CREATE TABLE staged (id INT NOT NULL); INSERT INTO staged VALUES (NULL)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "id" of relation "staged" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "staged", Column: "id"}},
					{"CREATE TABLE staged (id INT); INSERT INTO staged VALUES ($1)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P02", Message: "parameters are not allowed in restore", Position: "58"}},
				} {
					referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, bytes.NewBufferString(tc.script)), &tc.want)
					driverWantTuples(t, db, "SELECT id,note FROM diagnostic_rows ORDER BY id", kept)
				}
			}
			result, err = db.Exec("INSERT INTO diagnostic_rows VALUES (NULL,'bad')")
			if result != nil {
				t.Fatal("failed final Exec exposed result")
			}
			referenceAssertDiagnostic(t, err, &want)
			driverWantTuples(t, db, "SELECT id,note FROM diagnostic_rows ORDER BY id", kept)
		})
	}
}

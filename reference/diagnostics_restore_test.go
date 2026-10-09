package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
)

func TestReferenceErrorFieldsRestore(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE diagnostic_rows (id INT NOT NULL,note TEXT)", 0))
			run(referenceExec("INSERT INTO diagnostic_rows VALUES (7,'kept')", 1))
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
				var redump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
					t.Fatalf("roundtrip dump changed:\n%s\n%s", dump.Bytes(), redump.Bytes())
				}
			}
			notNull := controlsReferenceDiagnostic("23502", `null value in column "id" of relation "diagnostic_rows" violates not-null constraint`)
			notNull.Detail, notNull.Schema, notNull.Table, notNull.Column = "Failing row contains (null, bad).", "public", "diagnostic_rows", "id"
			run(referenceErrorDiagnostic("INSERT INTO diagnostic_rows VALUES (NULL,'bad')", notNull))
			kept := referenceSelect("SELECT id,note FROM diagnostic_rows ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}})
			run(kept)
			if !backend.pg {
				for _, tc := range []struct {
					script string
					want   referenceDiagnostic
				}{
					{"CREATE TABLE replacement (x TEXT); INSERT INTO replacement VALUES ('é', )", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: "73"}},
					{"CREATE TABLE staged (id INT NOT NULL); INSERT INTO staged VALUES (NULL)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "id" of relation "staged" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "staged", Column: "id"}},
					{"CREATE TABLE staged (id INT); INSERT INTO staged VALUES ($1)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P02", Message: "parameters are not allowed in restore", Position: "58"}},
				} {
					err := stuntgres.Restore(p.ctx, p.native, bytes.NewBufferString(tc.script))
					referenceState(t, err, tc.want.Code)
					referenceAssertDiagnostic(t, err, &tc.want)
					run(kept)
					for _, name := range []string{"replacement", "staged"} {
						s := referenceError("SELECT * FROM "+name, "42P01")
						s.selectRows = true
						run(s)
					}
					var after bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(dump.Bytes(), after.Bytes()) {
						t.Fatal("failed Restore changed serialized catalog")
					}
				}
			}
			run(referenceErrorDiagnostic("INSERT INTO diagnostic_rows VALUES (NULL,'bad')", notNull))
			run(kept)
		})
	}
}

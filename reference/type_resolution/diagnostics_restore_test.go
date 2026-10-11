package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceErrorFieldsRestore(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE diagnostic_rows (id INT NOT NULL,note TEXT)", 0))
			run(fixture.Exec("INSERT INTO diagnostic_rows VALUES (7,'kept')", 1))
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
				var redump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
					t.Fatalf("roundtrip dump changed:\n%s\n%s", dump.Bytes(), redump.Bytes())
				}
			}
			notNull := fixture.ControlsReferenceDiagnostic("23502", `null value in column "id" of relation "diagnostic_rows" violates not-null constraint`)
			notNull.Detail, notNull.Schema, notNull.Table, notNull.Column = "Failing row contains (null, bad).", "public", "diagnostic_rows", "id"
			run(fixture.ErrorDiagnostic("INSERT INTO diagnostic_rows VALUES (NULL,'bad')", notNull))
			kept := fixture.Select("SELECT id,note FROM diagnostic_rows ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}})
			run(kept)
			if !backend.PG {
				for _, tc := range []struct {
					script string
					want   fixture.Diagnostic
				}{
					{"CREATE TABLE replacement (x TEXT); INSERT INTO replacement VALUES ('é', )", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near ")"`, Position: "73"}},
					{"CREATE TABLE staged (id INT NOT NULL); INSERT INTO staged VALUES (NULL)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "id" of relation "staged" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "staged", Column: "id"}},
					{"CREATE TABLE staged (id INT); INSERT INTO staged VALUES ($1)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P02", Message: "parameters are not allowed in restore", Position: "58"}},
				} {
					err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(tc.script))
					fixture.State(t, err, tc.want.Code)
					fixture.AssertDiagnostic(t, err, &tc.want)
					run(kept)
					for _, name := range []string{"replacement", "staged"} {
						s := fixture.Error("SELECT * FROM "+name, "42P01")
						s.SelectRows = true
						run(s)
					}
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(dump.Bytes(), after.Bytes()) {
						t.Fatal("failed Restore changed serialized catalog")
					}
				}
			}
			run(fixture.ErrorDiagnostic("INSERT INTO diagnostic_rows VALUES (NULL,'bad')", notNull))
			run(kept)
		})
	}
}

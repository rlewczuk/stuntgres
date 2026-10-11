package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// SQL-level goldens retain Main's pinned PostgreSQL18.0 deparse and inference
// observations from phase A. Never derive expectations from native output.
func TestReferencePosixStoredDefinitionsInquiry(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec(`CREATE TABLE inquiry(v TEXT,p TEXT,CONSTRAINT valid CHECK(v ~ '^a'))`, 0))
			run(fixture.Exec(`INSERT INTO inquiry VALUES('abc','(b)')`, 1))
			run(fixture.Exec(`CREATE VIEW inquiry_view AS SELECT substring(v,p) AS piece FROM inquiry`, 0))
			run(fixture.Exec(`CREATE INDEX inquiry_idx ON inquiry ((substring(v,p))) WHERE v !~ 'z'`, 0))
			for _, c := range []struct{ q, w string }{
				{`SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conname='valid'`, `CHECK ((v ~ '^a'::text))`},
				{`SELECT pg_get_viewdef('inquiry_view'::regclass) AS definition`, " SELECT \"substring\"(v, p) AS piece\n   FROM inquiry;"},
				{`SELECT pg_get_indexdef('inquiry_idx'::regclass) AS definition`, `CREATE INDEX inquiry_idx ON public.inquiry USING btree ("substring"(v, p)) WHERE (v !~ 'z'::text)`},
			} {
				s := posixSelect(c.q, []string{"definition"}, []string{"TEXT"}, [][]any{{c.w}})
				s.CheckMetadata = similarReferenceDeclaredMetadata(b.PG)
				similarReferenceRoutes(t, p, b, s)
			}
			stmt, err := b.DB.PrepareContext(p.Ctx, `SELECT piece FROM inquiry_view`)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{b.DB, stmt, `SELECT piece FROM inquiry_view`}
			run(fixture.Exec(`ALTER TABLE inquiry RENAME COLUMN v TO value`, 0))
			want := fixture.Select(`SELECT piece FROM inquiry_view`, []string{"piece"}, []string{"TEXT"}, [][]any{{"b"}})
			want.CheckMetadata = similarReferenceDeclaredMetadata(b.PG)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
			run(fixture.Exec(`ALTER TABLE inquiry RENAME TO inquiry_renamed`, 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
			run(fixture.Exec(`CREATE TABLE retyped(v TEXT,p TEXT,m BOOLEAN GENERATED ALWAYS AS (v ~ p) STORED)`, 0))
			run(fixture.Exec(`INSERT INTO retyped(v,p) VALUES('abc','b')`, 1))
			// Existing generated-dependency exclusion, including pinned English detail.
			exclusion := fixture.ImplicitError(`ALTER TABLE retyped ALTER COLUMN v TYPE VARCHAR(8)`, fixture.Diagnostic{Code: "0A000", Message: "cannot alter type of a column used by a generated column", Detail: `Column "v" is used by generated column "m".`})
			exclusion.ErrorStage = "execute"
			for _, prepared := range []bool{false, true} {
				s := exclusion
				s.Prepared = prepared
				run(s)
			}
			similarReferenceRoutes(t, p, b, fixture.Select(`SELECT m FROM retyped`, []string{"m"}, []string{"BOOL"}, [][]any{{true}}))
			run(fixture.Exec(`CREATE TABLE retype_positive(v VARCHAR(8),p TEXT,CONSTRAINT valid_retype CHECK(v ~ '^a'))`, 0))
			run(fixture.Exec(`INSERT INTO retype_positive VALUES('abc','(b)')`, 1))
			run(fixture.Exec(`CREATE INDEX retype_idx ON retype_positive ((substring(v,p)))`, 0))
			run(fixture.Exec(`ALTER TABLE retype_positive ALTER COLUMN v TYPE TEXT USING v::text`, 0))
			similarReferenceRoutes(t, p, b, posixSelect(`SELECT v ~ p AS m,substring(v,p) AS piece FROM retype_positive`, []string{"m", "piece"}, []string{"BOOL", "TEXT"}, [][]any{{true, "b"}}))
		})
	}
}

func TestReferencePosixHeldParametersInferenceDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			// Inconsistent reuse is a Prepare diagnostic, not a runtime regex error.
			op := similarReferenceBindingError(`SELECT $1 ~ $2, $1 + 1`, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: text + integer", Hint: similarOperatorHint, Position: "20"})
			op.Args = []any{"a", "a"}
			similarReferenceRoutes(t, p, b, op)
			sub := similarReferenceBindingError(`SELECT substring($1,$2), $1 + 1`, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: text + integer", Hint: similarOperatorHint, Position: "29"})
			sub.Args = []any{"a", "a"}
			similarReferenceRoutes(t, p, b, sub)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE posix_def_t(id INT)`, 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO posix_def_t VALUES(1)`, 1))
			inference := similarReferenceBindingError(`SELECT $1,CAST($1 AS bpchar) ~ 'a' FROM posix_def_t`, fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character versus text", Position: "8"})
			inference.Args = []any{"a"}
			similarReferenceRoutes(t, p, b, inference)
		})
	}
}

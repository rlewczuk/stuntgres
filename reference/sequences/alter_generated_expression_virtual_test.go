package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func alterGeneratedVirtualDiagnostic(action string) fixture.Diagnostic {
	return fixture.Diagnostic{Code: "0A000", Message: "ALTER TABLE / " + action + " is not supported for virtual generated columns", Detail: `Column "b" of relation "g" is a virtual generated column.`}
}

func TestReferenceAlterGeneratedExpressionVirtual(t *testing.T) {
	for _, spelling := range []string{"VIRTUAL", ""} {
		t.Run("kind="+spelling, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) `+spelling+`)`, 0))
				run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
				run(fixture.Exec(`ALTER TABLE ONLY g ALTER b SET EXPRESSION AS(a+2)`, 0))
				run(alterGeneratedRows(`SELECT * FROM g`, []any{int64(2), int64(4)}))
				run(fixture.Prepared(fixture.Exec(`INSERT INTO g(a) VALUES(4)`, 1)))
				for _, suffix := range []string{"", " IF EXISTS"} {
					run(fixture.ImplicitError(`ALTER TABLE g ALTER b DROP EXPRESSION`+suffix, alterGeneratedVirtualDiagnostic("DROP EXPRESSION")))
					run(alterGeneratedRows(`SELECT * FROM g ORDER BY a`, []any{int64(2), int64(4)}, []any{int64(4), int64(6)}))
				}
				run(fixture.ImplicitError(`UPDATE g SET b=1`, fixture.Diagnostic{Code: "428C9", Message: `column "b" can only be updated to DEFAULT`, Detail: `Column "b" is a generated column.`}))
				run(fixture.Exec(`ALTER TABLE g ALTER b SET EXPRESSION AS(a/0)`, 0))
				run(fixture.Select(`SELECT a FROM g ORDER BY a`, []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(4)}}))
				s := fixture.ImplicitError(`SELECT b FROM g`, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.SelectRows = true
				run(s)
			})
		})
	}
	for _, check := range []string{"a>0", "b>0"} {
		t.Run("check="+check, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL,CONSTRAINT ck CHECK(`+check+`))`, 0))
				run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
				d := alterGeneratedVirtualDiagnostic("SET EXPRESSION")
				d.Message += " in tables with check constraints"
				for _, expr := range []string{"a+2", "-a"} {
					run(fixture.ImplicitError(`ALTER TABLE g ALTER b SET EXPRESSION AS(`+expr+`)`, d))
				}
				run(alterGeneratedRows(`SELECT * FROM g`, []any{int64(2), int64(3)}))
				run(fixture.Exec(`ALTER TABLE g ALTER b SET EXPRESSION AS(a+2), DROP CONSTRAINT ck`, 0))
				run(alterGeneratedRows(`SELECT * FROM g`, []any{int64(2), int64(4)}))
			})
		})
	}
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL NOT NULL)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
		run(fixture.ImplicitError(`ALTER TABLE g ALTER b SET EXPRESSION AS(nullif(a,2))`, fixture.Diagnostic{Code: "23502", Message: `column "b" of relation "g" contains null values`, Schema: "public", Table: "g", Column: "b"}))
		run(alterGeneratedRows(`SELECT * FROM g`, []any{int64(2), int64(3)}))
		run(fixture.Exec(`CREATE TABLE empty_g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL NOT NULL)`, 0))
		run(fixture.Exec(`ALTER TABLE empty_g ALTER b SET EXPRESSION AS(NULL::INT)`, 0))
		run(fixture.ImplicitError(`INSERT INTO empty_g(a) VALUES(2)`, fixture.Diagnostic{Code: "23502", Message: `null value in column "b" of relation "empty_g" violates not-null constraint`, Detail: "Failing row contains (2, virtual).", Schema: "public", Table: "empty_g", Column: "b"}))
	})
	for _, expr := range []string{"(a::vd)::INT", "CASE WHEN FALSE THEN (a::vd)::INT ELSE a END"} {
		t.Run(expr, func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec(`CREATE DOMAIN vd AS INT`, 0))
				run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
				q := `/*é*/ ALTER TABLE g ALTER b SET EXPRESSION AS(` + expr + `)`
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "generation expression uses user-defined type", Detail: "Virtual generated columns that make use of user-defined types are not yet supported."}))
			})
		})
	}
}

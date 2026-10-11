package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedStoredMixedConcatDefinition(t *testing.T) {
	for _, typ := range []string{"TIMESTAMP", "TIMESTAMPTZ"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", typ, prepared), func(t *testing.T) {
				generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
					q := `CREATE TABLE g(a ` + typ + `,b TEXT GENERATED ALWAYS AS('x'::text || a) STORED)`
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P17", Message: "generation expression is not immutable"})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
					run(fixture.ImplicitError(`SELECT * FROM g`, fixture.Diagnostic{Code: "42P01", Message: `relation "g" does not exist`, Position: "15"}))
					run(fixture.Exec(`CREATE TABLE g(a INT)`, 0))
					run(fixture.Exec(`INSERT INTO g VALUES(7)`, 1))
					run(fixture.Select(`SELECT * FROM g`, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			})
		}
	}
}

func TestReferenceGeneratedStoredMixedConcatPlanningControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
				exec := func(q string, n int64) { s := fixture.Exec(q, n); s.Prepared = prepared; run(s) }
				exec(`CREATE TABLE g(a INT,b TEXT GENERATED ALWAYS AS('x'::text || a) STORED,c TEXT GENERATED ALWAYS AS(CASE WHEN FALSE THEN 'x'::text || random() ELSE 'x'::text || a END) STORED)`, 0)
				exec(`INSERT INTO g(a) VALUES(2),(NULL)`, 2)
				run(fixture.Select(`SELECT * FROM g WHERE a=2`, []string{"a", "b", "c"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(2), "x2", "x2"}}))
				run(fixture.Select(`SELECT * FROM g WHERE a IS NULL`, []string{"a", "b", "c"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{nil, nil, nil}}))
				exec(`UPDATE g SET a=8 WHERE a=2`, 1)
				run(fixture.Select(`SELECT * FROM g WHERE a=8`, []string{"a", "b", "c"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(8), "x8", "x8"}}))
			})
		})
	}
}

func TestReferenceGeneratedStoredSameNameColumn(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
				exec := func(q string, n int64) { s := fixture.Exec(q, n); s.Prepared = prepared; run(s) }
				exec(`CREATE TABLE g(g INT,b INT GENERATED ALWAYS AS(g+1) STORED)`, 0)
				exec(`INSERT INTO g(g) VALUES(2),(NULL)`, 2)
				run(fixture.Select(`SELECT * FROM g WHERE g=2`, []string{"g", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}))
				run(fixture.Select(`SELECT * FROM g WHERE g IS NULL`, []string{"g", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}}))
				exec(`UPDATE g SET g=8 WHERE g=2`, 1)
				run(fixture.Select(`SELECT * FROM g WHERE g=8`, []string{"g", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(8), int64(9)}}))
			})
		})
	}
}

func TestReferenceGeneratedStoredSameNameReferenceDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, q, token, message, detail string }{
		{"generated-column", `CREATE TABLE g(a INT,g INT GENERATED ALWAYS AS(a+1) STORED,b INT GENERATED ALWAYS AS(g+1) STORED)`, "g+1", `cannot use generated column "g" in column generation expression`, "A generated column cannot reference another generated column."},
		{"whole-row", `CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(g) STORED)`, "g) STORED", "cannot use whole-row variable in column generation expression", "This would cause the generated column to depend on its own value."},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.name, prepared), func(t *testing.T) {
				generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, run func(fixture.Step)) {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42P17", Message: tc.message, Detail: tc.detail, Position: fixture.GeneratedStoredPosition(tc.q, tc.token)})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
					run(fixture.ImplicitError(`SELECT * FROM g`, fixture.Diagnostic{Code: "42P01", Message: `relation "g" does not exist`, Position: "15"}))
					run(fixture.Exec(`CREATE TABLE g(a INT)`, 0))
				})
			})
		}
	}
}

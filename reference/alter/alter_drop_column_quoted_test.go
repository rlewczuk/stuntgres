package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterDropColumnQuotedExpressionDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, definition, drop, projection, filter, message string }{
		{"quote", `"q""u"`, `ALTER TABLE quoted DROP COLUMN "q""u"`, `/*é*/ SELECT "q""u" FROM quoted`, `/*é*/ SELECT a FROM quoted WHERE "q""u"=1`, `column "q"u" does not exist`},
		{"backslash", `"q\u"`, `ALTER TABLE quoted DROP COLUMN "q\u"`, `/*é*/ SELECT "q\u" FROM quoted`, `/*é*/ SELECT a FROM quoted WHERE "q\u"=1`, `column "q\u" does not exist`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				db, record := backend.DB, backend.Recorder
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, record)
				fixture.RunStep(t, ctx, db, record, fixture.Exec("CREATE TABLE quoted("+tc.definition+" INT,a INT)", 0))
				fixture.RunStep(t, ctx, db, record, fixture.Exec("INSERT INTO quoted VALUES(1,7)", 1))
				queries := []string{tc.projection, tc.filter, strings.TrimPrefix(tc.projection, "/*é*/ "), strings.TrimPrefix(tc.filter, "/*é*/ ")}
				retained := make([]*sql.Stmt, len(queries))
				for i, q := range queries {
					s, err := db.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					retained[i] = s
				}
				fixture.RunStep(t, ctx, db, record, fixture.Exec(tc.drop, 0))
				for i, q := range queries {
					position := []string{"14", "34", "8", "28"}[i]
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: tc.message, Position: position}
					rows, err := retained[i].QueryContext(ctx)
					if rows != nil {
						rows.Close()
						t.Fatal("dropped expression returned rows")
					}
					fixture.AssertDiagnostic(t, err, &want)
					failure := fixture.ImplicitError(q, want)
					failure.SelectRows = true
					failure.ErrorStage = "execute"
					fixture.RunStep(t, ctx, db, record, failure)
					failure.Prepared = true
					failure.ErrorStage = "prepare"
					fixture.RunStep(t, ctx, db, record, failure)
				}
				fixture.RunStep(t, ctx, db, record, fixture.Select("SELECT * FROM quoted", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
			referenceDDLNativeDump(t, p)
		})
	}
}

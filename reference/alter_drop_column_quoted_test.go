package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceAlterDropColumnQuotedExpressionDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, definition, drop, projection, filter, message string }{
		{"quote", `"q""u"`, `ALTER TABLE quoted DROP COLUMN "q""u"`, `/*é*/ SELECT "q""u" FROM quoted`, `/*é*/ SELECT a FROM quoted WHERE "q""u"=1`, `column "q"u" does not exist`},
		{"backslash", `"q\u"`, `ALTER TABLE quoted DROP COLUMN "q\u"`, `/*é*/ SELECT "q\u" FROM quoted`, `/*é*/ SELECT a FROM quoted WHERE "q\u"=1`, `column "q\u" does not exist`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				db, record := backend.db, backend.recorder
				ctx := context.WithValue(p.ctx, referenceRecorderKey{}, record)
				referenceRunStep(t, ctx, db, record, referenceExec("CREATE TABLE quoted("+tc.definition+" INT,a INT)", 0))
				referenceRunStep(t, ctx, db, record, referenceExec("INSERT INTO quoted VALUES(1,7)", 1))
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
				referenceRunStep(t, ctx, db, record, referenceExec(tc.drop, 0))
				for i, q := range queries {
					position := []string{"14", "34", "8", "28"}[i]
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: tc.message, Position: position}
					rows, err := retained[i].QueryContext(ctx)
					if rows != nil {
						rows.Close()
						t.Fatal("dropped expression returned rows")
					}
					referenceAssertDiagnostic(t, err, &want)
					failure := referenceImplicitError(q, want)
					failure.selectRows = true
					failure.errorStage = "execute"
					referenceRunStep(t, ctx, db, record, failure)
					failure.prepared = true
					failure.errorStage = "prepare"
					referenceRunStep(t, ctx, db, record, failure)
				}
				referenceRunStep(t, ctx, db, record, referenceSelect("SELECT * FROM quoted", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
			referenceDDLNativeDump(t, p)
		})
	}
}

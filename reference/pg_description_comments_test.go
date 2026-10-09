package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReferencePGDescriptionComments(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("Prepared=%t", prepared), func(t *testing.T) {
			schemaReferenceCleanup(t, "pgd_comments_schema")
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) {
						t.Helper()
						if prepared {
							s = referencePrepared(s)
						}
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					}
					for _, query := range []string{"CREATE TABLE pgd_comments(a INT,b INT,c TEXT)", "CREATE SCHEMA pgd_comments_schema"} {
						run(referenceExec(query, 0))
					}
					var oid int64
					if err := backend.db.QueryRowContext(p.ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='pgd_comments'").Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 {
						t.Fatal("table identity", oid, err)
					}
					check := func(want [][]any) {
						t.Helper()
						s := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", want, oid)
						if prepared {
							s = referencePrepared(s)
						}
						pgDescriptionRows(t, p, backend, s)
					}
					check([][]any{})
					whole := "table 'quote' \\ slash\nUnicode: café 雪"
					first, third := "first 'quoted'", "third\nline \\ 雪"
					comment := func(target, text string) {
						t.Helper()
						run(referenceExec("COMMENT ON "+target+" IS '"+strings.ReplaceAll(text, "'", "''")+"'", 0))
					}
					comment("TABLE pgd_comments", whole)
					comment("COLUMN pgd_comments.a", first)
					comment("COLUMN pgd_comments.c", third)
					bag := func(table, a, c string) [][]any {
						rows := [][]any{}
						for _, entry := range []struct {
							sub  int64
							text string
						}{{0, table}, {1, a}, {3, c}} {
							if entry.text != "" {
								rows = append(rows, []any{oid, int64(1259), entry.sub, entry.text})
							}
						}
						return rows
					}
					check(bag(whole, first, third))
					for _, tc := range []struct{ query, label, text string }{
						{"SELECT obj_description($1,'pg_class')", "obj_description", whole},
						{"SELECT col_description($1,1)", "col_description", first},
						{"SELECT col_description($1,3)", "col_description", third},
					} {
						s := referenceSelect(tc.query, []string{tc.label}, []string{"TEXT"}, [][]any{{tc.text}}, oid)
						if prepared {
							s = referencePrepared(s)
						}
						commentInquiryStep(t, p, backend, backend.db, s)
					}
					comment("COLUMN pgd_comments.a", "replacement")
					check(bag(whole, "replacement", third))
					run(referenceExec("COMMENT ON COLUMN pgd_comments.a IS NULL", 0))
					check(bag(whole, "", third))
					comment("COLUMN pgd_comments.c", "")
					check(bag(whole, "", ""))
					comment("COLUMN pgd_comments.c", " ")
					check(bag(whole, "", " "))
					failure := referenceErrorDiagnostic("COMMENT ON COLUMN pgd_comments.missing IS 'not published'", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column \"missing\" of relation \"pgd_comments\" does not exist"})
					failure.errorStage = "execute"
					run(failure)
					check(bag(whole, "", " "))
					comment("SCHEMA pgd_comments_schema", "pgd schema unique description")
					var namespace int64
					if err := backend.db.QueryRowContext(p.ctx, "SELECT objoid FROM pg_catalog.pg_description WHERE classoid=2615 AND description='pgd schema unique description'").Scan(&namespace); err != nil || namespace <= 0 {
						t.Fatal("namespace identity", namespace, err)
					}
					s := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=2615", [][]any{{namespace, int64(2615), int64(0), "pgd schema unique description"}}, namespace)
					if prepared {
						s = referencePrepared(s)
					}
					pgDescriptionRows(t, p, backend, s)
					inquiry := referenceSelect("SELECT obj_description($1,'pg_namespace')", []string{"obj_description"}, []string{"TEXT"}, [][]any{{"pgd schema unique description"}}, namespace)
					if prepared {
						inquiry = referencePrepared(inquiry)
					}
					commentInquiryStep(t, p, backend, backend.db, inquiry)
					run(referenceExec("CREATE TABLE pgd_comments_copy(LIKE pgd_comments)", 0))
					var copyOID int64
					if err := backend.db.QueryRowContext(p.ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname='pgd_comments_copy'").Scan(&copyOID); err != nil || copyOID <= 0 || copyOID == oid {
						t.Fatal("LIKE identity", copyOID, err)
					}
					s = pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", [][]any{}, copyOID)
					if prepared {
						s = referencePrepared(s)
					}
					pgDescriptionRows(t, p, backend, s)
				})
			}
		})
	}
}

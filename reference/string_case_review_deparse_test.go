package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceStringCaseReviewDeparse(t *testing.T) {
	stringCaseScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ op, value string }{{"lower", "ß"}, {"upper", "SS"}, {"casefold", "ss"}, {"initcap", "Ss"}} {
			t.Run(tc.op, func(t *testing.T) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
				table, constraint := "sc_review_deparse_"+tc.op, "sc_review_deparse_check_"+tc.op
				decl := "id INT,v CHAR(4) COLLATE pg_unicode_fast,e TEXT"
				run(referenceExec("CREATE TABLE "+table+"("+decl+",CONSTRAINT "+constraint+" CHECK ("+tc.op+"(v) COLLATE pg_unicode_fast=e))", 0))
				run(referenceExec("INSERT INTO "+table+" VALUES(1,'ß',$1),(2,NULL,NULL)", 2, tc.value))
				// Independently specified ordinary-function casts and COLLATE
				// presentation, confirmed for all four calls on PostgreSQL18.0.
				for _, mode := range []struct{ name, suffix, expr string }{
					{"default", "", "((" + tc.op + "((v)::text) COLLATE pg_unicode_fast) = e)"},
					{"compact", ",false", "((" + tc.op + "((v)::text) COLLATE pg_unicode_fast) = e)"},
					{"pretty", ",true", "(" + tc.op + "(v::text) COLLATE pg_unicode_fast) = e"},
				} {
					t.Run(mode.name, func(t *testing.T) {
						for _, consumer := range []struct{ name, query, want string }{
							{"expr", "SELECT pg_get_expr(conbin,conrelid" + mode.suffix + ") AS d FROM pg_catalog.pg_constraint WHERE conname='" + constraint + "'", mode.expr},
							{"constraint", "SELECT pg_get_constraintdef(oid" + mode.suffix + ") AS d FROM pg_catalog.pg_constraint WHERE conname='" + constraint + "'", "CHECK (" + mode.expr + ")"},
						} {
							t.Run(consumer.name, func(t *testing.T) {
								var actual string
								if err := c.QueryRowContext(p.ctx, consumer.query).Scan(&actual); err != nil {
									t.Fatal(err)
								}
								stringCaseBoth(t, p, b, c, referenceSelect(consumer.query, []string{"d"}, []string{"TEXT"}, [][]any{{consumer.want}}))
								if consumer.name == "expr" {
									// Execute the fetched definition, not the expected string.
									stringCaseBoth(t, p, b, c, referenceSelect("SELECT id,"+actual+" AS ok,"+tc.op+"(v) AS value FROM "+table+" ORDER BY id", []string{"id", "ok", "value"}, []string{"INT4", "BOOL", "TEXT"}, [][]any{{int64(1), true, tc.value}, {int64(2), nil, nil}}))
									return
								}
								replay := fmt.Sprintf("sc_review_replay_%s_%s", tc.op, mode.name)
								run(referenceExec("CREATE TABLE "+replay+"("+decl+",CONSTRAINT sc_review_replay_check "+actual+")", 0))
								run(referenceExec("INSERT INTO "+replay+" VALUES(1,'ß',$1),(2,NULL,NULL)", 2, tc.value))
								d := controlsReferenceDiagnostic("23514", "new row for relation \""+replay+"\" violates check constraint \"sc_review_replay_check\"")
								d.Detail, d.Schema, d.Table, d.Constraint = "Failing row contains (3, ß   , wrong).", "public", replay, "sc_review_replay_check"
								for _, prepared := range []bool{false, true} {
									s := referenceErrorDiagnostic("INSERT INTO "+replay+" VALUES(3,'ß','wrong')", d)
									s.errorStage = "execute"
									if prepared {
										s = referencePrepared(s)
									}
									run(s)
								}
								stringCaseBoth(t, p, b, c, referenceSelect("SELECT id,v,e FROM "+replay+" ORDER BY id", []string{"id", "v", "e"}, []string{"INT4", "BPCHAR", "TEXT"}, [][]any{{int64(1), "ß   ", tc.value}, {int64(2), nil, nil}}))
								run(referenceExec("DROP TABLE "+replay, 0))
							})
						}
					})
				}
			})
		}
	})
}

package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalDefinitionDiagnostics(t *testing.T) {
	cases := []struct {
		q, tag string
		want   fixture.Diagnostic
	}{
		{`CREATE ACCESS METHOD tx_diag_bad TYPE INDEX HANDLER tx_diag_missing`, "CREATE ACCESS METHOD", fixture.Diagnostic{Code: "42883", Message: `function tx_diag_missing(internal) does not exist`}},
		{`CREATE ACCESS METHOD tx_diag_bad TYPE INDEX HANDLER int4in`, "CREATE ACCESS METHOD", fixture.Diagnostic{Code: "42883", Message: `function int4in(internal) does not exist`}},
		{`CREATE ACCESS METHOD tx_diag_bad TYPE INDEX HANDLER heap_tableam_handler`, "CREATE ACCESS METHOD", fixture.Diagnostic{Code: "42809", Message: `function heap_tableam_handler must return type index_am_handler`}},
		{`DROP ACCESS METHOD tx_diag_missing`, "DROP ACCESS METHOD", fixture.Diagnostic{Code: "42704", Message: `access method "tx_diag_missing" does not exist`}},
		{`CREATE OPERATOR FAMILY public.tx_diag_family USING tx_diag_missing`, "CREATE OPERATOR FAMILY", fixture.Diagnostic{Code: "42704", Message: `access method "tx_diag_missing" does not exist`}},
		{`ALTER OPERATOR FAMILY public.tx_diag_missing USING btree RENAME TO tx_diag_new`, "ALTER OPERATOR FAMILY", fixture.Diagnostic{Code: "42704", Message: `operator family "public.tx_diag_missing" does not exist for access method "btree"`}},
		{`DROP OPERATOR FAMILY public.tx_diag_missing USING btree`, "DROP OPERATOR FAMILY", fixture.Diagnostic{Code: "42704", Message: `operator family "public.tx_diag_missing" does not exist for access method "btree"`}},
		{`CREATE OPERATOR CLASS public.tx_diag_bad FOR TYPE integer USING tx_diag_missing AS STORAGE integer`, "CREATE OPERATOR CLASS", fixture.Diagnostic{Code: "42704", Message: `access method "tx_diag_missing" does not exist`}},
		{`ALTER OPERATOR CLASS public.tx_diag_missing USING btree RENAME TO tx_diag_new`, "ALTER OPERATOR CLASS", fixture.Diagnostic{Code: "42704", Message: `operator class "public.tx_diag_missing" does not exist for access method "btree"`}},
		{`DROP OPERATOR CLASS public.tx_diag_missing USING btree`, "DROP OPERATOR CLASS", fixture.Diagnostic{Code: "42704", Message: `operator class "public.tx_diag_missing" does not exist for access method "btree"`}},
	}
	for i, tc := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			definitionScenario(t, []string{"tx_diag_bad"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, mode := range []string{"autocommit", "sql", "go"} {
					for _, held := range []bool{false, true} {
						r := definitionRoute{mode: mode, held: held}
						h := definitionHeld(t, p, c, tc.q)
						e, end := r.begin(t, p, b, c)
						run := definitionRun(t, p, b, e)
						failure := fixture.ImplicitError(tc.q, tc.want)
						failure.ErrorStage = "execute"
						if held {
							fixture.RunStep(t, p.Ctx, h, b.Recorder, failure)
						} else {
							run(failure)
						}
						if mode != "autocommit" {
							run(fixture.ErrorDiagnostic(`SELECT 1`, definitionAborted()))
						}
						end()
						definitionRun(t, p, b, c)(definitionCount(`SELECT count(*) FROM pg_am WHERE amname='tx_diag_bad'`, 0))
						definitionRun(t, p, b, c)(definitionCount(`SELECT count(*) FROM pg_opfamily WHERE opfname IN ('tx_diag_family','tx_diag_bad','tx_diag_new')`, 0))
						definitionRun(t, p, b, c)(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname IN ('tx_diag_bad','tx_diag_new')`, 0))
					}
				}
				for _, goTx := range []bool{false, true} {
					var e fixture.Executor = c
					var tx *sql.Tx
					if goTx {
						var err error
						tx, err = c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
						if err != nil {
							t.Fatal(err)
						}
						e = tx
					} else {
						definitionRun(t, p, b, c)(fixture.Exec(`BEGIN READ ONLY`, 0))
					}
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "25006", Message: `cannot execute ` + tc.tag + ` in a read-only transaction`})
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, definitionHeld(t, p, e, tc.q), b.Recorder, s)
					definitionRun(t, p, b, e)(fixture.ErrorDiagnostic(`SELECT 1`, definitionAborted()))
					if goTx {
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
					} else {
						definitionRun(t, p, b, c)(fixture.Exec(`ROLLBACK`, 0))
					}
				}
				definitionRun(t, p, b, c)(fixture.Select(`SELECT 42 AS healthy`, []string{"healthy"}, []string{"INT4"}, [][]any{{int64(42)}}))
			})
		})
	}
	t.Run("syntax_position", func(t *testing.T) {
		definitionScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q := `CREATE ACCESS METHOD tx_diag_bad TYPE BOGUS HANDLER bthandler`
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "BOGUS"`, Position: strconv.Itoa(strings.Index(q, "BOGUS") + 1)})
			s.ErrorStage = "execute"
			definitionRun(t, p, b, c)(s)
			s.Prepared = true
			s.ErrorStage = "prepare"
			definitionRun(t, p, b, c)(s)
		})
	})
	t.Run("if_exists_notices", func(t *testing.T) {
		definitionScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
			var notices []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			for _, tc := range []struct{ q, message string }{
				{`DROP ACCESS METHOD IF EXISTS tx_diag_missing`, `access method "tx_diag_missing" does not exist, skipping`},
				{`DROP OPERATOR FAMILY IF EXISTS public.tx_diag_missing USING btree`, `operator family "public.tx_diag_missing" does not exist for access method "btree", skipping`},
				{`DROP OPERATOR CLASS IF EXISTS public.tx_diag_missing USING btree`, `operator class "public.tx_diag_missing" does not exist for access method "btree", skipping`},
			} {
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec(`BEGIN`, 0))
					fixture.RoleGrantNoticeExec(t, p, c, rec, prepared, &notices, tc.q, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: tc.message})
					fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec(`COMMIT`, 0))
				}
			}
		})
	})
}

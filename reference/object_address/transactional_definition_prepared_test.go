package reference_test

import (
	"database/sql"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalDefinitionPrepared(t *testing.T) {
	cases := []struct{ q, message, hint string }{
		{`CREATE ACCESS METHOD tx_prepared_new TYPE INDEX HANDLER bthandler`, `permission denied to create access method "tx_prepared_new"`, `Must be superuser to create an access method.`},
		{`DROP ACCESS METHOD tx_prepared_am`, `must be superuser`, ""},
		{`CREATE OPERATOR FAMILY public.tx_prepared_new USING btree`, `must be superuser to create an operator family`, ""},
		{`ALTER OPERATOR FAMILY public.tx_prepared_family USING btree ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`, `must be superuser to alter an operator family`, ""},
		{`ALTER OPERATOR FAMILY public.tx_prepared_family USING btree RENAME TO tx_prepared_new`, `must be owner of operator family tx_prepared_family`, ""},
		{`DROP OPERATOR FAMILY public.tx_prepared_family USING btree`, `must be owner of operator family btree.public.tx_prepared_family`, ""},
		{`CREATE OPERATOR CLASS public.tx_prepared_new FOR TYPE integer USING btree AS STORAGE integer`, `must be superuser to create an operator class`, ""},
		{`ALTER OPERATOR CLASS public.tx_prepared_class USING btree RENAME TO tx_prepared_new`, `must be owner of operator class tx_prepared_class`, ""},
		{`DROP OPERATOR CLASS public.tx_prepared_class USING btree`, `must be owner of operator class btree.public.tx_prepared_class`, ""},
	}
	for i, tc := range cases {
		for _, mode := range []string{"autocommit", "sql", "go"} {
			t.Run(strconv.Itoa(i)+"/"+mode, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"tx_prepared_reader"})
				definitionScenario(t, []string{"tx_prepared_am", "tx_prepared_new"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := definitionRun(t, p, b, c)
					for _, q := range []string{`CREATE ROLE tx_prepared_reader`, `GRANT USAGE,CREATE ON SCHEMA public TO tx_prepared_reader`, `CREATE ACCESS METHOD tx_prepared_am TYPE INDEX HANDLER bthandler`, `CREATE OPERATOR FAMILY public.tx_prepared_family USING btree`, `CREATE OPERATOR CLASS public.tx_prepared_class FOR TYPE integer USING btree AS STORAGE integer`} {
						run(fixture.Exec(q, 0))
					}
					// A genuine held statement is analyzed while authorized, before BEGIN.
					h := definitionHeld(t, p, c, tc.q)
					run(definitionCount(`SELECT count(*) FROM pg_am WHERE amname='tx_prepared_new'`, 0))
					r := definitionRoute{mode: mode, commit: false}
					e, end := r.begin(t, p, b, c)
					execute := definitionRun(t, p, b, e)
					if mode != "autocommit" {
						execute(fixture.Exec(`SAVEPOINT actor`, 0))
					}
					execute(fixture.Exec(`SET ROLE tx_prepared_reader`, 0))
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42501", Message: tc.message, Hint: tc.hint})
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
					if mode != "autocommit" {
						execute(fixture.ErrorDiagnostic(`SELECT 1`, definitionAborted()))
						execute(fixture.Exec(`ROLLBACK TO actor`, 0))
					} else {
						execute(fixture.Exec(`RESET ROLE`, 0))
					}
					// Same handle and physical owner recover; no cached denial or grant.
					fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Exec(tc.q, 0))
					end()
					run(fixture.Exec(`RESET ROLE`, 0))
					run(fixture.Select(`SELECT 42 AS healthy`, []string{"healthy"}, []string{"INT4"}, [][]any{{int64(42)}}))
				})
			})
		}
	}
	t.Run("member_reader_reuse", func(t *testing.T) {
		definitionScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := definitionRun(t, p, b, c)
			run(fixture.Exec(`CREATE OPERATOR FAMILY public.tx_reader_family USING btree`, 0))
			q := `SELECT a.amprocnum,a.amproc::oid::bigint FROM pg_amproc a JOIN pg_opfamily f ON f.oid=a.amprocfamily WHERE f.opfname='tx_reader_family' ORDER BY a.amprocnum`
			h := definitionHeld(t, p, c, q)
			check := func(rows [][]any) {
				fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"amprocnum", "amproc"}, []string{"INT2", "INT8"}, rows))
			}
			check(nil)
			run(fixture.Exec(`BEGIN`, 0))
			run(fixture.Exec(`SAVEPOINT member`, 0))
			run(fixture.Exec(`ALTER OPERATOR FAMILY public.tx_reader_family USING btree ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, 0))
			check([][]any{{int64(1), int64(351)}})
			run(fixture.Exec(`ROLLBACK TO member`, 0))
			check(nil)
			run(fixture.Exec(`ALTER OPERATOR FAMILY public.tx_reader_family USING btree ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, 0))
			check([][]any{{int64(1), int64(351)}})
			run(fixture.Exec(`COMMIT`, 0))
			check([][]any{{int64(1), int64(351)}})
		})
	})
}

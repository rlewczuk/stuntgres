package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func plpgsqlLanguageDump(t *testing.T) {
	t.Helper()
	t.Run("altered_language_dump", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "GRANT USAGE ON LANGUAGE plpgsql TO PUBLIC"); err != nil {
					t.Error(err)
				}
			}()
			userOperatorExec(t, p, b, c, "REVOKE USAGE ON LANGUAGE plpgsql FROM PUBLIC")
			plpgsqlValue(t, p, b, c, "SELECT lanacl IS NOT NULL AS v FROM pg_language WHERE lanname='plpgsql'", "BOOL", true)
			var owner string
			if err:=c.QueryRowContext(p.Ctx,"SELECT lanowner::regrole::text FROM pg_language WHERE lanname='plpgsql'").Scan(&owner);err!=nil {t.Fatal(err)}
			check:=func(){
				fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT has_language_privilege('public','plpgsql','USAGE') AS v",[]string{"v"},[]string{"BOOL"},[][]any{{false}}))
				fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT unnest(lanacl)::text AS acl FROM pg_language WHERE lanname='plpgsql'",[]string{"acl"},[]string{"TEXT"},[][]any{{owner+"=U/"+owner}}))
			}
			check()
			if !b.PG {
				var saved bytes.Buffer
				if err:=stuntgres.Dump(p.Ctx,p.Native,&saved);err!=nil {t.Fatal(err)}
				if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(saved.Bytes()));err!=nil {t.Fatal(err)}
				check()
			}
		})
	})
}


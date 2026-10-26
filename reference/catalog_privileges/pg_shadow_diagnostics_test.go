package reference_test

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePGShadowDiagnostics(t *testing.T) {
	pgUserScenario(t, []string{"pgs_diag"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgUserCreate(t, p, b, "pgs_diag")
		password := pgShadowMD5; pgShadowSetPassword(t, p, b, "pgs_diag", &password)
		pgUserExec(t, p, b, "CREATE TABLE pgs_sentinel(a INT)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgs_sentinel VALUES(7)", 1))
		cases := []struct{ q string; want fixture.Diagnostic; token string }{
			{"INSERT INTO pg_catalog.pg_shadow(passwd) VALUES(NULL)", pgUserWriteDiagnostic("insert into", false), ""},
			{"INSERT INTO pg_catalog.pg_shadow(usename) VALUES('forbidden')", pgUserWriteDiagnostic("insert into", false), ""},
			{"INSERT INTO pg_catalog.pg_shadow VALUES('forbidden',1,false,false,false,false,NULL,NULL,NULL)", pgUserWriteDiagnostic("insert into", false), ""},
			{"UPDATE pg_catalog.pg_shadow SET passwd=NULL WHERE FALSE", pgUserWriteDiagnostic("update", false), ""},
			{"UPDATE pg_catalog.pg_shadow SET usecreatedb=true WHERE FALSE", pgUserWriteDiagnostic("update", false), ""},
			{"DELETE FROM pg_catalog.pg_shadow WHERE FALSE", pgUserWriteDiagnostic("delete from", false), ""},
			{"SELECT missing FROM pg_catalog.pg_shadow", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"}, ""},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_shadow", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "14"}, ""},
			{"SELECT * FROM pg_catalog.pg_shadow WHERE usesysid='oops'", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "oops"`}, "'oops'"},
			{"UPDATE pg_catalog.pg_shadow SET missing=true", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "pg_shadow" does not exist`}, "missing"},
			{"UPDATE pg_catalog.pg_shadow SET usecreatedb=1", fixture.Diagnostic{Code: "42804", Message: `column "usecreatedb" is of type boolean but expression is of type integer`, Hint: "You will need to rewrite or cast the expression."}, "1"},
			{"DELETE FROM pg_catalog.pg_shadow WHERE 1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer"}, "1"},
		}
		for _, tc := range cases {
			if tc.token != "" { tc.want.Position = strconv.Itoa(utf8.RuneCountInString(tc.q[:strings.LastIndex(tc.q, tc.token)])+1) }
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, tc.want); s.Prepared = prepared; s.SelectRows = strings.HasPrefix(tc.q, "SELECT"); s.ErrorStage = "execute"
				if prepared { s.ErrorStage = "prepare" }
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				pgShadowPassword(t, p, b.DB, false, "pgs_diag", &password)
				pgUserBoth(t, p, b, pgUserSelect("SELECT * FROM pg_user WHERE usesysid=$1", [][]any{pgUserRow("pgs_diag", id, nil)}, id))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a FROM pgs_sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT * FROM pg_shadow WHERE usesysid=$1", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "oops"`, Context: "unnamed portal parameter $1 = '...'"}, "oops")
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		pgUserExec(t, p, b, "DROP ROLE pgs_diag")
	})
}

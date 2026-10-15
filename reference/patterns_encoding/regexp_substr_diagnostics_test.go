package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegexpSubstrSignatures(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, typ := range []string{"text", "varchar(8)", "char(3)", "name"} {
			regexpRoutes(t, p, b, regexpOne(b, fmt.Sprintf(`regexp_substr('a'::%s,'^a$')`, typ), "TEXT", "a"))
		}
		for _, c := range []struct{ q, code, msg, token string }{
			{`SELECT regexp_substr('a')`, "42883", `function regexp_substr(unknown) does not exist`, "regexp_substr"},
			{`SELECT regexp_substr(true,'a')`, "42883", `function regexp_substr(boolean, unknown) does not exist`, "regexp_substr"},
			{`SELECT regexp_substr('a','a','oops')`, "22P02", `invalid input syntax for type integer: "oops"`, "'oops'"},
			{`SELECT regexp_substr('a','a','2147483648')`, "22003", `value "2147483648" is out of range for type integer`, "'2147483648'"},
			{`SELECT regexp_substr(string=>'a',string=>'a',pattern=>'a')`, "42601", `argument name "string" used more than once`, "string=>'a',pattern"},
			{`SELECT regexp_substr(string=>'a','a')`, "42601", `positional argument cannot follow named argument`, "'a')"},
			{`SELECT /* é🙂 */ regexp_substr(string=>'a',pattern=>'a',start=>1,n=>1)`, "42883", `function regexp_substr(string => unknown, pattern => unknown, start => integer, n => integer) does not exist`, "regexp_substr"},
		} {
			hint := ""
			if c.code == "42883" {
				hint = similarFunctionHint
			}
			regexpRoutes(t, p, b, regexpBindError(c.q, c.code, c.msg, hint, c.token))
		}
		q := `SELECT regexp_substr($1,'a',$1)`
		s := regexpBindError(q, "42P08", `inconsistent types deduced for parameter $1`, "", "$1)")
		s.Diagnostic.Detail = "text versus integer"
		s.Args = []any{"a"}
		regexpRoutes(t, p, b, s)
	})
}
func TestReferenceRegexpSubstrDiagnostics(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range []struct{ args, code, msg string }{
			{`'a','[',0,0,'z',-1`, "22023", `invalid value for parameter "start": 0`},
			{`'a','a',-2147483648`, "22023", `invalid value for parameter "start": -2147483648`},
			{`'a','[',1,0,'z',-1`, "22023", `invalid value for parameter "n": 0`},
			{`'a','a',1,-1`, "22023", `invalid value for parameter "n": -1`},
			{`'a','[',1,1,'z',-1`, "22023", `invalid value for parameter "subexpr": -1`},
			{`'a','[',1,1,'gz'`, "22023", `invalid regular expression option: "z"`},
			{`'a','a',1,1,'z'`, "22023", `invalid regular expression option: "z"`},
			{`'a','[',1,1,'g'`, "22023", `regexp_substr() does not support the "global" option`},
			{`'a','[',2147483647`, "2201B", `invalid regular expression: brackets [] not balanced`},
			{`'a','[',1,2147483647,'',2147483647`, "2201B", `invalid regular expression: brackets [] not balanced`},
			{`'a','('`, "2201B", `invalid regular expression: parentheses () not balanced`},
		} {
			regexpRoutes(t, p, b, regexpRuntimeError("SELECT regexp_substr("+c.args+")", c.code, c.msg, ""))
		}
		for _, flags := range []string{"qx", "qn", "qp", "qw"} {
			regexpRoutes(t, p, b, regexpRuntimeError(`SELECT regexp_substr('a','a',1,1,'`+flags+`')`, "2201B", "invalid regular expression: invalid argument to regex function", ""))
		}
		q := `SELECT regexp_substr('a' COLLATE "C",'a' COLLATE "POSIX")`
		regexpRoutes(t, p, b, regexpBindError(q, "42P21", `collation mismatch between explicit collations "C" and "POSIX"`, "", `COLLATE "POSIX"`))
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE substr_collation(s TEXT COLLATE "C",p TEXT COLLATE "POSIX",f TEXT COLLATE "POSIX")`, 0))
		run(fixture.Exec(`INSERT INTO substr_collation VALUES('a','a','')`, 1))
		for _, q := range []string{`SELECT regexp_substr(s,p) FROM substr_collation`, `SELECT regexp_substr(s,'a',1,1,f) FROM substr_collation`} {
			regexpRoutes(t, p, b, regexpRuntimeError(q, "42P22", "could not determine which collation to use for regular expression", "Use the COLLATE clause to set the collation explicitly."))
		}
	})
}

func TestReferenceRegexpSubstrAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"substr_reader"})
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, `RESET SESSION AUTHORIZATION`)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec(`CREATE USER substr_reader`, 0))
		run(fixture.Exec(`CREATE TABLE source(id INT,s TEXT,p TEXT,st INT,n INT,f TEXT,g INT)`, 0))
		run(fixture.Exec(`INSERT INTO source VALUES(1,'a','(a)',1,1,'',1)`, 1))
		if b.PG {
			run(fixture.Exec(`GRANT USAGE ON SCHEMA public TO substr_reader`, 0))
		}
		run(fixture.Exec(`GRANT SELECT(id) ON source TO substr_reader`, 0))
		queries := []string{`SELECT regexp_substr(s,'a') FROM source`, `SELECT regexp_substr('a',p) FROM source`, `SELECT regexp_substr('a','a',st) FROM source`, `SELECT regexp_substr('a','a',1,n) FROM source`, `SELECT regexp_substr('a','a',1,1,f) FROM source`, `SELECT regexp_substr('a','a',1,1,'',g) FROM source`}
		for _, q := range queries {
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			for _, empty := range []bool{false, true} {
				if empty {
					run(fixture.Exec(`DELETE FROM source`, 1))
				}
				run(fixture.Exec(`SET SESSION AUTHORIZATION substr_reader`, 0))
				for _, query := range []string{q, q + " WHERE false"} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table source"})
						s.SelectRows = true
						s.Prepared = prepared
						s.ErrorStage = "execute"
						run(s)
					}
				}
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table source"})
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
			}
			stmt.Close()
			run(fixture.Exec(`INSERT INTO source VALUES(1,'a','(a)',1,1,'',1)`, 1))
		}
		run(fixture.Exec(`GRANT SELECT(s,p,st,n,f,g) ON source TO substr_reader`, 0))
		run(fixture.Exec(`SET SESSION AUTHORIZATION substr_reader`, 0))
		s := regexpRows(b, `SELECT regexp_substr(s,p,st,n,f,g) AS result FROM source`, []string{"result"}, []string{"TEXT"}, [][]any{{"a"}})
		run(s)
		run(fixture.Prepared(s))
	})
}

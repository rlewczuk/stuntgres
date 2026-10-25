package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPasswordDiagnostics(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		// Pinned PostgreSQL 18.6 UTF8/C confirms ERROR in both severity
		// fields and empty Detail/Hint/Position/InternalPosition/InternalQuery/
		// Context/Schema/Table/Column/DataType/Constraint for body failures.
		// ImplicitError fills both severities; omitted Diagnostic fields are
		// asserted empty by the shared fixture, not ignored.
		for i, salt := range []string{"", "a", "_", "_.......", "$2a$", "$2x$04$short", "$2a$00$......................", "$2a$03$......................", "$2a$32$......................", "$2x$40$......................", "$2a$ab$......................", "$2a$4$......................", "$2a$004$......................", "$2a$04!......................", "$2a$04$.....................=", "$2a$04$.....................\n", "$2x$04$.....................\r"} {
			t.Run(fmt.Sprintf("salt_%d", i), func(t *testing.T) {
				s := fixture.ImplicitError("SELECT crypt($1,$2)", fixture.Diagnostic{Code: "22023", Message: "invalid salt"}, "password", salt)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, r, s)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
			})
		}
		for _, count := range []int64{-2147483648, -1, 1, 2, 3, 32, 2147483647} {
			s := fixture.ImplicitError("SELECT gen_salt($1,$2)", fixture.Diagnostic{Code: "22023", Message: "gen_salt: Incorrect number of rounds"}, "bf", count)
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, r, s)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
		}
		for _, v := range []struct {
			name   string
			counts []int64
		}{
			{"des", []int64{-2147483648, -1, 1, 24, 26, 2147483647}},
			{"md5", []int64{-2147483648, -1, 1, 999, 1001, 2147483647}},
			{"xdes", []int64{-2147483648, -1, 2, 724, 16777214, 16777216, 2147483647}},
		} {
			q := "SELECT gen_salt($1,$2)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, count := range v.counts {
				for _, s := range []fixture.Step{
					fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: "gen_salt: Incorrect number of rounds"}, v.name, count),
					fixture.ImplicitError(fmt.Sprintf("SELECT gen_salt('%s',%d)", v.name, count), fixture.Diagnostic{Code: "22023", Message: "gen_salt: Incorrect number of rounds"}),
				} {
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, r, s)
					fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
				}
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: "gen_salt: Incorrect number of rounds"}, v.name, count)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, r, s)
				var salt string
				if err := stmt.QueryRowContext(p.Ctx, v.name, 0).Scan(&salt); err != nil {
					t.Fatal(err)
				}
				rounds := map[string]int{"des": 25, "md5": 1000, "xdes": 725}[v.name]
				pgcryptoPasswordAlgorithmSaltShape(t, salt, v.name, rounds)
			}
			stmt.Close()
		}
		for _, name := range []string{"", "unknown", " bf", "bf ", " des", "xdes ", " md5", strings.Repeat("b", 129)} {
			fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError("SELECT gen_salt($1)", fixture.Diagnostic{Code: "22023", Message: "gen_salt: Unknown salt algorithm"}, name))
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError("SELECT crypt('password','$2$')", fixture.Diagnostic{Code: "39000", Message: "crypt(3) returned NULL"}))
		for _, v := range []struct{ salt, hash string }{
			{"ab", "abJnggxhB/yWI"},
			{"$2b$04$......................", "$2SXu7evaw9i."},
			{"$2y$04$......................", "$2SXu7evaw9i."},
		} {
			// These prefixes dispatch to DES, not additional bcrypt variants.
			pgcryptoPasswordText(t, p, c, r, "SELECT crypt('password',$1) AS value", v.hash, v.salt)
		}
		for _, v := range []struct{ query, code, message string }{
			{"SELECT crypt('p','$2a$03$......................')", "22023", "invalid salt"},
			{"SELECT gen_salt('bf',3)", "22023", "gen_salt: Incorrect number of rounds"},
			{"SELECT gen_salt('unknown')", "22023", "gen_salt: Unknown salt algorithm"},
			{"SELECT crypt('password','')", "22023", "invalid salt"},
			{"SELECT crypt('password','a')", "22023", "invalid salt"},
			{"SELECT crypt('password','_')", "22023", "invalid salt"},
			{"SELECT crypt('password','_.......')", "22023", "invalid salt"},
			{"SELECT crypt('password','_........')", "39000", "crypt(3) returned NULL"},
			{"SELECT crypt('p','')", "22023", "invalid salt"},
			{"SELECT crypt('p','_........')", "39000", "crypt(3) returned NULL"},
		} {
			s := fixture.ImplicitError(v.query, fixture.Diagnostic{Code: v.code, Message: v.message})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, r, s)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE pgcrypto_password_atomic(p text,s text,h text)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_password_atomic VALUES('foox',$1,$2)", 1, pgcryptoPasswordSalt, pgcryptoPasswordFoox))
		write := fixture.ImplicitError("INSERT INTO pgcrypto_password_atomic SELECT p,s,crypt(p,s) FROM (VALUES ('foox',$1::text),('wrong','$2a$03$......................')) AS source(p,s)", fixture.Diagnostic{Code: "22023", Message: "invalid salt"}, pgcryptoPasswordSalt)
		fixture.RunStep(t, p.Ctx, c, r, write)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pgcrypto_password_atomic", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		late := fixture.ImplicitError("INSERT INTO pgcrypto_password_atomic SELECT p,s,crypt(p,s) FROM (VALUES ('foox','NB'),('wrong','_.......')) AS source(p,s)", fixture.Diagnostic{Code: "22023", Message: "invalid salt"})
		late.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, r, late)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(late))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT p,s,h FROM pgcrypto_password_atomic", []string{"p", "s", "h"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"foox", pgcryptoPasswordSalt, pgcryptoPasswordFoox}}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
		fixture.RunStep(t, p.Ctx, c, r, write)
		fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pgcrypto_password_atomic", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}

func TestReferencePgcryptoPasswordOverloadsAndNull(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, q := range []string{"SELECT gen_salt('bf',4::smallint)", "SELECT gen_salt('bf','4')"} {
			pgcryptoPasswordGenerated(t, p, c, r, q, "crypt", 4)
		}
		for _, v := range []struct{ q, name, args string }{
			{"SELECT crypt('a')", "crypt", "unknown"}, {"SELECT crypt('a','b','c')", "crypt", "unknown, unknown, unknown"},
			{"SELECT crypt('a'::bytea,'b')", "crypt", "bytea, unknown"}, {"SELECT gen_salt()", "gen_salt", ""},
			{"SELECT gen_salt('bf',4::bigint)", "gen_salt", "unknown, bigint"}, {"SELECT gen_salt('bf',4::numeric)", "gen_salt", "unknown, numeric"},
			{"SELECT gen_salt('bf',4,5)", "gen_salt", "unknown, integer, integer"}, {"SELECT \"CRYPT\"('a','b')", "CRYPT", "unknown, unknown"},
			{"SELECT gen_salt(type => 'bf')", "gen_salt", "type => unknown"},
			{"SELECT gen_salt(VARIADIC ARRAY['bf'])", "gen_salt", "text[]"},
		} {
			s := extensionMissing(v.q, v.name, v.args)
			fixture.RunStep(t, p.Ctx, c, r, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
		// These complete records/stages were observed on pinned PG18.0 UTF8/C.
		for _, v := range []struct{ query, code, message string }{
			{"SELECT gen_salt('bf','oops')", "22P02", `invalid input syntax for type integer: "oops"`},
			{"SELECT gen_salt('bf','2147483648')", "22003", `value "2147483648" is out of range for type integer`},
		} {
			s := fixture.ImplicitError(v.query, fixture.Diagnostic{Code: v.code, Message: v.message, Position: "22"})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, r, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
		for _, arg := range []any{"oops", "2147483648", int64(2147483648)} {
			code, message := "22003", `value "2147483648" is out of range for type integer`
			if arg == "oops" {
				code, message = "22P02", `invalid input syntax for type integer: "oops"`
			}
			s := fixture.ImplicitError("SELECT gen_salt('bf',$1)", fixture.Diagnostic{Code: code, Message: message, Context: "unnamed portal parameter $1 = '...'"}, arg)
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, r, s)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
		}
		for _, q := range []string{"SELECT crypt(NULL::text,'') AS value", "SELECT crypt(NULL::text,'_........') AS value", "SELECT crypt(NULL::text,'$1$') AS value", "SELECT gen_salt(NULL::text,-1) AS value", "SELECT gen_salt('des',NULL::integer) AS value", "SELECT gen_salt('xdes',NULL::integer) AS value", "SELECT gen_salt('md5',NULL::integer) AS value", "SELECT crypt(NULL,'bad') AS value", "SELECT crypt('a',NULL) AS value", "SELECT gen_salt(NULL) AS value", "SELECT gen_salt(NULL,3) AS value", "SELECT gen_salt('bad',NULL) AS value"} {
			pgcryptoPasswordText(t, p, c, r, q, nil)
		}
		// A table column supplies genuine runtime NULL, not a parameter that
		// PostgreSQL's custom plan may replace with a strict-foldable constant.
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE pgcrypto_password_runtime_null(p text,n integer)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_password_runtime_null VALUES(NULL,0)", 1))
		sibling := fixture.ImplicitError("SELECT crypt(p,(1/n)::text) FROM pgcrypto_password_runtime_null", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		sibling.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, r, sibling)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(sibling))
		demand := fixture.ImplicitError("SELECT gen_salt(p,1/n) FROM pgcrypto_password_runtime_null", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		demand.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, r, demand)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(demand))
		pgcryptoPasswordNewSalts(t, p, c, r, "")
		q := "SELECT gen_salt($1,$2) AS value"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, args := range [][]any{{"bf", 3}, {"unknown", 4}} {
			message := "gen_salt: Incorrect number of rounds"
			if args[0] == "unknown" {
				message = "gen_salt: Unknown salt algorithm"
			}
			fixture.RunStep(t, p.Ctx, held, r, fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: message}, args...))
		}
		fixture.RunStep(t, p.Ctx, held, r, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{nil}}, nil, 4))
		var salt string
		if err := stmt.QueryRowContext(p.Ctx, "bf", 4).Scan(&salt); err != nil {
			t.Fatal(err)
		}
		pgcryptoPasswordSaltShape(t, salt, 4)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SEQUENCE pgcrypto_password_effect", 0))
		pgcryptoPasswordText(t, p, c, r, "SELECT crypt(p,nextval('pgcrypto_password_effect')::text) AS value FROM pgcrypto_password_runtime_null", nil)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT currval('pgcrypto_password_effect') AS value", []string{"value"}, []string{"INT8"}, [][]any{{int64(2)}}))
	})
}

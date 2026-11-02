package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPolicyStrictDemand(t *testing.T) {
	t.Run("SixAlgorithmsAndBothSaltSignatures", func(t *testing.T) {
		pgcryptoPolicyCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			q := "SELECT pgcrypto_part.crypt($1,$2) AS value"
			vectors := []struct{ password, salt, hash string }{
				{"foox", "NB", "NB53EGGqrrb5E"},
				{"foox", "_J9..j2zz", "_J9..j2zzAYKMvO2BYRY"},
				{"foox", "$1$Szzz0yzz", "$1$Szzz0yzz$IYL49cd3t9bllsA7Jmz1M1"},
				{"foox", pgcryptoPasswordSalt, pgcryptoPasswordFoox},
				{"Hello world!", "$5$saltstring", pgcryptoSHA256Hello},
				{"Hello world!", "$6$saltstring", pgcryptoSHA512Hello},
			}
			for _, v := range vectors {
				for _, prepared := range []bool{false, true} {
					s := pgcryptoPolicyText(r, q, v.hash, v.password, v.salt)
					s.Prepared = prepared
					run(s)
				}
			}
			for _, algorithm := range []struct{ name string; count int }{{"des", 25}, {"xdes", 1}, {"md5", 1000}, {"bf", 4}, {"sha256crypt", 1000}, {"sha512crypt", 1000}} {
				var salt string
				if err := c.QueryRowContext(p.Ctx, "SELECT pgcrypto_part.gen_salt($1,$2)", algorithm.name, algorithm.count).Scan(&salt); err != nil { t.Fatal(err) }
				if algorithm.name == "sha256crypt" || algorithm.name == "sha512crypt" {
					prefix := "$5$rounds=1000$"
					if algorithm.name == "sha512crypt" { prefix = "$6$rounds=1000$" }
					if !strings.HasPrefix(salt, prefix) || len(salt) != len(prefix)+16 { t.Fatal("SHA salt", salt) }
				} else {
					pgcryptoPasswordAlgorithmSaltShape(t, salt, algorithm.name, algorithm.count)
				}
				run(fixture.Select("SELECT pgcrypto_part.crypt('password',pgcrypto_part.crypt('password',$1))=pgcrypto_part.crypt('password',$1) AS correct,pgcrypto_part.crypt('wrong',pgcrypto_part.crypt('password',$1))=pgcrypto_part.crypt('password',$1) AS wrong", []string{"correct", "wrong"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}, salt))
			}
			run(fixture.Exec("SET "+pgcryptoPolicyName+"=fips", 0))
			run(fixture.Select("SELECT pgcrypto_part.fips_mode() AS value", []string{"value"}, []string{"BOOL"}, [][]any{{pgcryptoRandomFIPSExpected(pg)}}))
			for _, v := range vectors {
				s := pgcryptoPolicyText(r, q, v.hash, v.password, v.salt)
				if pgcryptoRandomFIPSExpected(pg) { s = pgcryptoPolicyFailure(q, pgcryptoPolicyFIPS, v.password, v.salt) }
				run(s)
				run(fixture.Prepared(s))
			}
			for _, algorithm := range []string{"des", "xdes", "md5", "bf", "sha256crypt", "sha512crypt"} {
				for _, q := range []string{"SELECT pgcrypto_part.gen_salt($1) IS NOT NULL AS value", "SELECT pgcrypto_part.gen_salt($1,0) IS NOT NULL AS value"} {
					s := fixture.Select(q, []string{"value"}, []string{"BOOL"}, [][]any{{true}}, algorithm)
					if pgcryptoRandomFIPSExpected(pg) { s = pgcryptoPolicyFailure(q, pgcryptoPolicyFIPS, algorithm) }
					run(s)
					run(fixture.Prepared(s))
				}
			}
			run(fixture.Exec("SET "+pgcryptoPolicyName+"=off", 0))
			for _, v := range vectors {
				run(pgcryptoPolicyFailure(q, pgcryptoPolicyOff, v.password, v.salt))
				run(fixture.Prepared(pgcryptoPolicyFailure(q, pgcryptoPolicyOff, v.password, v.salt)))
			}
			for _, salt := range []string{"", "invalid", "$2a$03$......................", "$5$rounds=10$!", "$6$rounds=oops$!"} {
				run(pgcryptoPolicyFailure(q, pgcryptoPolicyOff, "p", salt))
				run(fixture.Prepared(pgcryptoPolicyFailure(q, pgcryptoPolicyOff, "p", salt)))
			}
			for _, algorithm := range []string{"des", "xdes", "md5", "bf", "sha256crypt", "sha512crypt", "no such algorithm", ""} {
				for _, count := range []int{0, -1, 3, 1000000000} {
					s := pgcryptoPolicyFailure("SELECT pgcrypto_part.gen_salt($1,$2) AS value", pgcryptoPolicyOff, algorithm, count)
					run(s)
					run(fixture.Prepared(s))
				}
				s := pgcryptoPolicyFailure("SELECT pgcrypto_part.gen_salt($1) AS value", pgcryptoPolicyOff, algorithm)
				run(s)
				run(fixture.Prepared(s))
			}
			pgcryptoPolicyNotices(t, notices)
			for _, s := range []fixture.Step{
				pgcryptoPolicyText(r, "SELECT pgcrypto_part.crypt(NULL::text,'$5$rounds=10$!') AS value", nil),
				pgcryptoPolicyText(r, "SELECT pgcrypto_part.crypt('p',NULL::text) AS value", nil),
				pgcryptoPolicyText(r, "SELECT pgcrypto_part.gen_salt(NULL::text) AS value", nil),
				pgcryptoPolicyText(r, "SELECT pgcrypto_part.gen_salt(NULL::text,-1) AS value", nil),
				pgcryptoPolicyText(r, "SELECT pgcrypto_part.gen_salt('invalid',NULL::integer) AS value", nil),
				pgcryptoPolicyText(r, q, nil, nil, "$5$rounds=10$!"), pgcryptoPolicyText(r, q, nil, "p", nil),
				pgcryptoPolicyText(r, "SELECT pgcrypto_part.gen_salt($1) AS value", nil, nil),
				pgcryptoPolicyText(r, "SELECT pgcrypto_part.gen_salt($1,$2) AS value", nil, nil, -1),
				pgcryptoPolicyText(r, "SELECT pgcrypto_part.gen_salt($1,$2) AS value", nil, "invalid", nil),
			} { run(s); run(fixture.Prepared(s)) }
			pgcryptoPolicyNotices(t, notices)
			// Unrelated members still perform genuine work, not policy exceptions.
			pgcryptoPolicyActivate(t, p, c, r)
			pgcryptoResult(t, p, c, r, "value", "SELECT pgcrypto_part.hmac('what do ya want for nothing?','Jefe','sha256') AS value", "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843")
			run(fixture.Select("SELECT pgcrypto_part.decrypt(pgcrypto_part.encrypt('abc'::bytea,'key'::bytea,'aes'),'key'::bytea,'aes') AS value", []string{"value"}, []string{"BYTEA"}, [][]any{{"abc"}}))
			run(fixture.Select("SELECT length(pgcrypto_part.gen_random_bytes(16)) AS size,pgcrypto_part.gen_random_uuid() IS NOT NULL AS uuid,pgcrypto_part.fips_mode() AS mode", []string{"size", "uuid", "mode"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(16), true, pgcryptoRandomFIPSExpected(pg)}}))
		})
	})
	t.Run("RuntimeNullDemandAndAtomicity", func(t *testing.T) {
		pgcryptoPolicyCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			run(fixture.Exec("CREATE TABLE policy_null(p text,n integer)", 0))
			run(fixture.Exec("INSERT INTO policy_null VALUES(NULL,0)", 1))
			run(fixture.Exec("CREATE SEQUENCE policy_demand_seq", 0))
			run(fixture.Exec("SET "+pgcryptoPolicyName+"=off", 0))
			for _, q := range []string{"SELECT pgcrypto_part.crypt(p,(1/n)::text) FROM policy_null", "SELECT pgcrypto_part.gen_salt(p,1/n) FROM policy_null"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s); run(fixture.Prepared(s))
			}
			for _, q := range []string{"SELECT pgcrypto_part.crypt(p,nextval('policy_demand_seq')::text) AS value FROM policy_null", "SELECT pgcrypto_part.gen_salt(p,nextval('policy_demand_seq')::integer) AS value FROM policy_null"} {
				run(pgcryptoPolicyText(r, q, nil)); run(fixture.Prepared(pgcryptoPolicyText(r, q, nil)))
			}
			run(fixture.Select("SELECT currval('policy_demand_seq') AS value", []string{"value"}, []string{"INT8"}, [][]any{{int64(4)}}))
			run(fixture.Exec("CREATE TABLE policy_atomic(id integer,p text,s text,h text)", 0))
			run(fixture.Exec("INSERT INTO policy_atomic VALUES(0,'kept','kept','kept')", 1))
			for _, transaction := range []bool{false, true} {
				if transaction { run(fixture.Exec("BEGIN", 0)) }
				s := pgcryptoPolicyFailure("INSERT INTO policy_atomic SELECT id,p,s,pgcrypto_part.crypt(p,s) FROM (VALUES(1,NULL::text,'NB'),(2,'foox','NB')) AS source(id,p,s) ORDER BY id", pgcryptoPolicyOff)
				s.SelectRows = false
				run(s)
				if transaction {
					run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					run(fixture.Exec("ROLLBACK", 0))
				}
				run(fixture.Select("SELECT id,p,s,h FROM policy_atomic ORDER BY id", []string{"id", "p", "s", "h"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(0), "kept", "kept", "kept"}}))
			}
			pgcryptoPolicyNotices(t, notices)
			if !pg {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				rows, err := c.QueryContext(ctx, "SELECT pgcrypto_part.gen_salt('des')")
				if rows != nil { rows.Close(); t.Fatal("canceled call returned rows") }
				if !errors.Is(err, context.Canceled) { t.Fatal("precanceled policy call", err) }
			}
		})
	})
	t.Run("CoercionBeforePolicy", func(t *testing.T) {
		pgcryptoPolicyCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			run(fixture.Exec("SET "+pgcryptoPolicyName+"=off", 0))
			for _, v := range []struct{ input, code, message string }{{"oops", "22P02", `invalid input syntax for type integer: "oops"`}, {"2147483648", "22003", `value "2147483648" is out of range for type integer`}} {
				for _, prefix := range []string{"", "/*é界*/ "} {
					q := prefix + "SELECT pgcrypto_part.gen_salt('bf','" + v.input + "')"
					pos := utf8.RuneCountInString(q[:strings.Index(q, "'"+v.input+"'")]) + 1
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: v.code, Message: v.message, Position: strconv.Itoa(pos)})
					s.SelectRows, s.ErrorStage = true, "execute"
					run(s)
					s.Prepared, s.ErrorStage = true, "prepare"
					run(s)
				}
				q := "SELECT pgcrypto_part.gen_salt('bf',$1)"
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: v.code, Message: v.message, Context: "unnamed portal parameter $1 = '...'"}, v.input)
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s); run(fixture.Prepared(s))
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil { t.Fatal(err) }
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, r, s)
				stmt.Close()
			}
			s := extensionMissing("SELECT pgcrypto_part.gen_salt('bf',1::bigint)", "pgcrypto_part.gen_salt", "unknown, bigint")
			run(s)
			s.Prepared, s.ErrorStage = true, "prepare"
			run(s)
			pgcryptoPolicyShow(t, p, c, r, "off")
			pgcryptoPolicyNotices(t, notices)
		})
	})
}

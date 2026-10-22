package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPasswordSHAParsingAndDiagnostics(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		q := "SELECT crypt($1,$2) AS value"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, id := range []string{"5", "6"} {
			for i, tc := range []struct{ suffix, code, message string }{
				{"rounds=x$salt", "42601", "could not parse salt options"},
				{"rounds=1000x$salt", "42601", "could not parse salt options"},
				{"rounds=1000", "42601", "could not parse salt options"},
				{"bad!", "22023", `invalid character in salt string: "!"`},
				{"é", "22023", `invalid character in salt string: "é"`},
				{"界", "22023", `invalid character in salt string: "界"`},
				{"salt$5$x", "XX000", "bogus magic byte found in salt string"},
				{"salt$6$x", "XX000", "bogus magic byte found in salt string"},
				{"salt$rounds=1000", "XX000", "invalid rounds option specified in salt string"},
				{"1234567890123456$5$x", "XX000", "bogus magic byte found in salt string"},
				{"1234567890123456$6$x", "XX000", "bogus magic byte found in salt string"},
				{"1234567890123456rounds=1000", "XX000", "invalid rounds option specified in salt string"},
				{"!rounds=x$5$x", "XX000", "bogus magic byte found in salt string"},
				{"!rounds=x", "XX000", "invalid rounds option specified in salt string"},
			} {
				t.Run(fmt.Sprintf("sha%s_error_%d", id, i), func(t *testing.T) {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message}, "p", "$"+id+"$"+tc.suffix)
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, r, s)
					fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, r, s)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, r, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{pgcryptoSHA256Hello}}, "Hello world!", "$5$saltstring"))
				})
			}
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE pgcrypto_sha_atomic(p text,s text,h text)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_sha_atomic VALUES('kept','kept','kept')", 1))
		write := fixture.ImplicitError("INSERT INTO pgcrypto_sha_atomic SELECT p,s,crypt(p,s) FROM (VALUES ('Hello world!','$5$saltstring'),('p','$6$bad!')) AS source(p,s)", fixture.Diagnostic{Code: "22023", Message: `invalid character in salt string: "!"`})
		write.ErrorStage = "execute"
		for _, transaction := range []bool{false, true} {
			if transaction {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
			}
			fixture.RunStep(t, p.Ctx, c, r, write)
			if transaction {
				fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK", 0))
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT p,s,h FROM pgcrypto_sha_atomic", []string{"p", "s", "h"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"kept", "kept", "kept"}}))
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE pgcrypto_sha_null(p text,n integer)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_sha_null VALUES(NULL,0)", 1))
		for _, query := range []string{"SELECT crypt(p,(1/n)::text) FROM pgcrypto_sha_null", "SELECT gen_salt(p,1/n) FROM pgcrypto_sha_null"} {
			s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, r, s)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
		}
	})
}

func pgcryptoSHAClamp(rounds string, upper bool) fixture.Diagnostic {
	message := "rounds=" + rounds + " is below supported value (1000), using 1000 instead"
	if upper {
		message = "rounds=" + rounds + " exceeds maximum supported value (999999999), using 999999999 instead"
	}
	return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "22003", Message: message}
}

func TestReferencePgcryptoPasswordSHANotices(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		var notices []fixture.Diagnostic
		_, nc, nr := fixture.NoticeReferencePool(t, p, pg, &notices)
		q := "SELECT crypt($1,$2) AS value"
		stmt, err := nc.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, id := range []string{"5", "6"} {
			for _, tc := range []struct {
				rounds string
				upper  bool
			}{{"10", false}, {"1000000000", true}} {
				for _, mode := range []string{"direct", "prepared", "held"} {
					t.Run("sha"+id+"_"+tc.rounds+"_"+mode, func(t *testing.T) {
						notices = nil
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: `invalid character in salt string: "!"`}, "p", "$"+id+"$rounds="+tc.rounds+"$!")
						s.ErrorStage = "execute"
						var executor fixture.Executor = nc
						if mode == "prepared" {
							s = fixture.Prepared(s)
						}
						if mode == "held" {
							executor = fixture.HeldStatement{Executor: nc, Statement: stmt, SQL: q}
						}
						fixture.RunStep(t, p.Ctx, executor, nr, s)
						want := []fixture.Diagnostic{pgcryptoSHAClamp(tc.rounds, tc.upper)}
						if !reflect.DeepEqual(notices, want) {
							t.Errorf("notices=%#v want=%#v", notices, want)
						}
					})
				}
			}
			for _, setting := range []string{"rounds=oops$!", "rounds=1000$!", "rounds=999999999$!"} {
				notices = nil
				code, message := "22023", `invalid character in salt string: "!"`
				if setting == "rounds=oops$!" {
					code, message = "42601", "could not parse salt options"
				}
				fixture.RunStep(t, p.Ctx, nc, nr, fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, "p", "$"+id+"$"+setting))
				if len(notices) != 0 {
					t.Errorf("unexpected notices setting=%q: %#v", setting, notices)
				}
			}
		}
		notices = nil
		pgcryptoPasswordText(t, p, nc, nr, q, nil, nil, "$5$rounds=1000000000$!")
		if len(notices) != 0 {
			t.Errorf("strict NULL notices=%#v", notices)
		}
		fixture.RunStep(t, p.Ctx, nc, nr, fixture.Exec("SET client_min_messages=warning", 0))
		notices = nil
		fixture.RunStep(t, p.Ctx, nc, nr, fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: `invalid character in salt string: "!"`}, "p", "$6$rounds=10$!"))
		if len(notices) != 0 {
			t.Errorf("filtered notices=%#v", notices)
		}
		fixture.RunStep(t, p.Ctx, nc, nr, fixture.Exec("SET client_min_messages=notice", 0))
		for i, hash := range []string{"$5$rounds=1000$roundstoolow$yfvwcWrQ8l/K0DAWyuPMDNHpIVlTQebY9l/gL972bIC", "$6$rounds=1000$roundstoolow$kUMsbe306n21p9R.FRkW3IGn.S9NPN0x50YhH1xhLsPuWGsUSklZt58jaTfF4ZEQpyUNGc0dqbpBYYBaHHrsX."} {
			notices = nil
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: nc, Statement: stmt, SQL: q}, nr, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{hash}}, "the minimum number is still observed", fmt.Sprintf("$%d$rounds=10$roundstoolow", i+5)))
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{pgcryptoSHAClamp("10", false)}) {
				t.Errorf("lower clamp result notices=%#v", notices)
			}
		}
		// Pinned18.6 strtol-to-int32 behavior, including signed64 saturation.
		// Invalid salt prevents any successful maximum-round hashing.
		for _, id := range []string{"5", "6"} {
			for i, tc := range []struct{ rounds, clamped string }{
				{"+01000", ""}, {" \t1000", ""}, {"", "0"}, {"-1", "-1"},
				{"2147483648", "-2147483648"}, {"4294968296", ""},
				{"9223372036854775808", "-1"}, {"-9223372036854775809", "0"},
			} {
				for _, mode := range []string{"direct", "prepared", "held"} {
					t.Run(fmt.Sprintf("sha%s_numeric_%d_%s", id, i, mode), func(t *testing.T) {
						notices = nil
						setting := "$" + id + "$rounds=" + tc.rounds + "$!"
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: `invalid character in salt string: "!"`}, "p", setting)
						s.ErrorStage = "execute"
						var executor fixture.Executor = nc
						if mode == "prepared" {
							s = fixture.Prepared(s)
						}
						if mode == "held" {
							executor = fixture.HeldStatement{Executor: nc, Statement: stmt, SQL: q}
						}
						fixture.RunStep(t, p.Ctx, executor, nr, s)
						var want []fixture.Diagnostic
						if tc.clamped != "" {
							want = []fixture.Diagnostic{pgcryptoSHAClamp(tc.clamped, false)}
						}
						if !reflect.DeepEqual(notices, want) {
							t.Errorf("setting=%q notices=%#v want=%#v", setting, notices, want)
						}
					})
				}
			}
		}
	})
}

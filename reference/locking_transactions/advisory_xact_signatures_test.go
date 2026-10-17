package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAdvisoryXactSignatures(t *testing.T) {
	advisoryXactScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := advisoryXactConn(t, p, b), advisoryXactConn(t, p, b)
		for _, k := range advisorySessionKeys {
			for _, shared := range []bool{false, true} {
				for _, try := range []bool{false, true} {
					name := advisoryXactName(true, shared, try)
					for _, route := range []string{"query", "exec", "prepared-query", "prepared-exec"} {
						t.Run(name+"/"+k.args+"/"+route, func(t *testing.T) {
							q := k.call(name)
							var x fixture.Executor = a
							var args []any
							if strings.HasPrefix(route, "prepared") {
								q = "SELECT " + name + "($1)"
								args = []any{int64(4294967298)}
								if k.space == 2 {
									q = "SELECT " + name + "($1,$2)"
									args = []any{int32(1), int32(2)}
								}
								// Preparation succeeds while another participant holds the key.
								advisorySessionValue(t, p.Ctx, peer, k.call("pg_advisory_lock"), "VOID", "")
								x = advisoryXactHeld(t, p, a, q)
								advisorySessionClear(t, p.Ctx, peer)
							}
							for _, end := range []string{"COMMIT", "ROLLBACK", "COMMIT"} {
								advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
								typ, want := "VOID", any("")
								if try {
									typ, want = "BOOL", true
								}
								if strings.HasSuffix(route, "exec") {
									advisorySessionExec(t, p.Ctx, x, q, 1, args...)
								} else {
									s := fixture.Select(q, []string{name}, []string{typ}, [][]any{{want}}, args...)
									s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { advisorySessionMetadata(t, rows, b.PG) }
									fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
								}
								advisoryXactProbe(t, p, peer, k, false, false)
								other := advisorySessionKeys[2-k.space]
								advisoryXactProbe(t, p, peer, other, false, true)
								advisorySessionExec(t, p.Ctx, a, end, 0)
								advisoryXactProbe(t, p, peer, k, false, true)
							}
						})
					}
					typ := "VOID"
					if try {
						typ = "BOOL"
					}
					for _, args := range []string{"NULL::bigint", "NULL::integer,2::integer", "1::integer,NULL::integer"} {
						for _, prepared := range []bool{false, true} {
							s := fixture.Select("SELECT "+name+"("+args+") AS result", []string{"result"}, []string{typ}, [][]any{{nil}})
							s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { advisorySessionMetadata(t, rows, b.PG) }
							if prepared {
								s = fixture.Prepared(s)
							}
							fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
						}
					}
				}
			}
		}
		for _, args := range []string{"'-9223372036854775808'", "'9223372036854775807'", "-1::bigint", "'-2147483648'::integer,2147483647::integer", "2147483647::integer,'-2147483648'::integer", "42::smallint", "42::integer"} {
			advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
			advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_xact_lock("+args+")", "VOID", "")
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock("+args+")", "BOOL", false)
			advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
		}
		for _, spelling := range []string{"PG_ADVISORY_XACT_LOCK", "pg_catalog.pg_advisory_xact_lock", `"pg_advisory_xact_lock"`, `"pg_catalog"."pg_advisory_xact_lock"`} {
			advisorySessionValue(t, p.Ctx, a, "SELECT "+spelling+"(42)", "VOID", "")
		}
		for _, pair := range []bool{false, true} {
			q := "SELECT pg_try_advisory_xact_lock($1)"
			keys := [][]any{{int64(-1)}, {int64(42)}, {int64(9223372036854775807)}}
			if pair {
				q = "SELECT pg_try_advisory_xact_lock($1,$2)"
				keys = [][]any{{int32(1), int32(2)}, {int32(-2147483648), int32(2147483647)}, {int32(2), int32(1)}}
			}
			x := advisoryXactHeld(t, p, a, q)
			for _, args := range keys {
				advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
				advisorySessionValue(t, p.Ctx, x, q, "BOOL", true, args...)
				peerQ := strings.Replace(q, "pg_try_advisory_xact_lock", "pg_try_advisory_lock", 1)
				advisorySessionValue(t, p.Ctx, peer, peerQ, "BOOL", false, args...)
				advisorySessionExec(t, p.Ctx, a, "COMMIT", 0)
			}
		}
	})
}

func TestReferenceAdvisoryXactDiagnosticsStrictness(t *testing.T) {
	advisoryXactScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := advisoryXactConn(t, p, b), advisoryXactConn(t, p, b)
		for _, name := range []string{"pg_advisory_xact_lock", "pg_advisory_xact_lock_shared", "pg_try_advisory_xact_lock", "pg_try_advisory_xact_lock_shared"} {
			for _, tc := range []struct{ args, sig string }{
				{"", ""}, {"1,2,3", "integer, integer, integer"}, {"true", "boolean"}, {"1::bigint,2::bigint", "bigint, bigint"}, {"key => 1", "key => integer"}, {"VARIADIC '{1}'::integer[]", "integer[]"},
			} {
				q := "SELECT " + name + "(" + tc.args + ")"
				want := advisorySessionTimeout("42883", "function "+name+"("+tc.sig+") does not exist")
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				want.Position = "8"
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, want)
					s.SelectRows = true
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
				}
			}
			for _, tc := range []struct{ args, code, message string }{
				{"'bad'", "22P02", `invalid input syntax for type bigint: "bad"`},
				{"'9223372036854775808'", "22003", `value "9223372036854775808" is out of range for type bigint`},
				{"1,'2147483648'", "22003", `value "2147483648" is out of range for type integer`},
			} {
				q := "SELECT " + name + "(" + tc.args + ")"
				want := advisorySessionTimeout(tc.code, tc.message)
				want.Position = fmt.Sprint(strings.Index(q, "'") + 1)
				advisorySessionError(t, p.Ctx, a, q, want)
			}
		}
		for _, tc := range []struct{ q, code, message, token string }{
			{`SELECT "PG_ADVISORY_XACT_LOCK"(1)`, "42883", "function PG_ADVISORY_XACT_LOCK(integer) does not exist", `"PG_ADVISORY_XACT_LOCK"`},
			{"SELECT missing_advisory.pg_advisory_xact_lock(1)", "3F000", `schema "missing_advisory" does not exist`, "missing_advisory"},
			{"SELECT pg_advisory_xact_lock(1, missing_advisory_column)", "42703", `column "missing_advisory_column" does not exist`, "missing_advisory_column"},
			{"SELECT pg_advisory_xact_lock(" + strings.Repeat("1,", 100) + "1)", "54023", "cannot pass more than 100 arguments to a function", "pg_advisory_xact_lock"},
			{"SELECT pg_advisory_xact_lock(1,)", "42601", `syntax error at or near ")"`, ")"},
		} {
			want := advisorySessionTimeout(tc.code, tc.message)
			want.Position = fmt.Sprint(strings.Index(tc.q, tc.token) + 1)
			if tc.code == "42883" {
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			}
			advisorySessionError(t, p.Ctx, a, tc.q, want)
		}
		advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_xact_lock(NULL::integer,CASE WHEN pg_try_advisory_lock(77) THEN 2 ELSE 3 END)", "VOID", nil)
		advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(77)", "BOOL", true)
		advisorySessionClear(t, p.Ctx, peer)
		advisorySessionError(t, p.Ctx, a, "SELECT pg_advisory_xact_lock(NULL::integer,('bad'::text)::integer)", advisorySessionTimeout("22P02", `invalid input syntax for type integer: "bad"`))
		advisorySessionExec(t, p.Ctx, a, "CREATE TABLE advisory_xact_input(k bigint,n integer)", 0)
		advisorySessionExec(t, p.Ctx, a, "INSERT INTO advisory_xact_input VALUES(51,NULL)", 1)
		advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_xact_lock(n,CASE WHEN pg_try_advisory_lock(78) THEN 2 ELSE 3 END) FROM advisory_xact_input", "VOID", nil)
		advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(78)", "BOOL", false)
		advisorySessionClear(t, p.Ctx, a)
		advisorySessionExec(t, p.Ctx, a, "CREATE VIEW advisory_xact_view AS SELECT pg_try_advisory_xact_lock(k) AS result FROM advisory_xact_input", 0)
		for _, q := range []string{"SELECT result FROM advisory_xact_view", "SELECT CASE WHEN n IS NULL THEN pg_try_advisory_xact_lock(k) ELSE false END AS result FROM advisory_xact_input"} {
			for _, prepared := range []bool{false, true} {
				advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
				s := fixture.Select(q, []string{"result"}, []string{"BOOL"}, [][]any{{true}})
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
				advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(51)", "BOOL", false)
				advisorySessionExec(t, p.Ctx, a, "ROLLBACK", 0)
				advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(51)", "BOOL", true)
				advisorySessionClear(t, p.Ctx, peer)
			}
		}
	})
}

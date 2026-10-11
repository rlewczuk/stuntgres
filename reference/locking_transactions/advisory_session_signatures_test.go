package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAdvisorySessionSignatures(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := advisorySessionConn(t, p, b), advisorySessionConn(t, p, b)
			for _, k := range advisorySessionKeys {
				for _, name := range []string{"pg_advisory_lock", "pg_advisory_lock_shared", "pg_try_advisory_lock", "pg_try_advisory_lock_shared", "pg_advisory_unlock", "pg_advisory_unlock_shared"} {
					t.Run(name+"/"+k.args, func(t *testing.T) {
						for _, route := range []string{"query", "exec", "prepared-query", "prepared-exec"} {
							advisorySessionClear(t, p.Ctx, a)
							shared := strings.HasSuffix(name, "_shared")
							acquire := "pg_advisory_lock"
							if shared {
								acquire += "_shared"
							}
							unlock := strings.HasPrefix(name, "pg_advisory_unlock")
							if unlock {
								advisorySessionValue(t, p.Ctx, a, k.call(acquire), "VOID", "")
							}
							q := k.call(name)
							typ, want := "BOOL", any(true)
							if !unlock && !strings.HasPrefix(name, "pg_try") {
								typ, want = "VOID", ""
							}
							var exec fixture.Executor = a
							var stmt *sql.Stmt
							var args []any
							if strings.HasPrefix(route, "prepared") {
								q = "SELECT " + name + "($1)"
								args = []any{int64(4294967298)}
								if k.space == 2 {
									q = "SELECT " + name + "($1,$2)"
									args = []any{int32(1), int32(2)}
								}
								var err error
								stmt, err = a.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								exec = fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}
								// Preparation neither acquires nor unlocks.
								advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", !unlock)
								advisorySessionClear(t, p.Ctx, peer)
							}
							if strings.HasSuffix(route, "exec") {
								advisorySessionExec(t, p.Ctx, exec, q, 1, args...)
							} else {
								s := fixture.Select(q, []string{name}, []string{typ}, [][]any{{want}}, args...)
								s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { advisorySessionMetadata(t, rows, b.PG) }
								fixture.RunStep(t, p.Ctx, exec, b.Recorder, s)
							}
							if stmt != nil {
								if err := stmt.Close(); err != nil {
									t.Fatal(err)
								}
							}
							advisorySessionValue(t, p.Ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", unlock)
							advisorySessionClear(t, p.Ctx, peer)
							advisorySessionClear(t, p.Ctx, a)
						}
					})
				}
			}
			for _, prepared := range []bool{false, true} {
				q := "SELECT pg_advisory_unlock_all()"
				s := fixture.Select(q, []string{"pg_advisory_unlock_all"}, []string{"VOID"}, [][]any{{""}})
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { advisorySessionMetadata(t, rows, b.PG) }
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
				e := fixture.Exec(q, 1)
				if prepared {
					e = fixture.Prepared(e)
				}
				fixture.RunStep(t, p.Ctx, a, b.Recorder, e)
			}
			for _, name := range []string{"pg_advisory_lock", "pg_advisory_lock_shared", "pg_try_advisory_lock", "pg_try_advisory_lock_shared", "pg_advisory_unlock", "pg_advisory_unlock_shared"} {
				typ := "BOOL"
				if name == "pg_advisory_lock" || name == "pg_advisory_lock_shared" {
					typ = "VOID"
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
			for _, args := range []string{"'-9223372036854775808'", "'9223372036854775807'", "-1::bigint", "'-2147483648'::integer,2147483647::integer", "2147483647::integer,'-2147483648'::integer", "42::smallint", "42::integer"} {
				advisorySessionValue(t, p.Ctx, a, "SELECT pg_advisory_lock("+args+")", "VOID", "")
				advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock("+args+")", "BOOL", false)
				advisorySessionClear(t, p.Ctx, a)
			}
			for _, name := range []string{"PG_ADVISORY_LOCK", "pg_catalog.pg_advisory_lock", `"pg_advisory_lock"`, `"pg_catalog"."pg_advisory_lock"`} {
				advisorySessionValue(t, p.Ctx, a, "SELECT "+name+"(42)", "VOID", "")
				advisorySessionClear(t, p.Ctx, a)
			}
			stmt, err := a.PrepareContext(p.Ctx, "SELECT pg_try_advisory_lock($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, key := range []int64{-1, 42, 9223372036854775807} {
				exec := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: "SELECT pg_try_advisory_lock($1)"}
				advisorySessionValue(t, p.Ctx, exec, "SELECT pg_try_advisory_lock($1)", "BOOL", true, key)
				advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock($1)", "BOOL", false, key)
				advisorySessionClear(t, p.Ctx, a)
			}
			pairStmt, err := a.PrepareContext(p.Ctx, "SELECT pg_try_advisory_lock($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer pairStmt.Close()
			for _, key := range [][2]int32{{1, 2}, {-2147483648, 2147483647}, {2, 1}} {
				exec := fixture.HeldStatement{Executor: a, Statement: pairStmt, SQL: "SELECT pg_try_advisory_lock($1,$2)"}
				advisorySessionValue(t, p.Ctx, exec, "SELECT pg_try_advisory_lock($1,$2)", "BOOL", true, key[0], key[1])
				advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock($1,$2)", "BOOL", false, key[0], key[1])
				advisorySessionClear(t, p.Ctx, a)
			}
		})
	}
}

func TestReferenceAdvisorySessionDiagnosticsConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, peer := advisorySessionConn(t, p, b), advisorySessionConn(t, p, b)
			cases := []struct{ q, signature string }{
				{"SELECT pg_advisory_lock()", "pg_advisory_lock()"},
				{"SELECT pg_advisory_lock(1,2,3)", "pg_advisory_lock(integer, integer, integer)"},
				{"SELECT pg_advisory_unlock_all(1)", "pg_advisory_unlock_all(integer)"},
				{"SELECT pg_advisory_lock(true)", "pg_advisory_lock(boolean)"},
				{"SELECT pg_advisory_lock(1::bigint,2::bigint)", "pg_advisory_lock(bigint, bigint)"},
				{`SELECT "PG_ADVISORY_LOCK"(1)`, "PG_ADVISORY_LOCK(integer)"},
				{"SELECT public.pg_advisory_lock(1)", "public.pg_advisory_lock(integer)"},
				{"SELECT pg_advisory_lock(key => 1)", "pg_advisory_lock(key => integer)"},
				{"SELECT pg_advisory_lock(VARIADIC '{1}'::integer[])", "pg_advisory_lock(integer[])"},
			}
			for _, tc := range cases {
				want := advisorySessionTimeout("42883", "function "+tc.signature+" does not exist")
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				want.Position = "8"
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.q, want)
					s.SelectRows = true
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
			for _, tc := range []struct{ q, code, message string }{
				{"SELECT pg_advisory_lock('bad')", "22P02", `invalid input syntax for type bigint: "bad"`},
				{"SELECT pg_advisory_lock('9223372036854775808')", "22003", `value "9223372036854775808" is out of range for type bigint`},
				{"SELECT pg_advisory_lock(1,'2147483648')", "22003", `value "2147483648" is out of range for type integer`},
			} {
				want := advisorySessionTimeout(tc.code, tc.message)
				want.Position = fmt.Sprint(strings.Index(tc.q, "'") + 1)
				advisorySessionError(t, p.Ctx, c, tc.q, want)
			}
			for _, tc := range []struct{ q, code, message, position string }{
				{"SELECT missing_advisory.pg_advisory_lock(1)", "3F000", `schema "missing_advisory" does not exist`, "8"},
				{"SELECT pg_advisory_lock(1, missing_advisory_column)", "42703", `column "missing_advisory_column" does not exist`, "28"},
				{"SELECT pg_advisory_lock(NULL::integer,('bad'::text)::integer)", "22P02", `invalid input syntax for type integer: "bad"`, ""},
				{"SELECT pg_advisory_lock(" + strings.Repeat("1,", 100) + "1)", "54023", "cannot pass more than 100 arguments to a function", "8"},
			} {
				want := advisorySessionTimeout(tc.code, tc.message)
				want.Position = tc.position
				advisorySessionError(t, p.Ctx, c, tc.q, want)
			}
			advisorySessionExec(t, p.Ctx, c, "CREATE TABLE advisory_consumers(k bigint, v integer)", 0)
			advisorySessionExec(t, p.Ctx, c, "INSERT INTO advisory_consumers VALUES(51,1),(52,2)", 2)
			for _, q := range []string{
				"SELECT pg_try_advisory_lock(k) AS result FROM advisory_consumers WHERE v=1",
				"SELECT CASE WHEN v=1 THEN pg_try_advisory_lock(k) ELSE false END AS result FROM advisory_consumers WHERE v=1",
				"SELECT coalesce(pg_try_advisory_lock(k),false) AS result FROM advisory_consumers WHERE v=1",
				"SELECT pg_try_advisory_lock(k) AS result FROM advisory_consumers GROUP BY k,v HAVING v=1",
				"SELECT pg_try_advisory_lock(a.k) AS result FROM advisory_consumers a JOIN advisory_consumers b ON a.v=b.v WHERE a.v=1",
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.Select(q, []string{"result"}, []string{"BOOL"}, [][]any{{true}})
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(51)", "BOOL", false)
					advisorySessionClear(t, p.Ctx, c)
				}
			}
			advisorySessionExec(t, p.Ctx, c, "CREATE VIEW advisory_view AS SELECT pg_try_advisory_lock(k) AS result FROM advisory_consumers WHERE v=1", 0)
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(51)", "BOOL", true)
			advisorySessionClear(t, p.Ctx, peer)
			advisorySessionValue(t, p.Ctx, c, "SELECT result FROM advisory_view", "BOOL", true)
			advisorySessionClear(t, p.Ctx, c)
			// PostgreSQL folds strict calls with a constant NULL after child
			// simplification. The volatile sibling does not run in this case.
			advisorySessionValue(t, p.Ctx, c, "SELECT pg_advisory_lock(NULL::integer,CASE WHEN pg_try_advisory_lock(77) THEN 2 ELSE 3 END)", "VOID", nil)
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(77)", "BOOL", true)
			advisorySessionClear(t, p.Ctx, peer)
			// A runtime row-column NULL cannot be folded at plan time. All
			// executable children are evaluated before runtime strictness.
			advisorySessionExec(t, p.Ctx, c, "CREATE TABLE advisory_null_input(n integer)", 0)
			advisorySessionExec(t, p.Ctx, c, "INSERT INTO advisory_null_input VALUES(NULL)", 1)
			advisorySessionValue(t, p.Ctx, c, "SELECT pg_advisory_lock(n,CASE WHEN pg_try_advisory_lock(78) THEN 2 ELSE 3 END) FROM advisory_null_input", "VOID", nil)
			advisorySessionValue(t, p.Ctx, peer, "SELECT pg_try_advisory_lock(78)", "BOOL", false)
			advisorySessionClear(t, p.Ctx, c)
		})
	}
}

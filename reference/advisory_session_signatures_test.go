package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceAdvisorySessionSignatures(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, peer := advisorySessionConn(t, p, b), advisorySessionConn(t, p, b)
			for _, k := range advisorySessionKeys {
				for _, name := range []string{"pg_advisory_lock", "pg_advisory_lock_shared", "pg_try_advisory_lock", "pg_try_advisory_lock_shared", "pg_advisory_unlock", "pg_advisory_unlock_shared"} {
					t.Run(name+"/"+k.args, func(t *testing.T) {
						for _, route := range []string{"query", "exec", "prepared-query", "prepared-exec"} {
							advisorySessionClear(t, p.ctx, a)
							shared := strings.HasSuffix(name, "_shared")
							acquire := "pg_advisory_lock"
							if shared {
								acquire += "_shared"
							}
							unlock := strings.HasPrefix(name, "pg_advisory_unlock")
							if unlock {
								advisorySessionValue(t, p.ctx, a, k.call(acquire), "VOID", "")
							}
							q := k.call(name)
							typ, want := "BOOL", any(true)
							if !unlock && !strings.HasPrefix(name, "pg_try") {
								typ, want = "VOID", ""
							}
							var exec referenceExecutor = a
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
								stmt, err = a.PrepareContext(p.ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								exec = referenceHeldStatement{referenceExecutor: a, statement: stmt, query: q}
								// Preparation neither acquires nor unlocks.
								advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", !unlock)
								advisorySessionClear(t, p.ctx, peer)
							}
							if strings.HasSuffix(route, "exec") {
								advisorySessionExec(t, p.ctx, exec, q, 1, args...)
							} else {
								s := referenceSelect(q, []string{name}, []string{typ}, [][]any{{want}}, args...)
								s.checkMetadata = func(t *testing.T, rows *sql.Rows) { advisorySessionMetadata(t, rows, b.pg) }
								referenceRunStep(t, p.ctx, exec, b.recorder, s)
							}
							if stmt != nil {
								if err := stmt.Close(); err != nil {
									t.Fatal(err)
								}
							}
							advisorySessionValue(t, p.ctx, peer, k.call("pg_try_advisory_lock"), "BOOL", unlock)
							advisorySessionClear(t, p.ctx, peer)
							advisorySessionClear(t, p.ctx, a)
						}
					})
				}
			}
			for _, prepared := range []bool{false, true} {
				q := "SELECT pg_advisory_unlock_all()"
				s := referenceSelect(q, []string{"pg_advisory_unlock_all"}, []string{"VOID"}, [][]any{{""}})
				s.checkMetadata = func(t *testing.T, rows *sql.Rows) { advisorySessionMetadata(t, rows, b.pg) }
				if prepared {
					s = referencePrepared(s)
				}
				referenceRunStep(t, p.ctx, a, b.recorder, s)
				e := referenceExec(q, 1)
				if prepared {
					e = referencePrepared(e)
				}
				referenceRunStep(t, p.ctx, a, b.recorder, e)
			}
			for _, name := range []string{"pg_advisory_lock", "pg_advisory_lock_shared", "pg_try_advisory_lock", "pg_try_advisory_lock_shared", "pg_advisory_unlock", "pg_advisory_unlock_shared"} {
				typ := "BOOL"
				if name == "pg_advisory_lock" || name == "pg_advisory_lock_shared" {
					typ = "VOID"
				}
				for _, args := range []string{"NULL::bigint", "NULL::integer,2::integer", "1::integer,NULL::integer"} {
					for _, prepared := range []bool{false, true} {
						s := referenceSelect("SELECT "+name+"("+args+") AS result", []string{"result"}, []string{typ}, [][]any{{nil}})
						s.checkMetadata = func(t *testing.T, rows *sql.Rows) { advisorySessionMetadata(t, rows, b.pg) }
						if prepared {
							s = referencePrepared(s)
						}
						referenceRunStep(t, p.ctx, a, b.recorder, s)
					}
				}
			}
			for _, args := range []string{"'-9223372036854775808'", "'9223372036854775807'", "-1::bigint", "'-2147483648'::integer,2147483647::integer", "2147483647::integer,'-2147483648'::integer", "42::smallint", "42::integer"} {
				advisorySessionValue(t, p.ctx, a, "SELECT pg_advisory_lock("+args+")", "VOID", "")
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock("+args+")", "BOOL", false)
				advisorySessionClear(t, p.ctx, a)
			}
			for _, name := range []string{"PG_ADVISORY_LOCK", "pg_catalog.pg_advisory_lock", `"pg_advisory_lock"`, `"pg_catalog"."pg_advisory_lock"`} {
				advisorySessionValue(t, p.ctx, a, "SELECT "+name+"(42)", "VOID", "")
				advisorySessionClear(t, p.ctx, a)
			}
			stmt, err := a.PrepareContext(p.ctx, "SELECT pg_try_advisory_lock($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, key := range []int64{-1, 42, 9223372036854775807} {
				exec := referenceHeldStatement{referenceExecutor: a, statement: stmt, query: "SELECT pg_try_advisory_lock($1)"}
				advisorySessionValue(t, p.ctx, exec, "SELECT pg_try_advisory_lock($1)", "BOOL", true, key)
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock($1)", "BOOL", false, key)
				advisorySessionClear(t, p.ctx, a)
			}
			pairStmt, err := a.PrepareContext(p.ctx, "SELECT pg_try_advisory_lock($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer pairStmt.Close()
			for _, key := range [][2]int32{{1, 2}, {-2147483648, 2147483647}, {2, 1}} {
				exec := referenceHeldStatement{referenceExecutor: a, statement: pairStmt, query: "SELECT pg_try_advisory_lock($1,$2)"}
				advisorySessionValue(t, p.ctx, exec, "SELECT pg_try_advisory_lock($1,$2)", "BOOL", true, key[0], key[1])
				advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock($1,$2)", "BOOL", false, key[0], key[1])
				advisorySessionClear(t, p.ctx, a)
			}
		})
	}
}

func TestReferenceAdvisorySessionDiagnosticsConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
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
					s := referenceErrorDiagnostic(tc.q, want)
					s.selectRows = true
					if prepared {
						s = referencePrepared(s)
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, c, b.recorder, s)
				}
			}
			for _, tc := range []struct{ q, code, message string }{
				{"SELECT pg_advisory_lock('bad')", "22P02", `invalid input syntax for type bigint: "bad"`},
				{"SELECT pg_advisory_lock('9223372036854775808')", "22003", `value "9223372036854775808" is out of range for type bigint`},
				{"SELECT pg_advisory_lock(1,'2147483648')", "22003", `value "2147483648" is out of range for type integer`},
			} {
				want := advisorySessionTimeout(tc.code, tc.message)
				want.Position = fmt.Sprint(strings.Index(tc.q, "'") + 1)
				advisorySessionError(t, p.ctx, c, tc.q, want)
			}
			for _, tc := range []struct{ q, code, message, position string }{
				{"SELECT missing_advisory.pg_advisory_lock(1)", "3F000", `schema "missing_advisory" does not exist`, "8"},
				{"SELECT pg_advisory_lock(1, missing_advisory_column)", "42703", `column "missing_advisory_column" does not exist`, "28"},
				{"SELECT pg_advisory_lock(NULL::integer,('bad'::text)::integer)", "22P02", `invalid input syntax for type integer: "bad"`, ""},
				{"SELECT pg_advisory_lock(" + strings.Repeat("1,", 100) + "1)", "54023", "cannot pass more than 100 arguments to a function", "8"},
			} {
				want := advisorySessionTimeout(tc.code, tc.message)
				want.Position = tc.position
				advisorySessionError(t, p.ctx, c, tc.q, want)
			}
			advisorySessionExec(t, p.ctx, c, "CREATE TABLE advisory_consumers(k bigint, v integer)", 0)
			advisorySessionExec(t, p.ctx, c, "INSERT INTO advisory_consumers VALUES(51,1),(52,2)", 2)
			for _, q := range []string{
				"SELECT pg_try_advisory_lock(k) AS result FROM advisory_consumers WHERE v=1",
				"SELECT CASE WHEN v=1 THEN pg_try_advisory_lock(k) ELSE false END AS result FROM advisory_consumers WHERE v=1",
				"SELECT coalesce(pg_try_advisory_lock(k),false) AS result FROM advisory_consumers WHERE v=1",
				"SELECT pg_try_advisory_lock(k) AS result FROM advisory_consumers GROUP BY k,v HAVING v=1",
				"SELECT pg_try_advisory_lock(a.k) AS result FROM advisory_consumers a JOIN advisory_consumers b ON a.v=b.v WHERE a.v=1",
			} {
				for _, prepared := range []bool{false, true} {
					s := referenceSelect(q, []string{"result"}, []string{"BOOL"}, [][]any{{true}})
					if prepared {
						s = referencePrepared(s)
					}
					referenceRunStep(t, p.ctx, c, b.recorder, s)
					advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(51)", "BOOL", false)
					advisorySessionClear(t, p.ctx, c)
				}
			}
			advisorySessionExec(t, p.ctx, c, "CREATE VIEW advisory_view AS SELECT pg_try_advisory_lock(k) AS result FROM advisory_consumers WHERE v=1", 0)
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(51)", "BOOL", true)
			advisorySessionClear(t, p.ctx, peer)
			advisorySessionValue(t, p.ctx, c, "SELECT result FROM advisory_view", "BOOL", true)
			advisorySessionClear(t, p.ctx, c)
			// PostgreSQL folds strict calls with a constant NULL after child
			// simplification. The volatile sibling does not run in this case.
			advisorySessionValue(t, p.ctx, c, "SELECT pg_advisory_lock(NULL::integer,CASE WHEN pg_try_advisory_lock(77) THEN 2 ELSE 3 END)", "VOID", nil)
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(77)", "BOOL", true)
			advisorySessionClear(t, p.ctx, peer)
			// A runtime row-column NULL cannot be folded at plan time. All
			// executable children are evaluated before runtime strictness.
			advisorySessionExec(t, p.ctx, c, "CREATE TABLE advisory_null_input(n integer)", 0)
			advisorySessionExec(t, p.ctx, c, "INSERT INTO advisory_null_input VALUES(NULL)", 1)
			advisorySessionValue(t, p.ctx, c, "SELECT pg_advisory_lock(n,CASE WHEN pg_try_advisory_lock(78) THEN 2 ELSE 3 END) FROM advisory_null_input", "VOID", nil)
			advisorySessionValue(t, p.ctx, peer, "SELECT pg_try_advisory_lock(78)", "BOOL", false)
			advisorySessionClear(t, p.ctx, c)
		})
	}
}

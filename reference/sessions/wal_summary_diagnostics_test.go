package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceWALSummaryDiagnostics(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		type sample struct {
			q, code, message, hint string
			pos                    int
		}
		var cases []sample
		for i, name := range walNames {
			args := []struct{ sql, types string }{{"1", "integer"}, {"true", "boolean"}, {"NULL", "unknown"}, {"1,2", "integer, integer"}, {"x=>1", "x => integer"}}
			if i == 1 {
				args = append(args, struct{ sql, types string }{"", ""}, struct{ sql, types string }{"1,'0/0','0/1',1", "integer, unknown, unknown, integer"}, struct{ sql, types string }{"true,'invalid','0/1'", "boolean, unknown, unknown"}, struct{ sql, types string }{"1,'0/0'::text,'invalid'", "integer, text, unknown"}, struct{ sql, types string }{"1,'invalid','0/1'::text", "integer, unknown, text"}, struct{ sql, types string }{"bad=>1,start_lsn=>'0/0',end_lsn=>'0/1'", "bad => integer, start_lsn => unknown, end_lsn => unknown"})
			}
			for _, a := range args {
				q := "SELECT " + name + "(" + a.sql + ")"
				cases = append(cases, sample{q, "42883", "function " + name + "(" + a.types + ") does not exist", hint, 8})
			}
			supplied := ""
			types := ""
			if i == 1 {
				supplied = "1,'0/0','0/1'"
				types = "integer, unknown, unknown"
			}
			cases = append(cases, sample{"SELECT public." + name + "(" + supplied + ")", "42883", "function public." + name + "(" + types + ") does not exist", hint, 8}, sample{"SELECT absent_wal_schema." + name + "(" + supplied + ")", "3F000", `schema "absent_wal_schema" does not exist`, "", 8})
			cases = append(cases, sample{`SELECT "` + strings.ToUpper(name) + `"(` + supplied + `)`, "42883", "function " + strings.ToUpper(name) + "(" + types + ") does not exist", hint, 8})
		}
		for _, q := range []string{"SELECT pg_wal_summary_contents(tli=>1,tli=>2,end_lsn=>'0/1')", "SELECT pg_wal_summary_contents(tli=>1,'0/0','0/1')"} {
			message := `argument name "tli" used more than once`
			pos := strings.LastIndex(q, "tli=>") + 1
			if strings.Contains(q, "1,'0/0'") {
				message = "positional argument cannot follow named argument"
				pos = strings.Index(q, "'0/0'") + 1
			}
			cases = append(cases, sample{q, "42601", message, "", pos})
		}
		malformed := "SELECT pg_wal_summary_contents(NULL::bigint,'broken','0/1')"
		cases = append(cases, sample{malformed, "22P02", `invalid input syntax for type pg_lsn: "broken"`, "", strings.Index(malformed, "'broken'") + 1}, sample{"/*é🙂*/ SELECT pg_wal_summary_contents()", "42883", "function pg_wal_summary_contents() does not exist", hint, 15})
		overflow := "SELECT pg_wal_summary_contents('9223372036854775808'::bigint,'0/0','0/1')"
		cases = append(cases, sample{overflow, "22003", `value "9223372036854775808" is out of range for type bigint`, "", strings.Index(overflow, "'9223372036854775808'") + 1})
		for _, tc := range cases {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: fixture.Position(tc.pos)})
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
	})
}
func walHeldArguments(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT * FROM pg_wal_summary_contents($1::bigint,$2::pg_lsn,$3::pg_lsn)"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, tli := range []any{int64(0), nil, int64(1), int64(-1), nil} {
		s := walEmpty(q, 1, b.PG)
		if tli != nil {
			if tli.(int64) <= 0 {
				s = walFailure(q, "22023", fmt.Sprintf("invalid timeline %d", tli))
			} else {
				s = walContentsDemand(q, b.PG, 1, 0, 1)
			}
		}
		s.Args = []any{tli, "0/0", "0/1"}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	for _, q := range []string{"SELECT * FROM pg_wal_summary_contents(NULL::bigint,('0/0'::pg_lsn + $1::numeric),'0/1'::pg_lsn)", "SELECT * FROM pg_wal_summary_contents(tli=>NULL::bigint,end_lsn=>'0/1'::pg_lsn,start_lsn=>('0/0'::pg_lsn + $1::numeric))"} {
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, n := range []int64{-1, 0, -1, 0} {
			s := walEmpty(q, 1, b.PG)
			if n < 0 {
				s = walFailure(q, "22023", "pg_lsn out of range")
			}
			s.Args = []any{n}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		stmt.Close()
	}
}
func TestReferenceWALSummaryArguments(t *testing.T) {
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE wal_sentinel(id integer)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO wal_sentinel VALUES(7)", 1))
		for _, args := range []string{"NULL,'0/0','0/1'", "1,NULL,'0/1'", "1,'0/0',NULL", "NULL,NULL,NULL", "0,NULL,'0/1'", "end_lsn=>'0/1',tli=>NULL::bigint,start_lsn=>'0/0'"} {
			s := walEmpty("SELECT * FROM pg_wal_summary_contents("+args+")", 1, b.PG)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			recoveryHeld(t, p, b, c, s)
		}
		for _, n := range []int64{0, -1, 2147483648, -9223372036854775808, 9223372036854775807} {
			s := walFailure(fmt.Sprintf("SELECT * FROM pg_wal_summary_contents(%d,'0/0','0/1')", n), "22023", fmt.Sprintf("invalid timeline %d", n))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		for _, tc := range []struct {
			tli        int64
			start, end uint64
			a, z       string
		}{{1, 0, 0, "0/0", "0/0"}, {1, 2, 1, "0/2", "0/1"}, {2147483647, 0, ^uint64(0), "0/0", "FFFFFFFF/FFFFFFFF"}, {1, ^uint64(0), 0, "FFFFFFFF/FFFFFFFF", "0/0"}} {
			q := fmt.Sprintf("SELECT * FROM pg_wal_summary_contents(end_lsn=>'%s',tli=>%d,start_lsn=>'%s')", tc.z, tc.tli, tc.a)
			s := walContentsDemand(q, b.PG, tc.tli, tc.start, tc.end)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		walHeldArguments(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM wal_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}

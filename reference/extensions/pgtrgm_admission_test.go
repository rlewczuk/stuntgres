package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func pgTrgmTextAdmissionCases(t *testing.T) {
	t.Run("TextAdmissionAndRepeatedParameter", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
			q := "SELECT similarity($1,'cat'),$1 AS input"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				arg, score, text any
			}{
				{"cat", float64(1), "cat"},
				{nil, nil, nil},
				{"!!!", float64(0), "!!!"},
				{"café", pgTrgmReal(b.PG, 1, 3), "café"},
			} {
				s := fixture.Select(q, []string{"similarity", "input"}, []string{"FLOAT4", "TEXT"}, [][]any{{tc.score, tc.text}}, tc.arg)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			for _, tc := range []struct {
				arg     any
				message string
			}{
				{"ca\x00t", `invalid byte sequence for encoding "UTF8": 0x00`},
				{[]byte{0xff}, `invalid byte sequence for encoding "UTF8": 0xff`},
			} {
				for _, prepared := range []bool{false, true} {
					// Bind conversion precedes invocation. PostgreSQL 18.6
					// identifies the input parameter without echoing its bytes.
					s := pgTrgmFailure("SELECT show_trgm($1)", fixture.Diagnostic{
						Code: "22021", Message: tc.message, Context: "unnamed portal parameter $1",
					}, tc.arg)
					s.Prepared = prepared
					run(s)
				}
			}
			// A repeated inferred TEXT parameter is not independently retyped by
			// a later integer operator. This is the ordinary binder conflict.
			bad := "SELECT similarity($1,'cat'),$1+1"
			d := fixture.Diagnostic{
				Code: "42883", Message: "operator does not exist: text + integer", Position: "31",
				Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.",
			}
			s := pgTrgmFailure(bad, d, "cat")
			run(s)
			s.Prepared, s.ErrorStage = true, "prepare"
			run(s)
			pgTrgmNotices(t, notices)
		})
	})
}

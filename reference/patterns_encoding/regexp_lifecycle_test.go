package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegexpHeldParameters(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		regexpHeldSets(t, p, b)
		const q = `SELECT regexp_count($1,$2,$3,$4) AS n,regexp_instr($1,$2,$3,1,0,$4) AS i,regexp_replace($1,$2,$5,$3,0,$4) AS r,regexp_match($1,$2,$4) AS a`
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		var typedNil *string
		for _, c := range []struct {
			args []any
			rows [][]any
			msg  string
		}{
			{[]any{"é🙂x", "🙂", 1, "", "X"}, [][]any{{int64(1), int64(2), "éXx", "{🙂}"}}, ""},
			{[]any{"abc", "z", 1, "", "X"}, [][]any{{int64(0), int64(0), "abc", nil}}, ""},
			{[]any{typedNil, "[", 0, "z", nil}, [][]any{{nil, nil, nil, nil}}, ""},
			{[]any{"abc", "b", 0, "", "X"}, nil, `invalid value for parameter "start": 0`},
			{[]any{"AbA", "a", 1, "i", "X"}, [][]any{{int64(2), int64(1), "XbX", "{A}"}}, ""},
			{[]any{"abc", "b", 1, nil, "X"}, [][]any{{nil, nil, nil, nil}}, ""},
			{[]any{"abc", "b", 1, "", typedNil}, [][]any{{int64(1), int64(2), nil, "{b}"}}, ""},
		} {
			s := regexpRows(b, q, []string{"n", "i", "r", "a"}, []string{"INT4", "INT4", "TEXT", "_TEXT"}, c.rows, c.args...)
			if c.msg != "" {
				s = regexpRuntimeError(q, "22023", c.msg, "", c.args...)
			}
			t.Run(strings.Join([]string{"held", c.msg}, "/"), func(t *testing.T) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) })
		}
		for _, pattern := range []string{"[", "("} {
			reason := "brackets [] not balanced"
			if pattern == "(" {
				reason = "parentheses () not balanced"
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, similarReferenceRegexError(q, reason, "", "a", pattern, 1, "", "X"))
		}
		// Mutable binds and retained cells must not alias a subsequent execution.
		input, pattern, replacement := []byte("aba"), []byte("(a)"), []byte("X")
		rows, err := stmt.QueryContext(p.Ctx, input, pattern, 1, "", replacement)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		input[0] = 'z'
		pattern[1] = 'z'
		replacement[0] = 'Y'
		fixture.StringCoreRetainedRows(t, rows, nil, []string{"n", "i", "r", "a"}, []string{"INT4", "INT4", "TEXT", "_TEXT"}, [][]any{{int64(2), int64(1), "XbX", "{a}"}}, b.PG)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, regexpRows(b, q, []string{"n", "i", "r", "a"}, []string{"INT4", "INT4", "TEXT", "_TEXT"}, [][]any{{int64(1), int64(1), "Yba", "{z}"}}, input, pattern, 1, "", replacement))
	})
}

func regexpCancellationQueries() []struct {
	q      string
	cursor bool
} {
	return []struct {
		q      string
		cursor bool
	}{
		{`UPDATE cancel_regexp SET sentinel=99 WHERE regexp_count(s,p)>0`, false},
		{`UPDATE cancel_regexp SET sentinel=99 WHERE regexp_instr(s,p,1,2147483647)>0`, false},
		{`UPDATE cancel_regexp SET s=regexp_replace(s,p,'X','g'),sentinel=99`, false},
		{`UPDATE cancel_regexp SET sentinel=99 WHERE regexp_split_to_array(s,p) IS NOT NULL`, false},
		{`SELECT regexp_matches(s,p,'g') AS m FROM cancel_regexp`, true},
		{`SELECT regexp_split_to_table(s,p) AS s FROM cancel_regexp`, true},
	}
}

func TestReferenceRegexpActualCancellation(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE cancel_regexp(s TEXT,p TEXT,sentinel INT)`, 0))
		// Native eager sets use a bounded-cardinality scan. Each candidate
		// consumes up to 128 a's before the absent z, keeping actual matcher
		// work observable without filling the eager regex result budget.
		// PostgreSQL retains empty-pattern enumeration and streaming-prefix checks.
		pattern := ""
		if !b.PG {
			pattern = "a{128}z"
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO cancel_regexp VALUES($1,$2,7)`, 1, strings.Repeat("a", 2*1024*1024), pattern))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, prepared := range []bool{false, true} {
			for _, c := range regexpCancellationQueries() {
				t.Run(c.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					if b.PG {
						if c.cursor {
							regexpCancelPostgresRows(t, p, b, conn, prepared, c.q)
						} else {
							fixture.SessionCancelQuery(t, p.Ctx, conn, prepared, c.q, false)
						}
					} else {
						var stmt *sql.Stmt
						if prepared {
							stmt, err = conn.PrepareContext(p.Ctx, c.q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
						}
						operation := func(ctx context.Context) error {
							if c.cursor {
								var rows *sql.Rows
								var err error
								if prepared {
									rows, err = stmt.QueryContext(ctx)
								} else {
									rows, err = conn.QueryContext(ctx, c.q)
								}
								if rows != nil {
									defer rows.Close()
									return errors.New("canceled selected SRF exposed cursor")
								}
								return err
							}
							var result sql.Result
							var err error
							if prepared {
								result, err = stmt.ExecContext(ctx)
							} else {
								result, err = conn.ExecContext(ctx, c.q)
							}
							if result != nil {
								return errors.New("canceled selected UPDATE exposed result")
							}
							return err
						}
						likeReferenceCancelEntered(t, "sqltype.(*PGRegexProgram)", operation)
					}
					var sentinel int
					if err := conn.QueryRowContext(p.Ctx, `SELECT sentinel FROM cancel_regexp`).Scan(&sentinel); err != nil || sentinel != 7 {
						t.Fatal("canceled partial write", sentinel, err)
					}
					var n int
					if err := conn.QueryRowContext(p.Ctx, `SELECT regexp_count('aba','a')`).Scan(&n); err != nil || n != 2 {
						t.Fatal("shared owner recovery", n, err)
					}
				})
			}
		}
		result, err := conn.ExecContext(p.Ctx, `UPDATE cancel_regexp SET sentinel=8 WHERE regexp_like('a','a')`)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal(n, err)
		}
		var sentinel int
		if err := conn.QueryRowContext(p.Ctx, `SELECT sentinel FROM cancel_regexp`).Scan(&sentinel); err != nil || sentinel != 8 {
			t.Fatal(sentinel, err)
		}
	})
}

func TestReferenceRegexpStatementTimeout(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE cancel_regexp(s TEXT,p TEXT,sentinel INT)`, 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO cancel_regexp VALUES($1,'',7)`, 1, strings.Repeat("a", 2*1024*1024)))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, prepared := range []bool{false, true} {
			for _, c := range regexpCancellationQueries() {
				t.Run(c.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					var stmt *sql.Stmt
					if prepared {
						stmt, err = conn.PrepareContext(p.Ctx, c.q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					if _, err := conn.ExecContext(p.Ctx, `BEGIN`); err != nil {
						t.Fatal(err)
					}
					rolledBack := false
					defer func() {
						if !rolledBack {
							if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
								t.Error(err)
							}
						}
					}()
					if _, err := conn.ExecContext(p.Ctx, `SET LOCAL statement_timeout='10ms'`); err != nil {
						t.Fatal(err)
					}
					if c.cursor {
						regexpTimeoutRows(t, p, b, conn, stmt, c.q)
					} else {
						s := regexpRuntimeError(c.q, "57014", "canceling statement due to statement timeout", "")
						s.SelectRows = false
						if prepared {
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: c.q}, b.Recorder, s)
						} else {
							fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
						}
					}
					if _, err := conn.ExecContext(p.Ctx, `ROLLBACK`); err != nil {
						t.Fatal(err)
					}
					rolledBack = true
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select(`SELECT sentinel FROM cancel_regexp`, []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(7)}}))
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, regexpOne(b, `regexp_count('a','a')`, "INT4", int64(1)))
				})
			}
		}
	})
}

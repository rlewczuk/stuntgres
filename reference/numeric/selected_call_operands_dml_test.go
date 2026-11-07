package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func selectedCallResetUsers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	selectedCallRun(t, p, b, c, fixture.Exec("DELETE FROM users", 3))
	selectedCallRun(t, p, b, c, fixture.Exec("INSERT INTO users VALUES(1,1,'old-1'),(2,1,'old-2'),(3,2,'untouched')", 3))
}

func selectedCallCreateUsers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	selectedCallRun(t, p, b, c, fixture.Exec("CREATE TABLE users(id integer PRIMARY KEY,tenant_id integer,security_stamp text)", 0))
	selectedCallRun(t, p, b, c, fixture.Exec("INSERT INTO users VALUES(1,1,'old-1'),(2,1,'old-2'),(3,2,'untouched')", 3))
}

func TestReferenceSelectedCallOperandsDML(t *testing.T) {
	selectedCallScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		selectedCallCreateUsers(t, p, b, c)
		before := selectedCallStamps(t, p, b, c)
		selectedCallRun(t, p, b, c, fixture.Exec("UPDATE users SET security_stamp = md5(random()::text || id::text) WHERE tenant_id = 1", 2))
		selectedCallChangedStamps(t, before, selectedCallStamps(t, p, b, c), 1, 2)
		selectedCallResetUsers(t, p, b, c)
		q := "UPDATE users SET security_stamp = md5(random()::text || id::text) WHERE tenant_id = $1"
		selectedCallSQLMetadata(t, p, b, c, q, "{integer}")
		selectedCallHeld(t, p, b, c, q, func(e fixture.Executor) {
			// pq names wire definitions; in-process Go handles intentionally
			// do not publish a named pg_prepared_statements entry.
			var want [][]any
			if b.PG {
				want = [][]any{{"{integer}", true}}
			}
			selectedCallRun(t, p, b, c, fixture.Select("SELECT parameter_types::text,result_types IS NULL FROM pg_catalog.pg_prepared_statements WHERE statement=$1 AND NOT from_sql", []string{"parameter_types", "?column?"}, []string{"TEXT", "BOOL"}, want, q))
			for _, tc := range []struct {
				tenant  int64
				count   int64
				changed []int
			}{
				{1, 2, []int{1, 2}}, {2, 1, []int{3}}, {99, 0, nil}, {1, 2, []int{1, 2}},
			} {
				before := selectedCallStamps(t, p, b, c)
				selectedCallRun(t, p, b, e, fixture.Exec(q, tc.count, tc.tenant))
				selectedCallChangedStamps(t, before, selectedCallStamps(t, p, b, c), tc.changed...)
			}
		})
		selectedCallResetUsers(t, p, b, c)
		q = "UPDATE users SET security_stamp = md5(random()::text || id::text) WHERE tenant_id = $1 AND id != $2"
		selectedCallSQLMetadata(t, p, b, c, q, "{integer,integer}")
		before = selectedCallStamps(t, p, b, c)
		selectedCallRun(t, p, b, c, fixture.Exec(q, 1, int64(1), int64(1)))
		selectedCallChangedStamps(t, before, selectedCallStamps(t, p, b, c), 2)
		selectedCallResetUsers(t, p, b, c)
		selectedCallHeld(t, p, b, c, q, func(e fixture.Executor) {
			var want [][]any
			if b.PG {
				want = [][]any{{"{integer,integer}"}}
			}
			selectedCallRun(t, p, b, c, fixture.Select("SELECT parameter_types::text FROM pg_catalog.pg_prepared_statements WHERE statement=$1 AND NOT from_sql", []string{"parameter_types"}, []string{"TEXT"}, want, q))
			before := selectedCallStamps(t, p, b, c)
			selectedCallRun(t, p, b, e, fixture.Exec(q, 1, int64(1), int64(1)))
			selectedCallChangedStamps(t, before, selectedCallStamps(t, p, b, c), 2)
		})
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("migration-shaped/prepared=%v", prepared), func(t *testing.T) {
				selectedCallResetUsers(t, p, b, c)
				before := selectedCallStamps(t, p, b, c)
				s := fixture.Exec("UPDATE users SET security_stamp=md5(random()::text || id::text || tenant_id::text)", 3)
				s.Prepared = prepared
				selectedCallRun(t, p, b, c, s)
				selectedCallChangedStamps(t, before, selectedCallStamps(t, p, b, c), 1, 2, 3)
			})
		}
		selectedCallDMLVolatility(t, p, b, c)
		selectedCallDMLQueries(t, p, b, c)
		selectedCallDMLWrites(t, p, b, c)
		selectedCallDMLAtomicity(t, p, b, c)
	})
}

// SQL PREPARE publishes the same named definition on both backends, exposing
// SQL-inferred parameter/result metadata independently of Go handle naming.
func selectedCallSQLMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query, types string) {
	t.Helper()
	selectedCallRun(t, p, b, c, fixture.Exec("PREPARE selected_call_update_metadata AS "+query, 0))
	selectedCallRun(t, p, b, c, fixture.Select("SELECT parameter_types::text,result_types IS NULL,from_sql FROM pg_catalog.pg_prepared_statements WHERE name='selected_call_update_metadata'", []string{"parameter_types", "?column?", "from_sql"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{types, true, true}}))
	selectedCallRun(t, p, b, c, fixture.Exec("DEALLOCATE selected_call_update_metadata", 0))
}

func selectedCallDMLQueries(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	selectedCallRun(t, p, b, c, fixture.Exec("CREATE TABLE selected_x(x integer)", 0))
	selectedCallRun(t, p, b, c, fixture.Exec("INSERT INTO selected_x VALUES(-2),(-1),(0),(1),(NULL)", 5))
	for _, s := range []fixture.Step{
		fixture.Select("SELECT x,abs(x)*2 FROM selected_x ORDER BY x NULLS LAST", []string{"x", "?column?"}, []string{"INT4", "INT4"}, [][]any{{int64(-2), int64(4)}, {int64(-1), int64(2)}, {int64(0), int64(0)}, {int64(1), int64(2)}, {nil, nil}}),
		fixture.Select("SELECT x FROM selected_x WHERE abs(x)+0=1 ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(1)}}),
		fixture.Select("SELECT x FROM selected_x ORDER BY abs(x),x NULLS LAST", []string{"x"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(-1)}, {int64(1)}, {int64(-2)}, {nil}}),
		fixture.Select("SELECT x,NOT abs(x)+0=1,abs(x)+0=1 AND TRUE,abs(x)+0=1 OR FALSE FROM selected_x ORDER BY x NULLS LAST", []string{"x", "?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(-2), true, false, false}, {int64(-1), false, true, true}, {int64(0), true, false, false}, {int64(1), false, true, true}, {nil, nil, nil, nil}}),
	} {
		selectedCallBoth(t, p, b, c, s)
	}
	q := "SELECT abs($1::int4)+$2::int4 AS magnitude"
	selectedCallHeld(t, p, b, c, q, func(e fixture.Executor) {
		for _, tc := range []struct{ a, b, want any }{{int64(-3), int64(2), int64(5)}, {int64(4), int64(-1), int64(3)}, {nil, int64(2), nil}, {int64(-1), nil, nil}, {int64(-2), int64(1), int64(3)}} {
			s := fixture.Select(q, []string{"magnitude"}, []string{"INT4"}, [][]any{{tc.want}}, tc.a, tc.b)
			selectedCallRun(t, p, b, c, s)
			selectedCallRun(t, p, b, e, s)
		}
	})
}

func selectedCallDMLWrites(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	selectedCallRun(t, p, b, c, fixture.Exec("CREATE TABLE selected_values(v integer,s text)", 0))
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("values/prepared=%v", prepared), func(t *testing.T) {
			run := func(s fixture.Step) { s.Prepared = prepared; selectedCallRun(t, p, b, c, s) }
			run(fixture.Exec("INSERT INTO selected_values(v,s) VALUES(abs(-1)*2,md5(random()::text)),(1+abs(-1),abs(-1)::text)", 2))
			rows := randomRead(t, p.Ctx, c, b, "SELECT v,s FROM selected_values", []string{"v", "s"}, []string{"INT4", "TEXT"}, nil)
			selectedCallCheckInserted(t, rows)
			run(fixture.Exec("DELETE FROM selected_values", 2))
			q := "INSERT INTO selected_values(v,s) VALUES(abs(-1)*2,md5(random()::text)),(1+abs(-1),abs(-1)::text) RETURNING v,s"
			if prepared {
				selectedCallHeld(t, p, b, c, q, func(e fixture.Executor) {
					selectedCallCheckInserted(t, randomRead(t, p.Ctx, e, b, q, []string{"v", "s"}, []string{"INT4", "TEXT"}, nil))
				})
			} else {
				selectedCallCheckInserted(t, randomRead(t, p.Ctx, c, b, q, []string{"v", "s"}, []string{"INT4", "TEXT"}, nil))
			}
			s := fixture.Select("UPDATE selected_values SET v=1+abs(v) RETURNING v,abs(v)*2,abs(v)::text", []string{"v", "?column?", "abs"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(3), int64(6), "3"}, {int64(3), int64(6), "3"}})
			s.Bag = true
			run(s)
			run(fixture.Exec("UPDATE selected_values SET v=1+abs(v)", 2))
			run(fixture.Select("SELECT v FROM selected_values ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(4)}}))
			run(fixture.Exec("DELETE FROM selected_values", 2))
			run(fixture.Exec("INSERT INTO selected_values(v) VALUES(abs(-1))", 1))
			run(fixture.Select("SELECT v FROM selected_values", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Exec("DELETE FROM selected_values", 1))
			run(fixture.Select("INSERT INTO selected_values(v) VALUES(1+abs(-1)) RETURNING abs(v)*2,abs(v)::text", []string{"?column?", "abs"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "2"}}))
			run(fixture.Exec("DELETE FROM selected_values", 1))
		})
	}
}

func selectedCallCheckInserted(t *testing.T, rows [][]any) {
	t.Helper()
	if len(rows) != 2 {
		t.Fatalf("INSERT row count: %#v", rows)
	}
	hashes, ones := 0, 0
	for _, row := range rows {
		if row[0] != int64(2) {
			t.Fatalf("INSERT v: %#v", rows)
		}
		s, ok := row[1].(string)
		if !ok {
			t.Fatalf("INSERT s: %#v", rows)
		}
		if s == "1" {
			ones++
		} else if selectedCallStampPattern.MatchString(s) {
			hashes++
		} else {
			t.Fatalf("INSERT s: %q", s)
		}
	}
	if hashes != 1 || ones != 1 {
		t.Fatalf("INSERT bag: %#v", rows)
	}
}

func selectedCallDMLAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	selectedCallRun(t, p, b, c, fixture.Exec("CREATE TABLE selected_atomic(v integer)", 0))
	selectedCallRun(t, p, b, c, fixture.Exec("INSERT INTO selected_atomic VALUES(7),(8)", 2))
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{
			"UPDATE selected_atomic SET v=1+mod(1,0)",
			"INSERT INTO selected_atomic VALUES(abs(-1)),(1+mod(1,0))",
			"UPDATE selected_atomic SET v=1+abs(v) RETURNING 1+mod(v-v,0)",
		} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if q == "UPDATE selected_atomic SET v=1+abs(v) RETURNING 1+mod(v-v,0)" {
				selectedCallRuntimeError(t, p, b, c, q, *s.Diagnostic, prepared)
			} else {
				selectedCallRun(t, p, b, c, s)
			}
			selectedCallRun(t, p, b, c, fixture.Select("SELECT v FROM selected_atomic ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
		}
	}
}

func selectedCallDMLVolatility(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"SELECT id,random()+0::float8 AS draw FROM users ORDER BY id",
		"SELECT id,CAST(random() AS float8) AS draw FROM users ORDER BY id",
	} {
		seen := make(map[float64]bool)
		read := func(e fixture.Executor) {
			rows := randomRead(t, p.Ctx, e, b, q, []string{"id", "draw"}, []string{"INT4", "FLOAT8"}, nil)
			if len(rows) != 3 {
				t.Fatalf("volatile projection row count: %#v", rows)
			}
			for i, row := range rows {
				if row[0] != int64(i+1) {
					t.Fatalf("volatile projection identities: %#v", rows)
				}
				selectedCallRange(t, row[1], 1)
				value := row[1].(float64)
				if seen[value] {
					t.Fatalf("nested random draw reused across rows/executions: %.17g", value)
				}
				seen[value] = true
			}
		}
		read(c)
		selectedCallHeld(t, p, b, c, q, func(e fixture.Executor) { read(e); read(e) })
	}
}

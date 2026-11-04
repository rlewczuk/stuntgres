package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func autoExplainTableMetadata(t *testing.T, b fixture.Backend, rows *sql.Rows) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 {
		t.Fatalf("id/v descriptors: %v: %v", types, err)
	}
	for _, column := range types {
		nullable, known := column.Nullable()
		// Both stored columns are declared nullable. pq deliberately exposes
		// unknown nullability, rather than inventing catalog knowledge.
		if b.PG {
			if nullable || known {
				t.Fatalf("pq %s nullability=%t/%t want false/false", column.Name(), nullable, known)
			}
		} else if !nullable || !known {
			t.Fatalf("native %s nullability=%t/%t want true/true", column.Name(), nullable, known)
		}
	}
}

func TestReferenceAutoExplainExecution(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE ae_data(id integer, v text)", 0))
		run(fixture.Exec("INSERT INTO ae_data VALUES (1,'one'),(2,NULL),(3,'three')", 3))
		autoExplainConfigure(t, p, b, c, notices)
		for _, test := range []struct {
			query  string
			values [][]any
			actual float64
		}{
			{"SELECT id,v FROM ae_data WHERE id<0 ORDER BY id", nil, 0},
			{"SELECT id,v FROM ae_data WHERE id=2 ORDER BY id", [][]any{{int64(2), nil}}, 1},
			{"SELECT id,v FROM ae_data ORDER BY id", [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}, 3},
		} {
			s := fixture.Select(test.query, []string{"id", "v"}, []string{"INT4", "TEXT"}, test.values)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { autoExplainTableMetadata(t, b, rows) }
			run(s)
			autoExplainJSON(t, notices, test.query, nil, true, test.actual)
		}
		const query = "SELECT id,v FROM ae_data WHERE id=$1 ORDER BY id"
		held, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		autoExplainQuiet(t, notices)
		for _, test := range []struct {
			arg        int64
			parameters string
			values     [][]any
		}{
			{1, "$1 = '1'", [][]any{{int64(1), "one"}}},
			{2, "$1 = '2'", [][]any{{int64(2), nil}}},
			{99, "$1 = '99'", nil},
			{1, "$1 = '1'", [][]any{{int64(1), "one"}}},
		} {
			s := fixture.Select(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, test.values, test.arg)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { autoExplainTableMetadata(t, b, rows) }
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, s)
			autoExplainJSON(t, notices, query, new(test.parameters), true, float64(len(test.values)))
		}
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
		autoExplainQuiet(t, notices)
		run(fixture.Exec("SET auto_explain.log_analyze = off", 0))
		const ordinary = "SELECT id,v FROM ae_data ORDER BY id"
		run(fixture.Select(ordinary, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}))
		autoExplainJSON(t, notices, ordinary, nil, false, 0)
	})
}

func TestReferenceAutoExplainParameters(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		autoExplainConfigure(t, p, b, c, notices)
		const query = "SELECT $1::text AS v"
		held, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		autoExplainQuiet(t, notices)
		for _, test := range []struct {
			limit      string
			arg        any
			parameters *string
		}{
			{"-1", "a'b", new("$1 = 'a''b'")},
			{"-1", "", new("$1 = ''")},
			{"-1", nil, new("$1 = NULL")},
			{"1", "éx", new("$1 = '...'")},
			{"2", "éx", new("$1 = 'é...'")},
			{"3", "éx", new("$1 = 'éx'")},
			{"2", "a'b", new("$1 = 'a''...'")},
			{"2", nil, new("$1 = NULL")},
			{"0", "éx", nil},
			{"0", nil, nil},
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET auto_explain.log_parameter_max_length = "+test.limit, 0))
			s := fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{test.arg}}, test.arg)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, s)
			autoExplainJSON(t, notices, query, test.parameters, true, 1)
		}
		// The direct parameterized route must use the same typed-value policy.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET auto_explain.log_parameter_max_length = 2", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"éx"}}, "éx"))
		autoExplainJSON(t, notices, query, new("$1 = 'é...'"), true, 1)
	})
}

func TestReferenceAutoExplainSampling(t *testing.T) {
	autoExplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		autoExplainConfigure(t, p, b, c, notices)
		const query = "SELECT 11::integer AS v"
		for _, test := range []struct {
			duration, rate string
			logged         bool
		}{
			{"-1", "1", false},
			{"0", "0", false},
			{"2147483647", "1", false},
			{"0", "1", true},
		} {
			run(fixture.Exec("SET auto_explain.log_min_duration = "+test.duration, 0))
			run(fixture.Exec("SET auto_explain.sample_rate = "+test.rate, 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}})
				s.Prepared = prepared
				run(s)
				if test.logged {
					autoExplainJSON(t, notices, query, nil, true, 1)
				} else {
					autoExplainQuiet(t, notices)
				}
			}
		}
	})
}

package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGroupingSetsReviewProducerChildren(t *testing.T) {
	// These character positions and complete diagnostics passed pinned PostgreSQL18.0.
	for _, tc := range []struct{ clause, token string }{
		{"ROLLUP(())", "))"},
		{"CUBE(())", "))"},
		{"ROLLUP(a,())", "))"},
		{"CUBE(a,())", "))"},
		{"ROLLUP(GROUPING SETS((a),()))", "SETS"},
		{"CUBE(GROUPING SETS((a),()))", "SETS"},
	} {
		t.Run(tc.clause, func(t *testing.T) {
			fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				fixture.GroupingSetsFixture(run)
				token := tc.token
				messageToken := token
				if token == "))" {
					messageToken = ")"
				}
				q := "/*é界*/ SELECT count(*) FROM gs GROUP BY " + tc.clause
				fixture.GroupingSetsAnalysisError(run, fixture.GroupingSetsError(q, "42601", "syntax error at or near \""+messageToken+"\"", token))
				prefix := "INSERT INTO gs VALUES(9,9,9); " + q
				fixture.GroupingSetsAnalysisError(run, fixture.GroupingSetsError(prefix, "42601", "syntax error at or near \""+messageToken+"\"", token))
				run(fixture.GroupingSetsBag("SELECT a,b,v FROM gs", []string{"a", "b", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(2)}, {int64(1), int64(20), int64(3)}, {nil, int64(10), int64(5)}}))
			})
		})
	}
}

func TestReferenceGroupingSetsReviewProducerFunctions(t *testing.T) {
	// In these expression positions the nested unreserved name denotes an
	// ordinary function, not a grouping producer. PostgreSQL owns its42883;
	// native retains the existing generic-call capability boundary, not42601.
	for _, tc := range []struct{ clause, function string }{
		{"ROLLUP(cube(a))", "cube"},
		{"CUBE(rollup(a))", "rollup"},
	} {
		t.Run(tc.clause, func(t *testing.T) {
			fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				fixture.GroupingSetsFixture(run)
				q := "SELECT count(*) FROM gs GROUP BY " + tc.clause
				s := fixture.GroupingSetsError(q, "42883", "function "+tc.function+"(integer) does not exist", tc.function+"(a)")
				s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				if !b.PG {
					s = fixture.GroupingSetsError(q, "0A000", "functions are unsupported", tc.function+"(a)")
				}
				fixture.GroupingSetsAnalysisError(run, s)
			})
		})
	}
}

func groupingSetsReviewParameterMetadata(b fixture.Backend) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != 3 {
			t.Fatal(meta, err)
		}
		for i, m := range meta {
			wantScan := reflect.TypeFor[int64]()
			if b.PG && i < 2 {
				wantScan = reflect.TypeFor[int32]()
			}
			if m.ScanType() != wantScan {
				t.Fatal(m.Name(), m.ScanType(), wantScan)
			}
			wantNullable, wantKnown := i == 0, true
			if b.PG {
				wantNullable, wantKnown = false, false
			}
			if nullable, known := m.Nullable(); nullable != wantNullable || known != wantKnown {
				t.Fatal(m.Name(), nullable, known, wantNullable, wantKnown)
			}
			if length, known := m.Length(); length != 0 || known {
				t.Fatal(m.Name(), length, known)
			}
			if precision, scale, known := m.DecimalSize(); precision != 0 || scale != 0 || known {
				t.Fatal(m.Name(), precision, scale, known)
			}
		}
	}
}

func TestReferenceGroupingSetsReviewParameters(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		fixture.GroupingSetsFixture(run)
		for _, tc := range []struct {
			query    string
			values   []any
			expected [][][]any
		}{
			{
				query:  "SELECT $1::INT AS k,GROUPING($1::INT),count(*) FROM gs GROUP BY GROUPING SETS(($1::INT),($1::INT),())",
				values: []any{int64(42), nil, int64(7), nil, int64(42)},
				expected: [][][]any{
					{{int64(42), int64(0), int64(3)}, {int64(42), int64(0), int64(3)}, {nil, int64(1), int64(3)}},
					{{nil, int64(0), int64(3)}, {nil, int64(0), int64(3)}, {nil, int64(1), int64(3)}},
					{{int64(7), int64(0), int64(3)}, {int64(7), int64(0), int64(3)}, {nil, int64(1), int64(3)}},
					{{nil, int64(0), int64(3)}, {nil, int64(0), int64(3)}, {nil, int64(1), int64(3)}},
					{{int64(42), int64(0), int64(3)}, {int64(42), int64(0), int64(3)}, {nil, int64(1), int64(3)}},
				},
			},
			{
				query:  "SELECT a+$1::INT AS k,GROUPING(a+$1::INT),count(*) FROM gs GROUP BY ROLLUP(a+$1::INT)",
				values: []any{int64(10), nil, int64(-1), nil, int64(10)},
				expected: [][][]any{
					{{int64(11), int64(0), int64(2)}, {nil, int64(0), int64(1)}, {nil, int64(1), int64(3)}},
					{{nil, int64(0), int64(3)}, {nil, int64(1), int64(3)}},
					{{int64(0), int64(0), int64(2)}, {nil, int64(0), int64(1)}, {nil, int64(1), int64(3)}},
					{{nil, int64(0), int64(3)}, {nil, int64(1), int64(3)}},
					{{int64(11), int64(0), int64(2)}, {nil, int64(0), int64(1)}, {nil, int64(1), int64(3)}},
				},
			},
		} {
			stmt, err := c.PrepareContext(p.Ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}
			for i, value := range tc.values {
				s := fixture.GroupingSetsBag(tc.query, []string{"k", "grouping", "count"}, []string{"INT4", "INT4", "INT8"}, tc.expected[i])
				s.Args = []any{value}
				s.CheckMetadata = groupingSetsReviewParameterMetadata(b)
				fixture.GroupingSetsBoth(run, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Select("SELECT count(*) FROM gs", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
	})
}

func TestReferenceGroupingSetsReviewParameterIdentity(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		fixture.GroupingSetsFixture(run)
		q := "SELECT $1::INT AS k,$2::INT AS j,GROUPING($1::INT,$2::INT),count(*) FROM gs GROUP BY GROUPING SETS(($1::INT),($2::INT),())"
		s := fixture.GroupingSetsBag(q, []string{"k", "j", "grouping", "count"}, []string{"INT4", "INT4", "INT4", "INT8"}, [][]any{{int64(7), nil, int64(1), int64(3)}, {nil, int64(7), int64(2), int64(3)}, {nil, nil, int64(3), int64(3)}})
		s.Args = []any{int64(7), int64(7)}
		fixture.GroupingSetsBoth(run, s)
		q = "/*é*/ SELECT GROUPING($2::INT) FROM gs GROUP BY $1::INT"
		mismatch := fixture.GroupingSetsError(q, "42803", "arguments to GROUPING must be grouping expressions of the associated query level", "$2")
		mismatch.Args = []any{int64(7), int64(7)}
		fixture.GroupingSetsAnalysisError(run, mismatch)
		run(fixture.Select("SELECT count(*) FROM gs", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
	})
}

func TestReferenceGroupingSetsReviewParameterInsert(t *testing.T) {
	fixture.GroupingSetsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		fixture.GroupingSetsFixture(run)
		run(fixture.Exec("CREATE TABLE gs_param_out(k INT,g INT,n BIGINT)", 0))
		q := "INSERT INTO gs_param_out SELECT $1::INT,GROUPING($1::INT),count(*) FROM gs GROUP BY GROUPING SETS(($1::INT),($1::INT),())"
		fixture.GroupingSetsBoth(run, fixture.Exec(q, 3, int64(17)))
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 3, nil))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 3, int64(7)))
		var expected [][]any
		for _, value := range []any{int64(17), int64(17), nil, int64(7)} {
			expected = append(expected, []any{value, int64(0), int64(3)}, []any{value, int64(0), int64(3)}, []any{nil, int64(1), int64(3)})
		}
		fixture.GroupingSetsBoth(run, fixture.GroupingSetsBag("SELECT k,g,n FROM gs_param_out", []string{"k", "g", "n"}, []string{"INT4", "INT4", "INT8"}, expected))
		run(fixture.Exec("CREATE TABLE gs_param_nn(k INT NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO gs_param_nn VALUES(99)", 1))
		q = "INSERT INTO gs_param_nn SELECT $1::INT FROM gs GROUP BY ROLLUP($1::INT) HAVING GROUPING($1::INT)<=$2::INT"
		failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "23502", Message: "null value in column \"k\" of relation \"gs_param_nn\" violates not-null constraint", Detail: "Failing row contains (null).", Schema: "public", Table: "gs_param_nn", Column: "k"}, int64(17), int64(1))
		failure.ErrorStage = "execute"
		fixture.GroupingSetsBoth(run, failure)
		run(fixture.Select("SELECT k FROM gs_param_nn", []string{"k"}, []string{"INT4"}, [][]any{{int64(99)}}))
		atomic, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer atomic.Close()
		sameOwner := fixture.HeldStatement{Executor: c, Statement: atomic, SQL: q}
		fixture.RunStep(t, p.Ctx, sameOwner, b.Recorder, failure)
		run(fixture.Select("SELECT k FROM gs_param_nn", []string{"k"}, []string{"INT4"}, [][]any{{int64(99)}}))
		fixture.RunStep(t, p.Ctx, sameOwner, b.Recorder, fixture.Exec(q, 1, int64(17), int64(0)))
		run(fixture.GroupingSetsBag("SELECT k FROM gs_param_nn", []string{"k"}, []string{"INT4"}, [][]any{{int64(99)}, {int64(17)}}))
	})
}

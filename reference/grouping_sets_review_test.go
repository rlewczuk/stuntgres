package reference_test

import (
	"database/sql"
	"reflect"
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
			groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
				groupingSetsFixture(run)
				token := tc.token
				messageToken := token
				if token == "))" {
					messageToken = ")"
				}
				q := "/*é界*/ SELECT count(*) FROM gs GROUP BY " + tc.clause
				groupingSetsAnalysisError(run, groupingSetsError(q, "42601", "syntax error at or near \""+messageToken+"\"", token))
				prefix := "INSERT INTO gs VALUES(9,9,9); " + q
				groupingSetsAnalysisError(run, groupingSetsError(prefix, "42601", "syntax error at or near \""+messageToken+"\"", token))
				run(groupingSetsBag("SELECT a,b,v FROM gs", []string{"a", "b", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(2)}, {int64(1), int64(20), int64(3)}, {nil, int64(10), int64(5)}}))
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
			groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
				groupingSetsFixture(run)
				q := "SELECT count(*) FROM gs GROUP BY " + tc.clause
				s := groupingSetsError(q, "42883", "function "+tc.function+"(integer) does not exist", tc.function+"(a)")
				s.diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				if !b.pg {
					s = groupingSetsError(q, "0A000", "functions are unsupported", tc.function+"(a)")
				}
				groupingSetsAnalysisError(run, s)
			})
		})
	}
}

func groupingSetsReviewParameterMetadata(b referenceBackend) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != 3 {
			t.Fatal(meta, err)
		}
		for i, m := range meta {
			wantScan := reflect.TypeFor[int64]()
			if b.pg && i < 2 {
				wantScan = reflect.TypeFor[int32]()
			}
			if m.ScanType() != wantScan {
				t.Fatal(m.Name(), m.ScanType(), wantScan)
			}
			wantNullable, wantKnown := i == 0, true
			if b.pg {
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
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		groupingSetsFixture(run)
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
			stmt, err := c.PrepareContext(p.ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: tc.query}
			for i, value := range tc.values {
				s := groupingSetsBag(tc.query, []string{"k", "grouping", "count"}, []string{"INT4", "INT4", "INT8"}, tc.expected[i])
				s.args = []any{value}
				s.checkMetadata = groupingSetsReviewParameterMetadata(b)
				groupingSetsBoth(run, s)
				referenceRunStep(t, p.ctx, held, b.recorder, s)
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		run(referenceSelect("SELECT count(*) FROM gs", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
	})
}

func TestReferenceGroupingSetsReviewParameterIdentity(t *testing.T) {
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		groupingSetsFixture(run)
		q := "SELECT $1::INT AS k,$2::INT AS j,GROUPING($1::INT,$2::INT),count(*) FROM gs GROUP BY GROUPING SETS(($1::INT),($2::INT),())"
		s := groupingSetsBag(q, []string{"k", "j", "grouping", "count"}, []string{"INT4", "INT4", "INT4", "INT8"}, [][]any{{int64(7), nil, int64(1), int64(3)}, {nil, int64(7), int64(2), int64(3)}, {nil, nil, int64(3), int64(3)}})
		s.args = []any{int64(7), int64(7)}
		groupingSetsBoth(run, s)
		q = "/*é*/ SELECT GROUPING($2::INT) FROM gs GROUP BY $1::INT"
		mismatch := groupingSetsError(q, "42803", "arguments to GROUPING must be grouping expressions of the associated query level", "$2")
		mismatch.args = []any{int64(7), int64(7)}
		groupingSetsAnalysisError(run, mismatch)
		run(referenceSelect("SELECT count(*) FROM gs", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
	})
}

func TestReferenceGroupingSetsReviewParameterInsert(t *testing.T) {
	groupingSetsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		groupingSetsFixture(run)
		run(referenceExec("CREATE TABLE gs_param_out(k INT,g INT,n BIGINT)", 0))
		q := "INSERT INTO gs_param_out SELECT $1::INT,GROUPING($1::INT),count(*) FROM gs GROUP BY GROUPING SETS(($1::INT),($1::INT),())"
		groupingSetsBoth(run, referenceExec(q, 3, int64(17)))
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 3, nil))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 3, int64(7)))
		var expected [][]any
		for _, value := range []any{int64(17), int64(17), nil, int64(7)} {
			expected = append(expected, []any{value, int64(0), int64(3)}, []any{value, int64(0), int64(3)}, []any{nil, int64(1), int64(3)})
		}
		groupingSetsBoth(run, groupingSetsBag("SELECT k,g,n FROM gs_param_out", []string{"k", "g", "n"}, []string{"INT4", "INT4", "INT8"}, expected))
		run(referenceExec("CREATE TABLE gs_param_nn(k INT NOT NULL)", 0))
		run(referenceExec("INSERT INTO gs_param_nn VALUES(99)", 1))
		q = "INSERT INTO gs_param_nn SELECT $1::INT FROM gs GROUP BY ROLLUP($1::INT) HAVING GROUPING($1::INT)<=$2::INT"
		failure := referenceImplicitError(q, referenceDiagnostic{Code: "23502", Message: "null value in column \"k\" of relation \"gs_param_nn\" violates not-null constraint", Detail: "Failing row contains (null).", Schema: "public", Table: "gs_param_nn", Column: "k"}, int64(17), int64(1))
		failure.errorStage = "execute"
		groupingSetsBoth(run, failure)
		run(referenceSelect("SELECT k FROM gs_param_nn", []string{"k"}, []string{"INT4"}, [][]any{{int64(99)}}))
		atomic, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer atomic.Close()
		sameOwner := referenceHeldStatement{referenceExecutor: c, statement: atomic, query: q}
		referenceRunStep(t, p.ctx, sameOwner, b.recorder, failure)
		run(referenceSelect("SELECT k FROM gs_param_nn", []string{"k"}, []string{"INT4"}, [][]any{{int64(99)}}))
		referenceRunStep(t, p.ctx, sameOwner, b.recorder, referenceExec(q, 1, int64(17), int64(0)))
		run(groupingSetsBag("SELECT k FROM gs_param_nn", []string{"k"}, []string{"INT4"}, [][]any{{int64(99)}, {int64(17)}}))
	})
}

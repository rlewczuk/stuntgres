package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func querySetOpsScenario(t *testing.T, exercise func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		exercise(p, backend)
	}
}

func querySetOpsSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE sl(v INT)", 0),
		fixture.Exec("CREATE TABLE sr(v INT)", 0),
		fixture.Exec("INSERT INTO sl VALUES(1),(1),(1),(2),(NULL),(NULL)", 6),
		fixture.Exec("INSERT INTO sr VALUES(1),(1),(3),(NULL)", 4),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func querySetOpsRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, nonContext := range []bool{false, true} {
			s.Prepared, s.NonContext = prepared, nonContext
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	}
}

func querySetOpsDiagnosticRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step, preparedStage string) {
	t.Helper()
	for _, nonContext := range []bool{false, true} {
		s.NonContext, s.Prepared, s.ErrorStage = nonContext, false, "execute"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		s.Prepared, s.ErrorStage = true, preparedStage
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func querySetOpsBag(query, name, typ string, rows [][]any) fixture.Step {
	s := fixture.Select(query, []string{name}, []string{typ}, rows)
	s.Bag = true
	return s
}

func TestReferenceQuerySetOpsBags(t *testing.T) {
	querySetOpsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		querySetOpsSeed(t, p, b)
		// Explicit independent occurrence lists; no append-order promise.
		for _, tc := range []struct {
			op   string
			rows [][]any
		}{
			{"UNION", [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {nil}}},
			{"UNION DISTINCT", [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {nil}}},
			{"UNION ALL", [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(3)}, {nil}, {nil}, {nil}}},
			{"INTERSECT", [][]any{{int64(1)}, {nil}}},
			{"INTERSECT DISTINCT", [][]any{{int64(1)}, {nil}}},
			{"INTERSECT ALL", [][]any{{int64(1)}, {int64(1)}, {nil}}},
			{"EXCEPT", [][]any{{int64(2)}}},
			{"EXCEPT DISTINCT", [][]any{{int64(2)}}},
			{"EXCEPT ALL", [][]any{{int64(1)}, {int64(2)}, {nil}}},
		} {
			querySetOpsRoutes(t, p, b, querySetOpsBag("SELECT v FROM sl "+tc.op+" SELECT v FROM sr", "v", "INT4", tc.rows))
			querySetOpsRoutes(t, p, b, querySetOpsBag("SELECT v FROM sl WHERE FALSE "+tc.op+" SELECT v FROM sr WHERE FALSE", "v", "INT4", [][]any{}))
		}
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT v FROM sl WHERE FALSE UNION SELECT v FROM sr", [][]any{{int64(1)}, {int64(3)}, {nil}}},
			{"SELECT v FROM sl UNION ALL SELECT v FROM sr WHERE FALSE", [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {nil}, {nil}}},
			{"SELECT v FROM sl WHERE FALSE INTERSECT ALL SELECT v FROM sr", [][]any{}},
			{"SELECT v FROM sl INTERSECT SELECT v FROM sr WHERE FALSE", [][]any{}},
			{"SELECT v FROM sl WHERE FALSE EXCEPT ALL SELECT v FROM sr", [][]any{}},
			{"SELECT v FROM sl EXCEPT SELECT v FROM sr WHERE FALSE", [][]any{{int64(1)}, {int64(2)}, {nil}}},
			{"SELECT v FROM sl EXCEPT ALL SELECT v FROM sr WHERE FALSE", [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {nil}, {nil}}},
		} {
			querySetOpsRoutes(t, p, b, querySetOpsBag(tc.q, "v", "INT4", tc.rows))
		}
		for _, tc := range []struct {
			op   string
			rows [][]any
		}{
			{"INTERSECT ALL", [][]any{{int64(1), nil}, {nil, nil}, {nil, nil}}},
			{"EXCEPT ALL", [][]any{{int64(1), nil}, {nil, int64(1)}}},
			{"UNION", [][]any{{int64(1), nil}, {nil, int64(1)}, {nil, nil}}},
			{"UNION ALL", [][]any{{int64(1), nil}, {int64(1), nil}, {nil, int64(1)}, {nil, nil}, {nil, nil}, {int64(1), nil}, {nil, nil}, {nil, nil}, {nil, nil}}},
		} {
			q := "VALUES (1,NULL),(1,NULL),(NULL,1),(NULL,NULL),(NULL,NULL) " + tc.op + " VALUES (1,NULL::INT),(NULL,NULL),(NULL,NULL),(NULL,NULL)"
			s := fixture.Select(q, []string{"column1", "column2"}, []string{"INT4", "INT4"}, tc.rows)
			s.Bag = true
			querySetOpsRoutes(t, p, b, s)
		}
	})
}

func TestReferenceQuerySetOpsPrecedenceValues(t *testing.T) {
	querySetOpsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		querySetOpsSeed(t, p, b)
		for _, tc := range []struct {
			q, name string
			rows    [][]any
		}{
			{"SELECT 1 UNION SELECT 2 INTERSECT SELECT 2", "?column?", [][]any{{int64(1)}, {int64(2)}}},
			{"(SELECT 1 UNION SELECT 2) INTERSECT SELECT 2", "?column?", [][]any{{int64(2)}}},
			{"SELECT 1 UNION SELECT 2 EXCEPT SELECT 1", "?column?", [][]any{{int64(2)}}},
			{"SELECT 1 EXCEPT SELECT 1 UNION SELECT 2", "?column?", [][]any{{int64(2)}}},
			{"SELECT 1 UNION (SELECT 2 EXCEPT SELECT 1)", "?column?", [][]any{{int64(1)}, {int64(2)}}},
			{"(VALUES(1),(1) UNION ALL VALUES(1)) EXCEPT ALL VALUES(1),(1)", "column1", [][]any{{int64(1)}}},
			{"VALUES(1),(1),(NULL) INTERSECT ALL VALUES(1),(NULL),(NULL)", "column1", [][]any{{int64(1)}, {nil}}},
			{"VALUES(1) UNION ALL SELECT 2", "column1", [][]any{{int64(1)}, {int64(2)}}},
			{"SELECT 1 AS x UNION ALL VALUES(2)", "x", [][]any{{int64(1)}, {int64(2)}}},
		} {
			querySetOpsRoutes(t, p, b, querySetOpsBag(tc.q, tc.name, "INT4", tc.rows))
		}
		q := "(SELECT v FROM sl ORDER BY v NULLS LAST LIMIT 2) UNION ALL (SELECT v FROM sr ORDER BY v DESC NULLS LAST LIMIT 1) ORDER BY 1 LIMIT 2 OFFSET 1"
		querySetOpsRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
		querySetOpsRoutes(t, p, b, fixture.Select("SELECT v FROM sl UNION ALL SELECT v FROM sr ORDER BY 1 NULLS LAST LIMIT 2 OFFSET 5", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
	})
}

func TestReferenceQuerySetOpsCommonTypes(t *testing.T) {
	querySetOpsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			q, typ string
			rows   [][]any
		}{
			{"SELECT NULL AS x UNION SELECT 1", "INT4", [][]any{{nil}, {int64(1)}}},
			{"SELECT NULL AS x UNION SELECT NULL", "TEXT", [][]any{{nil}}},
			{"SELECT NULL AS x UNION (SELECT NULL UNION SELECT 1)", "INT4", [][]any{{nil}, {int64(1)}}},
			{"SELECT 1 AS x UNION SELECT '2'", "INT4", [][]any{{int64(1)}, {int64(2)}}},
			{"SELECT 1::SMALLINT AS x UNION ALL SELECT 2::BIGINT", "INT8", [][]any{{int64(1)}, {int64(2)}}},
			{"SELECT 'a'::TEXT AS x UNION ALL SELECT 'b'::VARCHAR", "TEXT", [][]any{{"a"}, {"b"}}},
			{"SELECT 1 AS x UNION SELECT '2.5'::REAL", "FLOAT4", [][]any{{float64(1)}, {float64(2.5)}}},
			{"SELECT 1 AS x UNION SELECT '2.2'::REAL", "FLOAT4", [][]any{{float64(1)}, {float64(2.2)}}},
			{"SELECT '{}'::JSON AS x UNION ALL SELECT '{}'::JSON", "JSON", [][]any{{"{}"}, {"{}"}}},
			{"SELECT '{1,NULL}'::INT[] AS x UNION SELECT '{1,NULL}'::INT[]", "_INT4", [][]any{{"{1,NULL}"}}},
		} {
			if !b.PG && tc.q == "SELECT 1 AS x UNION SELECT '2.2'::REAL" {
				tc.rows = [][]any{{float64(1)}, {float64(float32(2.2))}}
			}
			querySetOpsRoutes(t, p, b, querySetOpsBag(tc.q, "x", tc.typ, tc.rows))
		}
	})
}

func TestReferenceQuerySetOpsMetadataParameters(t *testing.T) {
	querySetOpsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, empty := range []bool{false, true} {
			q := "SELECT 1 AS left_label UNION ALL SELECT 2::BIGINT AS right_label"
			want := [][]any{{int64(1)}, {int64(2)}}
			if empty {
				q = "SELECT 1 AS left_label WHERE FALSE UNION ALL SELECT 2::BIGINT AS right_label WHERE FALSE"
				want = [][]any{}
			}
			s := querySetOpsBag(q, "left_label", "INT8", want)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				columns, err := rows.ColumnTypes()
				if err != nil || len(columns) != 1 {
					t.Fatal(columns, err)
				}
				if columns[0].Name() != "left_label" || columns[0].DatabaseTypeName() != "INT8" || columns[0].ScanType() != reflect.TypeFor[int64]() {
					t.Fatalf("common output metadata before Next: %v", columns[0])
				}
			}
			querySetOpsRoutes(t, p, b, s)
		}
		for _, tc := range []struct {
			q, typ string
			args   []any
			rows   [][][]any
		}{
			{"SELECT $1 AS v UNION ALL SELECT 1", "INT4", []any{7, nil}, [][][]any{{{int64(7)}, {int64(1)}}, {{nil}, {int64(1)}}}},
			{"SELECT $1 AS v UNION ALL SELECT $1::BIGINT", "INT8", []any{7, 9, nil}, [][][]any{{{int64(7)}, {int64(7)}}, {{int64(9)}, {int64(9)}}, {{nil}, {nil}}}},
		} {
			stmt, err := b.DB.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			for i, arg := range tc.args {
				s := querySetOpsBag(tc.q, "v", tc.typ, tc.rows[i])
				s.Args = []any{arg}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, stmt, tc.q}, b.Recorder, s)
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Original consumer methods complement, rather than replace, recorded Context lanes.
func TestReferenceLikeContextualLabelsSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.StringCoreControlExec(t, db, "CREATE TABLE t(x INTEGER)")
			fixture.StringCoreControlExec(t, db, "INSERT INTO t VALUES(1)")
			for _, word := range []string{"like", "ilike"} {
				q := "SELECT x+1 " + word + " FROM t;"
				rows, err := db.Query(q)
				if err != nil {
					t.Fatal(q, err)
				}
				columns, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(columns, []string{word}) {
					rows.Close()
					t.Fatal(columns, err)
				}
				fixture.StringCoreRetainedRows(t, rows, nil, nil, []string{"INT4"}, [][]any{{int64(2)}}, false)
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal(q, err)
				}
				rows, err = stmt.Query()
				fixture.StringCoreRetainedRows(t, rows, err, nil, []string{"INT4"}, [][]any{{int64(2)}}, false)
				stmt.Close()
			}
		})
	}
}

func TestReferenceLikeBaselineBehaviorSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.StringCoreControlExec(t, db, "CREATE TABLE t(v TEXT,c CHAR(3),b BYTEA)")
			fixture.StringCoreControlExec(t, db, "INSERT INTO t VALUES('AbC','a','\\xc3a9')")
			for _, tc := range []struct {
				query string
				want  []any
			}{
				{"SELECT v LIKE 'AbC',v LIKE 'b',v ILIKE 'abc',v NOT ILIKE 'abc' FROM t", []any{true, false, true, false}},
				{"SELECT v ~~ 'A%',v !~~ 'A%',v ~~* 'a%',v !~~* 'a%' FROM t", []any{true, false, true, false}},
				{"SELECT c LIKE 'a',c LIKE 'a__',CAST(c AS TEXT) LIKE 'a' FROM t", []any{false, true, true}},
				{"SELECT b LIKE '\\x5f',b LIKE '\\x5f5f',NULL LIKE 'x',v LIKE NULL FROM t", []any{false, true, nil, nil}},
				{"SELECT 'a%b' LIKE 'a!%b' ESCAPE '!','ab' ILIKE 'a_' ESCAPE 'A','É' ILIKE 'é' FROM t", []any{true, true, false}},
			} {
				t.Run(tc.query, func(t *testing.T) {
					types := make([]string, len(tc.want))
					for i := range types {
						types[i] = "BOOL"
					}
					rows, err := db.Query(tc.query)
					if err != nil {
						t.Fatal(err)
					}
					fixture.StringCoreRetainedRows(t, rows, nil, nil, types, [][]any{tc.want}, false)
					stmt, err := db.Prepare(tc.query)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					rows, err = stmt.Query()
					if err != nil {
						t.Fatal(err)
					}
					fixture.StringCoreRetainedRows(t, rows, nil, nil, types, [][]any{tc.want}, false)
				})
			}
		})
	}
}

func TestReferenceLikeAggregateJoinOrderConsumersSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.StringCoreControlExec(t, db, "CREATE TABLE t(id INT,v TEXT,p TEXT,e TEXT)")
			fixture.StringCoreControlExec(t, db, "INSERT INTO t VALUES(1,'ab','a%','!'),(2,'b','a%','!'),(3,NULL,NULL,NULL)")
			fixture.StringCoreControlExec(t, db, "CREATE TABLE patterns(id INT,p TEXT,e TEXT)")
			fixture.StringCoreControlExec(t, db, "INSERT INTO patterns VALUES(1,'a%','!'),(2,'b%','!')")
			for _, tc := range []struct {
				q     string
				types []string
				rows  [][]any
			}{
				{"SELECT COUNT(v LIKE p ESCAPE e),COUNT(v NOT ILIKE p ESCAPE e) FROM t", []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}},
				{"SELECT (CASE COUNT(*) WHEN 3 THEN 'Ab' ELSE 'x' END) ILIKE (CASE COUNT(*) WHEN 3 THEN 'A%' ELSE 'y' END) ESCAPE (CASE COUNT(*) WHEN 3 THEN '!' ELSE '?' END) FROM t", []string{"BOOL"}, [][]any{{true}}},
				{"SELECT (CASE COUNT(*) WHEN 0 THEN NULL::text ELSE 'x' END) LIKE (CASE COUNT(*) WHEN 0 THEN NULL::text ELSE 'x' END) ESCAPE 'ab' FROM t WHERE FALSE", []string{"BOOL"}, [][]any{{nil}}},
				{"SELECT a.id,b.id FROM t a JOIN patterns b ON a.v LIKE b.p ESCAPE b.e ORDER BY a.id,b.id", []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}},
				{"SELECT id FROM t ORDER BY v LIKE 'a%',id", []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {int64(3)}}},
				{"SELECT v OPERATOR(\"pg_catalog\".~~) p FROM t ORDER BY id", []string{"BOOL"}, [][]any{{true}, {false}, {nil}}},
			} {
				t.Run(tc.q, func(t *testing.T) {
					rows, err := db.Query(tc.q)
					fixture.StringCoreRetainedRows(t, rows, err, nil, tc.types, tc.rows, false)
					stmt, err := db.Prepare(tc.q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					rows, err = stmt.Query()
					fixture.StringCoreRetainedRows(t, rows, err, nil, tc.types, tc.rows, false)
				})
			}
		})
	}
}

func TestReferenceLikeMixedLogicalBetweenConsumersSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.StringCoreControlExec(t, db, "CREATE TABLE t(id INT,v TEXT)")
			fixture.StringCoreControlExec(t, db, "INSERT INTO t VALUES(1,'ab'),(2,'b'),(3,NULL)")
			for _, tc := range []struct {
				expression string
				rows       [][]any
			}{
				{"NOT v LIKE 'a%'", [][]any{{false}, {true}, {nil}}},
				{"v LIKE 'a%' = FALSE", [][]any{{false}, {true}, {nil}}},
				{"v LIKE 'a%' OR v LIKE 'b%' AND FALSE", [][]any{{true}, {false}, {nil}}},
				{"v LIKE 'a%' AND id BETWEEN 1 AND 2", [][]any{{true}, {false}, {false}}},
				{"(v LIKE 'a%') BETWEEN FALSE AND TRUE", [][]any{{true}, {true}, {nil}}},
				{"NOT v LIKE 'a%' AND TRUE OR v LIKE 'b%'", [][]any{{false}, {true}, {nil}}},
			} {
				t.Run(tc.expression, func(t *testing.T) {
					q := "SELECT " + tc.expression + " FROM t ORDER BY id"
					rows, err := db.Query(q)
					fixture.StringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, tc.rows, false)
					held, err := db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					rows, err = held.Query()
					fixture.StringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, tc.rows, false)
				})
			}
			for _, tc := range []struct {
				q, token, position string
			}{
				{"SELECT v LIKE 'a%' BETWEEN FALSE AND TRUE FROM t", "BETWEEN", "20"},
				{"SELECT v BETWEEN 'a' AND 'z' LIKE 'a%' FROM t", "LIKE", "30"},
				{"SELECT v BETWEEN 'a' AND 'z' LIKE FROM t", "LIKE", "30"},
				{"SELECT v BETWEEN 'a' AND 'z' ILIKE FROM t", "ILIKE", "30"},
				{"SELECT v='a' LIKE FROM t", "FROM", "19"},
				{"SELECT v LIKE 'a' LIKE FROM t", "LIKE", "19"},
			} {
				t.Run(tc.q, func(t *testing.T) {
					want := fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: tc.position}
					rows, err := db.Query(tc.q)
					if rows != nil {
						rows.Close()
						t.Fatal("ungrouped predicate chain exposed cursor")
					}
					fixture.StringCoreControlDiagnostic(t, err, want)
					stmt, err := db.Prepare(tc.q)
					if stmt != nil {
						stmt.Close()
						t.Fatal("ungrouped predicate chain prepared successfully")
					}
					fixture.StringCoreControlDiagnostic(t, err, want)
				})
			}
			q := "SELECT (v BETWEEN 'a' AND 'z') LIKE FROM t ORDER BY id"
			rows, err := db.Query(q)
			if err != nil {
				t.Fatal(err)
			}
			names, err := rows.Columns()
			if err != nil || len(names) != 1 || names[0] != "like" {
				rows.Close()
				t.Fatal(names, err)
			}
			fixture.StringCoreRetainedRows(t, rows, nil, nil, []string{"BOOL"}, [][]any{{true}, {true}, {nil}}, false)
			held, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			rows, err = held.Query()
			fixture.StringCoreRetainedRows(t, rows, err, nil, []string{"BOOL"}, [][]any{{true}, {true}, {nil}}, false)
		})
	}
}

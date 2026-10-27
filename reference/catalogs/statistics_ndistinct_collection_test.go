package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

const ndistinctQuery = "SELECT s.stxname,d.stxdinherit,d.stxdndistinct FROM pg_statistic_ext s JOIN pg_statistic_ext_data d ON d.stxoid=s.oid ORDER BY s.stxname,d.stxdinherit"

func ndistinctRows(pg bool, rows ...[]any) fixture.Step {
	return ndistinctRawResult(pg, fixture.Select(ndistinctQuery, []string{"stxname", "stxdinherit", "stxdndistinct"}, []string{"NAME", "BOOL", statisticsDatabaseTypeName(pg, "pg_ndistinct")}, rows), 2)
}

// Statistics output is native text, but pq exposes unknown-OID output as bytes.
// Keep this raw contract independent of DatabaseTypeName (which pq leaves empty).
// Fixture row comparison still requires the complete canonical text or SQL NULL.
func ndistinctRawResult(pg bool, step fixture.Step, columns ...int) fixture.Step {
	step.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		for _, selected := range columns {
			if column != selected {
				continue
			}
			if value == nil {
				return true
			}
			if pg {
				if _, ok := value.([]byte); !ok {
					t.Fatalf("raw statistics column %d row %d: got %T, want []byte", column, row, value)
				}
			} else if _, ok := value.(string); !ok {
				t.Fatalf("raw statistics column %d row %d: got %T, want string", column, row, value)
			}
			return true
		}
		return false
	}
	return step
}

type ndistinctScenario struct {
	run  func(fixture.Step)
	pg   bool
	conn *sql.Conn
	pair *fixture.Pair
}

func withNDistinct(t *testing.T, body func(*testing.T, ndistinctScenario)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) {
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
					}
					body(t, ndistinctScenario{run: run, pg: b.PG, conn: conn, pair: p})
				})
			}
		})
	}
}

func TestReferenceStatisticsNDistinctCorrelation(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE nd_t(a INT,b INT)", 0))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(1,1),(1,1),(2,2),(2,2)", 4))
		s.run(fixture.Exec("CREATE STATISTICS nd_s(ndistinct) ON a,b FROM nd_t", 0))
		s.run(ndistinctRows(s.pg))
		s.run(fixture.Select("SELECT (SELECT stxdndistinct FROM pg_statistic_ext_data) AS v", []string{"v"}, []string{statisticsDatabaseTypeName(s.pg, "pg_ndistinct")}, [][]any{{nil}}))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 2}`}))
		s.run(fixture.Select("SELECT a,b FROM nd_t ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(1)}, {int64(2), int64(2)}, {int64(2), int64(2)}}))
	})
}

func TestReferenceStatisticsNDistinctNulls(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE nd_t(a INT,b INT)", 0))
		s.run(fixture.Exec("CREATE STATISTICS nd_s(ndistinct) ON a,b FROM nd_t", 0))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(NULL,NULL),(NULL,NULL)", 2))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 1}`}))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(NULL,1),(NULL,1),(1,NULL),(1,1)", 4))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 4}`}))
		s.run(fixture.Exec("DELETE FROM nd_t", 6))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 4}`}))
	})
}

func TestReferenceStatisticsNDistinctSubsets(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE nd_t(a INT,b INT,c INT)", 0))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(1,10,100),(1,10,101),(2,20,100),(2,20,100)", 4))
		s.run(fixture.Exec("CREATE STATISTICS nd_s(ndistinct) ON c,a,b FROM nd_t", 0))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 2, "1, 3": 3, "2, 3": 3, "1, 2, 3": 3}`}))
		s.run(fixture.Exec("DROP STATISTICS nd_s", 0))
		s.run(fixture.Exec("CREATE STATISTICS nd_s(ndistinct) ON a,c FROM nd_t", 0))
		s.run(ndistinctRows(s.pg))
		s.run(fixture.Exec("ALTER TABLE nd_t DROP COLUMN b", 0))
		s.run(fixture.Exec("ALTER TABLE nd_t RENAME COLUMN c TO renamed_c", 0))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 3": 3}`}))
		s.run(fixture.Exec(`CREATE TABLE nd_bp(a CHAR(3) COLLATE "C",b INT)`, 0))
		s.run(fixture.Exec("INSERT INTO nd_bp VALUES('x',1),('x ',1),('y',2)", 3))
		s.run(fixture.Exec("CREATE STATISTICS nd_bp_s(ndistinct) ON a,b FROM nd_bp", 0))
		s.run(fixture.Exec("ANALYZE nd_bp", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_bp_s", false, `{"1, 2": 2}`}, []any{"nd_s", false, `{"1, 3": 3}`}))
	})
}

func TestReferenceStatisticsNDistinctRefresh(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE nd_t(a INT,b INT,c INT)", 0))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(1,1,1),(2,2,2)", 2))
		s.run(fixture.Exec("CREATE STATISTICS nd_s(ndistinct) ON a,b FROM nd_t", 0))
		s.run(fixture.Exec("CREATE STATISTICS nd_z(ndistinct) ON a,c FROM nd_t", 0))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(fixture.Exec("INSERT INTO nd_t VALUES(3,3,3)", 1))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 2}`}, []any{"nd_z", false, `{"1, 3": 2}`}))
		held, err := s.conn.QueryContext(s.pair.Ctx, ndistinctQuery)
		if err != nil {
			t.Fatal(err)
		}
		// pq streams on its connection; detach its observations before another command.
		if s.pg {
			held.Close()
		}
		s.run(fixture.Exec("ANALYZE nd_t(a,b)", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 3}`}, []any{"nd_z", false, `{"1, 3": 2}`}))
		if !s.pg {
			defer held.Close()
			if !held.Next() {
				t.Fatal(held.Err())
			}
			var name string
			var inherited bool
			var value string
			if err := held.Scan(&name, &inherited, &value); err != nil {
				t.Fatal(err)
			}
			if name != "nd_s" || inherited || value != `{"1, 2": 2}` {
				t.Fatal(name, inherited, value)
			}
		}
		s.run(fixture.Exec("ANALYZE nd_t(a)", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 3}`}, []any{"nd_z", false, `{"1, 3": 2}`}))
		s.run(fixture.Exec("ANALYZE nd_t", 0))
		s.run(ndistinctRows(s.pg, []any{"nd_s", false, `{"1, 2": 3}`}, []any{"nd_z", false, `{"1, 3": 3}`}))
	})
}

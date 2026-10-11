package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePreparedRebindShadow(t *testing.T) {
	t.Run("read_and_dependency", func(t *testing.T) {
		preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			preparedRebindSetup(t, p, b, c)
			routes := preparedRebindRoutes(t, p, b, c, "SELECT v FROM t ORDER BY v", "PREPARE rb_read AS SELECT v FROM t ORDER BY v", "EXECUTE rb_read")
			read := func(value int64) {
				for _, r := range routes {
					preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{value}})
				}
			}
			read(7)
			preparedRebindShadow(t, p, b, c)
			fresh := sqlPreparedScalar("SELECT v FROM t", "v", "INT4", int64(9))
			sqlPreparedStep(t, p, b, c, fresh)
			fresh.Prepared = true
			sqlPreparedStep(t, p, b, c, fresh)
			read(7)
			sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE TABLE rb_late.unrelated(v INT)", 0))
			sqlPreparedStep(t, p, b, c, fixture.Exec("UPDATE rb_late.t SET v=8", 1))
			read(8) // Live data, without resolving the new shadow.
			sqlPreparedStep(t, p, b, c, fixture.Exec("ALTER TABLE rb_late.t ADD unused INT", 0))
			read(9) // Real dependency invalidation reanalyzes original names.
		})
	})

	for _, kind := range []string{"insert", "insert_source", "update", "update_source", "delete", "delete_source", "merge"} {
		for _, routeName := range []string{"go", "sql"} {
			t.Run(kind+"/"+routeName, func(t *testing.T) {
				preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					preparedRebindSetup(t, p, b, c)
					query, actuals, count := "", "", int64(1)
					var args []any
					switch kind {
					case "insert":
						query, actuals, args = "INSERT INTO t(v) VALUES($1)", "(12)", []any{12}
					case "insert_source":
						query = "INSERT INTO t(v) SELECT v+5 FROM t"
					case "update":
						query = "UPDATE t SET v=8 WHERE v=7 RETURNING v"
					case "update_source":
						query = "UPDATE t AS dst SET v=src.v+1 FROM t AS src WHERE dst.v=src.v RETURNING dst.v"
					case "delete":
						query = "DELETE FROM t WHERE v=7 RETURNING v"
					case "delete_source":
						query = "DELETE FROM t AS dst USING t AS src WHERE dst.v=src.v RETURNING dst.v"
					case "merge":
						query = "MERGE INTO t AS dst USING (SELECT v+5 AS v FROM t) AS src ON dst.v=src.v WHEN NOT MATCHED THEN INSERT(v) VALUES(src.v)"
						if b.PG {
							count = 0 // Existing pq MERGE command-tag affected-count contract.
						}
					}
					var r preparedRebindRoute
					if routeName == "go" {
						r = preparedRebindRoute{Executor: preparedRebindHold(t, p, c, query), SQL: query}
					} else {
						sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE rb_write AS "+query, 0))
						r = preparedRebindRoute{Executor: c, SQL: "EXECUTE rb_write" + actuals}
						args = nil
					}
					// Warm every retained write, then roll back its row effects.
					sqlPreparedStep(t, p, b, c, fixture.Exec("BEGIN", 0))
					sqlPreparedStep(t, p, b, r, fixture.Exec(r.SQL, count, args...))
					sqlPreparedStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
					preparedRebindShadow(t, p, b, c)
					switch kind {
					case "update":
						preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}})
						preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, nil)
					case "update_source":
						preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}})
					case "delete", "delete_source":
						preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
						preparedRebindRead(t, p, b, r, []string{"v"}, []string{"INT4"}, nil)
					default:
						sqlPreparedStep(t, p, b, r, fixture.Exec(r.SQL, count, args...))
					}
					late := [][]any{{int64(7)}, {int64(12)}}
					if kind == "update" || kind == "update_source" {
						late = [][]any{{int64(8)}}
					} else if kind == "delete" || kind == "delete_source" {
						late = nil
					}
					sqlPreparedStep(t, p, b, c, fixture.Select("SELECT v FROM rb_late.t ORDER BY v", []string{"v"}, []string{"INT4"}, late))
					sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT v FROM rb_early.t", "v", "INT4", int64(9)))
					if kind == "insert" {
						sqlPreparedStep(t, p, b, c, fixture.Exec("ALTER TABLE rb_late.t ADD unused INT", 0))
						sqlPreparedStep(t, p, b, r, fixture.Exec(r.SQL, 1, args...))
						sqlPreparedStep(t, p, b, c, fixture.Select("SELECT v FROM rb_early.t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(12)}}))
					}
				})
			})
		}
	}

	for i, query := range []string{
		"SELECT l.v AS a,r.v AS b FROM t AS l JOIN t AS r ON l.v=r.v",
		"SELECT v FROM (SELECT v FROM t) AS d",
		"SELECT v,(SELECT v FROM t) AS s FROM t",
		"WITH c AS (SELECT v FROM t) SELECT v FROM c",
		"SELECT l.v AS a,r.v AS b FROM t AS l CROSS JOIN rb_late.t AS r",
		"WITH unused AS (SELECT v FROM rb_late.dep) SELECT v FROM t",
	} {
		t.Run(fmt.Sprintf("source_%d", i), func(t *testing.T) {
			preparedRebindScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				preparedRebindSetup(t, p, b, c)
				sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE TABLE rb_late.dep(v INT)", 0))
				routes := preparedRebindRoutes(t, p, b, c, query, "PREPARE rb_source AS "+query, "EXECUTE rb_source")
				names, types, want := []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}
				if i == 0 || i == 4 {
					names, types, want = []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}
				} else if i == 2 {
					names, types, want = []string{"v", "s"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}
				}
				for _, r := range routes {
					preparedRebindRead(t, p, b, r, names, types, want)
				}
				preparedRebindShadow(t, p, b, c)
				for _, r := range routes {
					preparedRebindRead(t, p, b, r, names, types, want)
				}
				dependency := "rb_late.t"
				if i == 5 {
					dependency = "rb_late.dep"
				}
				sqlPreparedStep(t, p, b, c, fixture.Exec("ALTER TABLE "+dependency+" ADD unused INT", 0))
				want = [][]any{{int64(9)}}
				if i == 0 || i == 2 {
					want = [][]any{{int64(9), int64(9)}}
				} else if i == 4 {
					want = [][]any{{int64(9), int64(7)}}
				}
				for _, r := range routes {
					preparedRebindRead(t, p, b, r, names, types, want)
				}
			})
		})
	}
}

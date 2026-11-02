package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 optimizer/util/inherit.c expand_single_inheritance_child copies the
// TABLESAMPLE descriptor into every physical child RTE. Counts below are
// independently specified per physical relation, never learned from native.
func TestReferenceSystemRowsInheritance(t *testing.T) {
	srCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS sr_kind_fdw CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		exec := func(sql string, affected int64) { srRun(t, p, b, c, fixture.Exec(sql, affected)) }
		exec("CREATE TABLE sr_parent(id integer NOT NULL)", 0)
		exec("CREATE TABLE sr_child_a() INHERITS(sr_parent)", 0)
		exec("CREATE TABLE sr_child_b() INHERITS(sr_parent)", 0)
		exec("INSERT INTO sr_parent VALUES(1),(2),(3),(4),(5)", 5)
		exec("INSERT INTO sr_child_a VALUES(11),(12),(13),(14),(15)", 5)
		exec("INSERT INTO sr_child_b VALUES(21),(22),(23),(24),(25)", 5)
		exec("CREATE TABLE sr_partitioned(id integer NOT NULL) PARTITION BY RANGE(id)", 0)
		exec("CREATE TABLE sr_partition_a PARTITION OF sr_partitioned FOR VALUES FROM(0) TO(10)", 0)
		exec("CREATE TABLE sr_partition_b PARTITION OF sr_partitioned FOR VALUES FROM(10) TO(20)", 0)
		exec("INSERT INTO sr_partitioned VALUES(1),(2),(3),(4),(5),(11),(12),(13),(14),(15)", 10)
		exec("CREATE MATERIALIZED VIEW sr_materialized AS SELECT id,payload FROM sr", 10)
		exec("CREATE VIEW sr_kind_view AS SELECT id,payload FROM sr", 0)
		exec("CREATE FOREIGN DATA WRAPPER sr_kind_fdw", 0)
		exec("CREATE SERVER sr_kind_server FOREIGN DATA WRAPPER sr_kind_fdw", 0)
		exec("CREATE FOREIGN TABLE sr_kind_foreign(id integer) SERVER sr_kind_server", 0)

		t.Run("source_kind_diagnostics", func(t *testing.T) {
			for _, query := range []string{
				"SELECT * FROM sr_kind_view TABLESAMPLE system_rows(2)",
				"WITH source AS (SELECT id FROM sr) SELECT * FROM source TABLESAMPLE system_rows(2)",
				"SELECT * FROM sr_kind_foreign TABLESAMPLE system_rows(2)",
			} {
				// Source-kind rejection precedes missing-method resolution too.
				for _, method := range []string{"system_rows", "absent_rows"} {
					q := strings.Replace(query, "system_rows", method, 1)
					position := strings.LastIndex(q, "FROM ") + len("FROM ") + 1
					for _, prepared := range []bool{false, true} {
						s := srError(q, "0A000", "TABLESAMPLE clause can only be applied to tables and materialized views", position)
						s.Prepared, s.ErrorStage = prepared, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						srRun(t, p, b, c, s)
					}
				}
			}
		})
		for _, item := range []struct {
			source, argument string
			count            int64
		}{
			{"sr_parent", "0", 0},
			{"sr_parent", "2", 6},
			{"sr_parent", "9223372036854775807", 15},
			{"ONLY sr_parent", "2", 2},
			{"ONLY (sr_parent)", "9223372036854775807", 5},
			{"sr_partitioned", "0", 0},
			{"sr_partitioned", "2", 4},
			{"sr_partitioned", "9223372036854775807", 10},
			{"ONLY sr_partitioned", "2", 0},
			{"sr_materialized", "0", 0},
			{"sr_materialized", "2", 2},
			{"sr_materialized", "11", 10},
		} {
			t.Run(item.source+"_"+item.argument, func(t *testing.T) {
				q := "SELECT count(*) AS n FROM " + item.source + " TABLESAMPLE system_rows(" + item.argument + ")"
				srBoth(t, p, b, c, srCount(q, item.count))
			})
		}
	})
}

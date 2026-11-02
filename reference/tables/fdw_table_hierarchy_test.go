package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWTableHierarchy(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		t.Run(mode, func(t *testing.T) {
			t.Run("inherits", func(t *testing.T) {
				fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
					fdwLive(t, p, b, r, run)
					fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.hierarchy(id integer,extra text)", 0)
					fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.hierarchy VALUES(7,'source')", 1)
					route := func(q string, count int64) { s := fixture.Exec(q, count); s.Prepared = prepared; run(s) }
					route("CREATE TABLE fdw_parent(id integer CHECK(id>0))", 0)
					route("CREATE FOREIGN TABLE fdw_child() INHERITS(fdw_parent) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'hierarchy')", 0)
					run(fixture.Select("SELECT p.relname AS parent,c.relname AS child,c.relkind AS kind FROM pg_inherits i JOIN pg_class p ON p.oid=i.inhparent JOIN pg_class c ON c.oid=i.inhrelid WHERE c.oid='fdw_child'::regclass::oid", []string{"parent", "child", "kind"}, []string{"NAME", "NAME", "CHAR"}, [][]any{{"fdw_parent", "fdw_child", "f"}}))
					run(fixture.Select("SELECT attname,attinhcount,attislocal FROM pg_attribute WHERE attrelid='fdw_child'::regclass::oid AND attnum>0 AND NOT attisdropped", []string{"attname", "attinhcount", "attislocal"}, []string{"NAME", "INT2", "BOOL"}, [][]any{{"id", int64(1), false}}))
					run(fixture.Select("SELECT id FROM ONLY fdw_parent", []string{"id"}, []string{"INT4"}, nil))
					run(fixture.Select("SELECT id FROM fdw_parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					route("INSERT INTO fdw_child(id) VALUES(9)", 1)
					run(fixture.Prepared(fixture.Select("SELECT id FROM fdw_parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}})))
					route("ALTER TABLE fdw_parent ADD COLUMN extra text", 0)
					run(fixture.Select("SELECT id,extra FROM fdw_parent ORDER BY id", []string{"id", "extra"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "source"}, {int64(9), nil}}))
					fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id,extra FROM fdw_source.hierarchy ORDER BY id", []string{"id", "extra"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "source"}, {int64(9), nil}}))
					route("ALTER FOREIGN TABLE fdw_child NO INHERIT fdw_parent", 0)
					run(fixture.Select("SELECT id FROM fdw_parent", []string{"id"}, []string{"INT4"}, nil))
					route("ALTER FOREIGN TABLE fdw_child INHERIT fdw_parent", 0)
					run(fixture.ErrorDiagnostic("DROP TABLE fdw_parent RESTRICT", fixture.PgDependRestrict("table fdw_parent", "foreign table fdw_child depends on table fdw_parent")))
					run(fixture.Exec("BEGIN", 0))
					route("DROP TABLE fdw_parent CASCADE", 0)
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.Select("SELECT id FROM fdw_parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
					route("DROP TABLE fdw_parent CASCADE", 0)
					run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname IN('fdw_parent','fdw_child')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id FROM fdw_source.hierarchy ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
				})
			})
			t.Run("foreign_parent", func(t *testing.T) {
				fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
					fdwLive(t, p, b, r, run)
					fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.foreign_child(id integer)", 0)
					fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.foreign_child VALUES(7)", 1)
					s := fixture.Exec("CREATE FOREIGN TABLE fdw_foreign_parent(id integer) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'data')", 0)
					s.Prepared = prepared
					run(s)
					s = fixture.Exec("CREATE FOREIGN TABLE fdw_foreign_child() INHERITS(fdw_foreign_parent) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'foreign_child')", 0)
					s.Prepared = prepared
					run(s)
					run(fixture.Select("SELECT id FROM ONLY fdw_foreign_parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
					run(fixture.Select("SELECT id FROM fdw_foreign_parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(7)}}))
					s = fixture.Exec("INSERT INTO fdw_foreign_child VALUES(9)", 1)
					s.Prepared = prepared
					run(s)
					fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id FROM fdw_source.foreign_child ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
				})
			})
			t.Run("partition", func(t *testing.T) {
				fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
					fdwLive(t, p, b, r, run)
					fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.partition_rows(id integer)", 0)
					fdwSourceExec(t, p, b, r, run, "INSERT INTO fdw_source.partition_rows VALUES(7)", 1)
					route := func(q string, count int64) { s := fixture.Exec(q, count); s.Prepared = prepared; run(s) }
					route("CREATE TABLE fdw_partitioned(id integer) PARTITION BY RANGE(id)", 0)
					route("CREATE FOREIGN TABLE fdw_partition PARTITION OF fdw_partitioned FOR VALUES FROM(0) TO(10) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'partition_rows')", 0)
					run(fixture.Select("SELECT c.relkind AS kind,c.relispartition,pg_get_expr(c.relpartbound,c.oid) AS bound,p.relname AS parent FROM pg_class c JOIN pg_inherits i ON i.inhrelid=c.oid JOIN pg_class p ON p.oid=i.inhparent WHERE c.oid='fdw_partition'::regclass::oid", []string{"kind", "relispartition", "bound", "parent"}, []string{"CHAR", "BOOL", "TEXT", "NAME"}, [][]any{{"f", true, "FOR VALUES FROM (0) TO (10)", "fdw_partitioned"}}))
					run(fixture.Select("SELECT id FROM ONLY fdw_partitioned", []string{"id"}, []string{"INT4"}, nil))
					run(fixture.Select("SELECT id FROM fdw_partitioned ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					route("INSERT INTO fdw_partitioned VALUES(9)", 1)
					run(fixture.Prepared(fixture.Select("SELECT id FROM fdw_partitioned ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}})))
					fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id FROM fdw_source.partition_rows ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
					run(fixture.Exec("BEGIN", 0))
					returning := fixture.Select("INSERT INTO fdw_partitioned VALUES(8) RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}})
					returning.Prepared = prepared
					run(returning)
					run(fixture.Select("SELECT id FROM fdw_partitioned ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}, {int64(9)}}))
					run(fixture.Exec("ROLLBACK", 0))
					fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id FROM fdw_source.partition_rows ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
					q := "CREATE FOREIGN TABLE fdw_overlap PARTITION OF fdw_partitioned FOR VALUES FROM(5) TO(8) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'partition_rows')"
					want := fdwDiagnostic("42P17", `partition "fdw_overlap" would overlap partition "fdw_partition"`)
					want.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "5)")]) + 1)
					s := fixture.ErrorDiagnostic(q, want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='fdw_overlap'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Exec("BEGIN", 0))
					route("ALTER TABLE fdw_partitioned DETACH PARTITION fdw_partition", 0)
					run(fixture.Select("SELECT id FROM fdw_partitioned", []string{"id"}, []string{"INT4"}, nil))
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.Select("SELECT id FROM fdw_partitioned ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
					route("DROP TABLE fdw_partitioned CASCADE", 0)
					run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname IN('fdw_partitioned','fdw_partition')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					fdwWriteSourceRows(t, p, b, r, fixture.Select("SELECT id FROM fdw_source.partition_rows ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
				})
			})
		})
	}
	t.Run("invalid_parent", func(t *testing.T) {
		fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
			fdwMetadata(run)
			run(fixture.Exec("CREATE TABLE fdw_not_partitioned(id integer)", 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("CREATE FOREIGN TABLE fdw_invalid PARTITION OF fdw_not_partitioned FOR VALUES FROM(0) TO(10) SERVER fdw_server", fdwDiagnostic("42P17", `"fdw_not_partitioned" is not partitioned`))
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='fdw_invalid'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
		})
	})
}

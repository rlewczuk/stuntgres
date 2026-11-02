package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLInsertKinds(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"dbins_kind_reader"})
	insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS insert_no_handler CASCADE")
		for _, q := range []string{
			"CREATE TABLE insert_kind_heap(id integer,payload text)",
			"CREATE INDEX insert_kind_index ON insert_kind_heap(id)",
			"CREATE TYPE insert_kind_composite AS(id integer)",
			"CREATE TABLE insert_kind_parent(id integer,payload text) PARTITION BY RANGE(id)",
			"CREATE TABLE insert_kind_leaf PARTITION OF insert_kind_parent DEFAULT",
			"CREATE VIEW insert_kind_view AS SELECT id,payload FROM insert_kind_heap",
			"CREATE SEQUENCE insert_kind_sequence",
			"CREATE ROLE dbins_kind_reader",
			"GRANT USAGE ON SCHEMA public,dbins_ext TO dbins_kind_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO insert_kind_heap VALUES(7,'heap')", 1))
		run(fixture.Exec("INSERT INTO insert_kind_parent VALUES(7,'partition')", 1))
		run(fixture.Exec("CREATE MATERIALIZED VIEW insert_kind_materialized AS SELECT id,payload FROM insert_kind_heap", 1))
		held := insertHeld(t, p, c, insertCall)
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, restricted := range []bool{false, true} {
			if restricted {
				run(fixture.Exec("SET SESSION AUTHORIZATION dbins_kind_reader", 0))
			}
			for _, tc := range []struct{ relation, detail string }{
				{"insert_kind_index", "This operation is not supported for indexes."},
				{"insert_kind_composite", "This operation is not supported for composite types."},
			} {
				for _, args := range []struct {
					vector         string
					count          int64
					source, target string
				}{
					{"1", 1, "{7}", "{9}"}, {"0", 0, "{}", "{}"}, {"1", 1, "{}", "{}"},
				} {
					s := insertError(insertCall, "42809", `cannot open relation "`+tc.relation+`"`, tc.relation, args.vector, args.count, args.source, args.target)
					s.Diagnostic.Detail = tc.detail
					insertBoth(t, p, b, c, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			}
			if restricted {
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
		}
		for _, tc := range []struct{ relation, source, want string }{
			{"insert_kind_heap", "{7}", "INSERT INTO insert_kind_heap(id,payload) VALUES('9','heap')"},
			{"insert_kind_view", "{7}", "INSERT INTO insert_kind_view(id,payload) VALUES('9','heap')"},
			{"insert_kind_materialized", "{7}", "INSERT INTO insert_kind_materialized(id,payload) VALUES('9','heap')"},
			{"insert_kind_parent", "{7}", "INSERT INTO insert_kind_parent(id,payload) VALUES('9','partition')"},
			{"insert_kind_leaf", "{7}", "INSERT INTO insert_kind_leaf(id,payload) VALUES('9','partition')"},
			{"insert_kind_sequence", "{1}", "INSERT INTO insert_kind_sequence(last_value,log_cnt,is_called) VALUES('9','0','f')"},
		} {
			s := insertSelect(insertCall, tc.want, tc.relation, "1", int64(1), tc.source, "{9}")
			insertBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		// Catalog rows are real SQL sources, with all four released namespace
		// columns. Owner/ACL OIDs are environment data, not fixed oracle IDs.
		prefix := "INSERT INTO pg_namespace(oid,nspname,nspowner,nspacl) VALUES("
		// Assert structure and replacement without copying a backend-produced
		// golden or requiring environment-specific owner identities.
		q := "SELECT left(sql,length('" + prefix + "'))='" + prefix + "' AS shape,position('''remote_namespace''' in sql)>0 AS replaced FROM dbins_ext.dblink_build_sql_insert('pg_catalog.pg_namespace','2'::int2vector,1,'{public}'::text[],'{remote_namespace}'::text[]) AS t(sql)"
		insertBoth(t, p, b, c, fixture.Select(q, []string{"shape", "replaced"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		for _, q := range []string{
			"CREATE FOREIGN DATA WRAPPER insert_no_handler",
			"CREATE SERVER insert_no_handler_server FOREIGN DATA WRAPPER insert_no_handler",
			"CREATE FOREIGN TABLE insert_kind_foreign(id integer) SERVER insert_no_handler_server",
		} {
			run(fixture.Exec(q, 0))
		}
		s := insertError(insertCall, "55000", `foreign-data wrapper "insert_no_handler" has no handler`, "insert_kind_foreign", "1", int64(1), "{1}", "{9}")
		s.Diagnostic.Context = "SQL statement \"SELECT id FROM insert_kind_foreign WHERE id = '1'\""
		insertBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	})
}

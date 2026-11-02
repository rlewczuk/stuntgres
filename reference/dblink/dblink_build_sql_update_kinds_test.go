package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLUpdateKinds(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"dbupd_kind_reader"})
	updateCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS update_no_handler CASCADE")
		for _, q := range []string{
			"CREATE TABLE update_kind_heap(id integer,payload text)",
			"CREATE INDEX update_kind_index ON update_kind_heap(id)",
			"CREATE TYPE update_kind_composite AS(id integer)",
			"CREATE TABLE update_kind_parent(id integer,payload text) PARTITION BY RANGE(id)",
			"CREATE TABLE update_kind_leaf PARTITION OF update_kind_parent DEFAULT",
			"CREATE VIEW update_kind_view AS SELECT id,payload FROM update_kind_heap",
			"CREATE SEQUENCE update_kind_sequence",
			"CREATE ROLE dbupd_kind_reader",
			"GRANT USAGE ON SCHEMA public,dbupd_ext TO dbupd_kind_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO update_kind_heap VALUES(7,'heap')", 1))
		run(fixture.Exec("INSERT INTO update_kind_parent VALUES(7,'partition')", 1))
		run(fixture.Exec("CREATE MATERIALIZED VIEW update_kind_materialized AS SELECT id,payload FROM update_kind_heap", 1))
		held := updateHeld(t, p, c, updateCall)
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, restricted := range []bool{false, true} {
			if restricted {
				run(fixture.Exec("SET SESSION AUTHORIZATION dbupd_kind_reader", 0))
			}
			for _, tc := range []struct{ relation, detail string }{
				{"update_kind_index", "This operation is not supported for indexes."},
				{"update_kind_composite", "This operation is not supported for composite types."},
			} {
				for _, args := range []struct {
					vector         string
					count          int64
					source, target string
				}{
					{"1", 1, "{7}", "{9}"}, {"0", 0, "{}", "{}"}, {"1", 1, "{}", "{}"},
				} {
					s := updateError(updateCall, "42809", `cannot open relation "`+tc.relation+`"`, tc.relation, args.vector, args.count, args.source, args.target)
					s.Diagnostic.Detail = tc.detail
					updateBoth(t, p, b, c, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			}
			if restricted {
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
		}
		for _, tc := range []struct{ relation, source, want string }{
			{"update_kind_heap", "{7}", "UPDATE update_kind_heap SET id = '9', payload = 'heap' WHERE id = '9'"},
			{"update_kind_view", "{7}", "UPDATE update_kind_view SET id = '9', payload = 'heap' WHERE id = '9'"},
			{"update_kind_materialized", "{7}", "UPDATE update_kind_materialized SET id = '9', payload = 'heap' WHERE id = '9'"},
			{"update_kind_parent", "{7}", "UPDATE update_kind_parent SET id = '9', payload = 'partition' WHERE id = '9'"},
			{"update_kind_leaf", "{7}", "UPDATE update_kind_leaf SET id = '9', payload = 'partition' WHERE id = '9'"},
			{"update_kind_sequence", "{1}", "UPDATE update_kind_sequence SET last_value = '9', log_cnt = '0', is_called = 'f' WHERE last_value = '9'"},
		} {
			s := updateSelect(updateCall, tc.want, tc.relation, "1", int64(1), tc.source, "{9}")
			updateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		// Catalog rows are real SQL sources, with all four released namespace
		// columns. Owner/ACL OIDs are environment data, not fixed oracle IDs.
		prefix := "UPDATE pg_namespace SET oid = "
		// Assert structure and replacement without copying a backend-produced
		// golden or requiring environment-specific owner identities.
		q := "SELECT left(sql,length('" + prefix + "'))='" + prefix + "' AS shape,position('nspname = ''remote_namespace''' in sql)>0 AND right(sql,length(' WHERE nspname = ''remote_namespace'''))=' WHERE nspname = ''remote_namespace''' AS replaced FROM dbupd_ext.dblink_build_sql_update('pg_catalog.pg_namespace','2'::int2vector,1,'{public}'::text[],'{remote_namespace}'::text[]) AS t(sql)"
		updateBoth(t, p, b, c, fixture.Select(q, []string{"shape", "replaced"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		for _, q := range []string{
			"CREATE FOREIGN DATA WRAPPER update_no_handler",
			"CREATE SERVER update_no_handler_server FOREIGN DATA WRAPPER update_no_handler",
			"CREATE FOREIGN TABLE update_kind_foreign(id integer) SERVER update_no_handler_server",
		} {
			run(fixture.Exec(q, 0))
		}
		s := updateError(updateCall, "55000", `foreign-data wrapper "update_no_handler" has no handler`, "update_kind_foreign", "1", int64(1), "{1}", "{9}")
		s.Diagnostic.Context = "SQL statement \"SELECT id FROM update_kind_foreign WHERE id = '1'\""
		updateBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	})
}

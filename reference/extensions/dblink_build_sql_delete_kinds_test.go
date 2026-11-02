package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLDeleteRelationKinds(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"dbdel_kind_reader"})
	deleteCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE TABLE delete_kind_heap(id text)",
			"CREATE INDEX delete_kind_index ON delete_kind_heap(id)",
			"CREATE TYPE delete_kind_composite AS(id text)",
			"CREATE TABLE delete_kind_parent(id integer) PARTITION BY RANGE(id)",
			"CREATE TABLE delete_kind_leaf PARTITION OF delete_kind_parent DEFAULT",
			"CREATE VIEW delete_kind_view AS SELECT id FROM delete_kind_heap",
			"CREATE SEQUENCE delete_kind_sequence",
			"CREATE ROLE dbdel_kind_reader",
			"GRANT USAGE ON SCHEMA public,dbdel_ext TO dbdel_kind_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		held := deleteHeld(t, p, c, deleteCall)
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, restricted := range []bool{false, true} {
			if restricted {
				run(fixture.Exec("SET SESSION AUTHORIZATION dbdel_kind_reader", 0))
			}
			for _, kind := range []struct{ relation, detail string }{
				{"delete_kind_index", "This operation is not supported for indexes."},
				{"delete_kind_composite", "This operation is not supported for composite types."},
			} {
				for _, args := range []struct{ keys string; count int64; values string }{
					{"1", 1, "{a}"},
					{"1", 0, "{}"},
					{"0", 1, "{}"},
					{"1", 1, "{}"},
				} {
					// Released table_openrv admission precedes SELECT authorization,
					// used-key/count validation and target-array cardinality checks.
					s := deleteError(deleteCall, "42809", `cannot open relation "`+kind.relation+`"`, kind.relation, args.keys, args.count, args.values)
					s.Diagnostic.Detail = kind.detail
					deleteBoth(t, p, b, c, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			}
			if restricted {
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			}
		}
		// table_openrv does not impose the statistics functions' wider kind
		// exclusions. Construction needs metadata, not scannable source storage.
		for _, tc := range []struct{ relation, want string }{
			{"delete_kind_view", "DELETE FROM delete_kind_view WHERE id = '7'"},
			{"delete_kind_parent", "DELETE FROM delete_kind_parent WHERE id = '7'"},
			{"delete_kind_sequence", "DELETE FROM delete_kind_sequence WHERE last_value = '7'"},
		} {
			s := deleteSelect(deleteCall, tc.want, tc.relation, "1", int64(1), "{7}")
			deleteBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
}

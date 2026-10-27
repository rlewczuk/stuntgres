package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePredefinedViewWrites(t *testing.T) {
	predefinedCases(t, []string{"pdef_view_actor", "pdef_view_owner"}, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("CREATE SCHEMA pdef_ad AUTHORIZATION pdef_view_owner")
		s.Exec("CREATE TABLE pdef_ad.t(id INT,v TEXT)")
		s.Run(fixture.Exec("INSERT INTO pdef_ad.t VALUES(1,'a'),(2,'b')", 2))
		s.Exec("CREATE VIEW pdef_ad.v AS SELECT id,v FROM pdef_ad.t")
		s.Exec("CREATE VIEW pdef_ad.inv WITH(security_invoker=true) AS SELECT id,v FROM pdef_ad.t")
		s.Exec("CREATE VIEW pdef_ad.distinct_v AS SELECT DISTINCT id,v FROM pdef_ad.t")
		s.Run(fixture.Exec("CREATE MATERIALIZED VIEW pdef_ad.mv AS SELECT id,v FROM pdef_ad.t", 2))
		s.Exec("GRANT pg_write_all_data TO pdef_view_actor")
		s.Actor("pdef_view_actor")
		// Writer-only logical target authority does not include column reads.
		for _, q := range []string{"UPDATE pdef_ad.v SET v='bad' WHERE id=1", "DELETE FROM pdef_ad.v WHERE id=1"} {
			predefinedDenied(s, q, "permission denied for view v", false)
		}
		s.Actor("")
		predefinedRows(s, "pdef_ad.t", predefinedInitialRows())
		s.Actor("pdef_view_actor")
		s.Run(fixture.Exec("INSERT INTO pdef_ad.v VALUES(3,'c')", 1))
		s.Actor("")
		predefinedRows(s, "pdef_ad.t", [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(3), "c"}})
		s.Actor("pdef_view_actor")
		s.Run(fixture.Exec("UPDATE pdef_ad.v SET v='z'", 3))
		s.Actor("")
		predefinedRows(s, "pdef_ad.t", [][]any{{int64(1), "z"}, {int64(2), "z"}, {int64(3), "z"}})
		s.Actor("pdef_view_actor")
		s.Run(fixture.Exec("DELETE FROM pdef_ad.v WHERE TRUE", 3))
		s.Actor("")
		predefinedRows(s, "pdef_ad.t", [][]any{})
		s.Run(fixture.Exec("INSERT INTO pdef_ad.t VALUES(1,'a'),(2,'b')", 2))
		s.Exec("GRANT pg_read_all_data TO pdef_view_actor")
		s.Actor("pdef_view_actor")
		s.Run(fixture.Select("UPDATE pdef_ad.v SET v='x' WHERE id=1 RETURNING id,v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}}))
		s.Run(fixture.Exec("DELETE FROM pdef_ad.v WHERE id=2", 1))
		s.Actor("")
		predefinedRows(s, "pdef_ad.t", [][]any{{int64(1), "x"}})
		// Source authority is the view owner unless security_invoker is true.
		// The actor's global capabilities must not silently replace that owner.
		s.Exec("ALTER VIEW pdef_ad.v OWNER TO pdef_view_owner")
		s.Exec("ALTER VIEW pdef_ad.inv OWNER TO pdef_view_owner")
		s.Actor("pdef_view_actor")
		predefinedDenied(s, "INSERT INTO pdef_ad.v VALUES(3,'bad')", "permission denied for table t", false)
		s.Run(fixture.Exec("INSERT INTO pdef_ad.inv VALUES(3,'invoker')", 1))
		s.Actor("")
		predefinedRows(s, "pdef_ad.t", [][]any{{int64(1), "x"}, {int64(3), "invoker"}})
		s.Exec("GRANT pg_write_all_data TO pdef_view_owner")
		s.Actor("pdef_view_actor")
		q := "INSERT INTO pdef_ad.v VALUES(4,'owner')"
		held := predefinedHeld(s, q)
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Exec(q, 1))
		s.Actor("")
		predefinedRows(s, "pdef_ad.t", [][]any{{int64(1), "x"}, {int64(3), "invoker"}, {int64(4), "owner"}})
		s.Exec("REVOKE pg_write_all_data FROM pdef_view_owner")
		s.Actor("pdef_view_actor")
		failure := fixture.RoleReferenceError(q, "42501", "permission denied for table t", "", false, "execute")
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, failure)
		s.Actor("")
		predefinedRows(s, "pdef_ad.t", [][]any{{int64(1), "x"}, {int64(3), "invoker"}, {int64(4), "owner"}})
		s.Exec("GRANT pg_write_all_data TO pdef_view_owner")
		s.Actor("pdef_view_actor")
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Exec(q, 1))
		// Real non-updatable kinds remain distinct from supported view writes.
		predefinedFailure(s, "INSERT INTO pdef_ad.mv VALUES(9,'bad')", fixture.Diagnostic{Code: "42809", Message: `cannot change materialized view "mv"`}, false, "execute")
		// Native Describe binds the view write shape too; both prepared routes
		// reject DISTINCT before execution (direct routes reject at Execute).
		predefinedFailure(s, "INSERT INTO pdef_ad.distinct_v VALUES(9,'bad')", fixture.Diagnostic{Code: "55000", Message: `cannot insert into view "distinct_v"`, Detail: "Views containing DISTINCT are not automatically updatable.", Hint: "To enable inserting into the view, provide an INSTEAD OF INSERT trigger or an unconditional ON INSERT DO INSTEAD rule."}, false, "prepare")
		s.Actor("")
		predefinedRows(s, "pdef_ad.t", [][]any{{int64(1), "x"}, {int64(3), "invoker"}, {int64(4), "owner"}, {int64(4), "owner"}})
		predefinedRows(s, "pdef_ad.mv", predefinedInitialRows())
	})
}

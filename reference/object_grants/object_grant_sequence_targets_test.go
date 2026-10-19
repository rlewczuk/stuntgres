package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantSequenceTargets(t *testing.T) {
	objectGrantScenario(t, []string{"og_seq_scope", "og_seq_other"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE SCHEMA og_seq_scope", 0))
		run(fixture.Exec("CREATE SCHEMA og_seq_other", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA og_seq_scope,og_seq_other TO og_object_reader,og_object_other", 0))
		run(fixture.Exec("CREATE SEQUENCE og_seq_scope.s START 10", 0))
		run(fixture.Exec("CREATE SEQUENCE og_seq_scope.second START 20", 0))
		run(fixture.Exec("CREATE SEQUENCE og_seq_other.outside START 30", 0))
		run(fixture.Exec("CREATE TABLE og_seq_scope.t(x INT)", 0))
		run(fixture.Exec("INSERT INTO og_seq_scope.t VALUES(7)", 1))
		run(fixture.Exec("GRANT SELECT ON og_seq_scope.s TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT last_value FROM og_seq_scope.s", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(10)}}))
			fixture.RoleGrantPermissionDenied(run, "SELECT nextval('og_seq_scope.s')", "sequence", "s")
		})
		run(fixture.Exec("GRANT USAGE ON TABLE og_seq_scope.s TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT nextval('og_seq_scope.s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(10)}}))
		})
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT SELECT,INSERT ON TABLE og_seq_scope.s TO og_object_other",
			fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "0LP01", Message: `sequence "s" only supports USAGE, SELECT, and UPDATE privileges`})
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Select("SELECT last_value FROM og_seq_scope.s", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(10)}}))
			fixture.RoleGrantPermissionDenied(run, "SELECT nextval('og_seq_scope.s')", "sequence", "s")
		})
		run(fixture.Exec("GRANT ALL ON TABLE og_seq_scope.s TO og_object_other", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Select("SELECT setval('og_seq_scope.s',11)", []string{"setval"}, []string{"INT8"}, [][]any{{int64(11)}}))
		})
		fixture.RoleGrantError(run, "GRANT SELECT ON SEQUENCE og_seq_scope.t TO og_object_reader", "42809", `"t" is not a sequence`)
		fixture.RoleGrantError(run, "GRANT SELECT ON SEQUENCE og_seq_scope.second,og_seq_scope.t TO og_object_reader", "42809", `"t" is not a sequence`)
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT last_value FROM og_seq_scope.second", "sequence", "second")
		})
		fixture.RoleGrantError(run, "GRANT USAGE ON ALL SEQUENCES IN SCHEMA og_seq_scope,og_seq_missing TO og_object_reader", "3F000", `schema "og_seq_missing" does not exist`)
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT nextval('og_seq_scope.second')", "sequence", "second")
		})
		run(fixture.Exec("GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA og_seq_scope TO og_object_reader", 0))
		run(fixture.Exec("CREATE SEQUENCE og_seq_scope.later START 40", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT setval('og_seq_scope.s',12)", []string{"setval"}, []string{"INT8"}, [][]any{{int64(12)}}))
			run(fixture.Select("SELECT nextval('og_seq_scope.second')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(20)}}))
			fixture.RoleGrantPermissionDenied(run, "SELECT nextval('og_seq_other.outside')", "sequence", "outside")
			fixture.RoleGrantPermissionDenied(run, "SELECT nextval('og_seq_scope.later')", "sequence", "later")
			fixture.RoleGrantPermissionDenied(run, "SELECT x FROM og_seq_scope.t", "table", "t")
			fixture.RoleGrantPermissionDenied(run, "GRANT USAGE ON ALL SEQUENCES IN SCHEMA og_seq_other TO og_object_other", "sequence", "outside")
		})
		var revokeOwner string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&revokeOwner); err != nil {
			t.Fatal(err)
		}
		revokeACL := func() {
			s := fixture.Select("SELECT unnest(relacl) AS acl FROM pg_class WHERE oid='og_seq_scope.second'::regclass", []string{"acl"}, []string{"ACLITEM"}, [][]any{{revokeOwner + "=rwU/" + revokeOwner}, {"og_object_reader=rwU/" + revokeOwner}})
			s.Bag = true
			run(s)
		}
		revokeACL()
		fixture.RoleGrantError(run, "REVOKE ALL ON ALL SEQUENCES IN SCHEMA og_seq_scope,og_seq_missing FROM og_object_reader", "3F000", `schema "og_seq_missing" does not exist`)
		revokeACL()
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT nextval('og_seq_scope.second') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(21)}}))
		})
		run(fixture.Exec("GRANT USAGE ON SEQUENCE og_seq_other.outside TO og_object_reader", 0))
		run(fixture.Exec("REVOKE ALL ON ALL SEQUENCES IN SCHEMA og_seq_scope FROM og_object_reader", 0))
		run(fixture.Exec("CREATE SEQUENCE og_seq_scope.after_revoke START 50", 0))
		run(fixture.Exec("GRANT USAGE ON SEQUENCE og_seq_scope.after_revoke TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT nextval('og_seq_scope.second')", "sequence", "second")
			run(fixture.Select("SELECT nextval('og_seq_other.outside') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(30)}}))
			run(fixture.Select("SELECT nextval('og_seq_scope.after_revoke') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(50)}}))
		})
		run(fixture.Exec("GRANT SELECT ON ALL TABLES IN SCHEMA og_seq_scope TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT x FROM og_seq_scope.t", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
			fixture.RoleGrantPermissionDenied(run, "SELECT last_value FROM og_seq_scope.s", "sequence", "s")
		})
		run(fixture.Exec("REVOKE USAGE ON SCHEMA og_seq_scope FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantError(run, "GRANT USAGE ON ALL SEQUENCES IN SCHEMA og_seq_scope TO og_object_other", "42501", "permission denied for schema og_seq_scope")
		})
	})
}

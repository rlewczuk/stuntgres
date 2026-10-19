package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectRevokeDiagnostics(t *testing.T) {
	objectGrantScenario(t, []string{"or_diag"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE SCHEMA or_diag", 0))
		run(fixture.Exec("CREATE SEQUENCE or_diag.or_diag_seq START 10", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA or_diag TO og_object_reader,og_object_other", 0))
		run(fixture.Exec("CREATE TABLE or_diag.t(x INT)", 0))
		run(fixture.Exec("INSERT INTO or_diag.t VALUES(7)", 1))
		run(fixture.Exec("GRANT SELECT ON or_diag.t TO og_object_other", 0))
		var owner string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		schemaACL := func() {
			s := fixture.Select("SELECT unnest(nspacl) AS acl FROM pg_namespace WHERE nspname='or_diag'", []string{"acl"}, []string{"ACLITEM"}, [][]any{{owner + "=UC/" + owner}, {"og_object_reader=U/" + owner}, {"og_object_other=U/" + owner}})
			s.Bag = true
			run(s)
		}
		for _, tc := range []struct{ query, code, message string }{
			{"REVOKE SELECT ON SCHEMA or_diag FROM og_object_reader", "0LP01", "invalid privilege type SELECT for schema"},
			{"REVOKE invented ON SCHEMA or_diag FROM og_object_reader", "42601", `unrecognized privilege type "invented"`},
			{"REVOKE USAGE(x) ON SCHEMA or_diag FROM og_object_reader", "0LP01", "column privileges are only valid for relations"},
			{"REVOKE USAGE ON SCHEMA or_diag FROM og_object_reader GRANTED BY og_object_other", "0A000", "grantor must be current user"},
			{"REVOKE SELECT ON PARAMETER lc_messages FROM og_object_reader", "0LP01", "invalid privilege type SELECT for parameter"},
		} {
			fixture.RoleGrantError(run, tc.query, tc.code, tc.message)
		}
		run(fixture.Exec("REVOKE USAGE ON SCHEMA or_diag FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "REVOKE USAGE ON SCHEMA or_diag FROM og_object_other", "schema", "or_diag")
		})
		run(fixture.Exec("GRANT USAGE ON SCHEMA or_diag TO og_object_reader", 0))
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		notice := func(query string, want ...fixture.Diagnostic) {
			fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, query, want...)
		}
		warning := func(message string) fixture.Diagnostic {
			return fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01006", Message: message}
		}
		schemaACL()
		notice("SET SESSION AUTHORIZATION og_object_reader")
		notice("REVOKE USAGE ON SCHEMA or_diag FROM og_object_other", warning(`no privileges could be revoked for "or_diag"`))
		notice("RESET SESSION AUTHORIZATION")
		schemaACL()
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Select("SELECT x FROM or_diag.t", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
		run(fixture.Exec("GRANT SELECT ON SEQUENCE or_diag.or_diag_seq TO og_object_reader WITH GRANT OPTION", 0))
		run(fixture.Exec("GRANT UPDATE ON SEQUENCE or_diag.or_diag_seq TO og_object_reader,og_object_other", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("GRANT SELECT ON SEQUENCE or_diag.or_diag_seq TO og_object_other", 0)) })
		notice("SET SESSION AUTHORIZATION og_object_reader")
		// PostgreSQL 18.6 emits the relation warning followed by each sequence
		// column warning; retain the complete ordered diagnostic contract.
		partialWarnings := []fixture.Diagnostic{warning(`not all privileges could be revoked for "or_diag_seq"`)}
		for _, column := range []string{"tableoid", "cmax", "xmax", "cmin", "xmin", "ctid", "last_value", "log_cnt", "is_called"} {
			partialWarnings = append(partialWarnings, warning(`not all privileges could be revoked for column "`+column+`" of relation "or_diag_seq"`))
		}
		notice("REVOKE SELECT,UPDATE ON SEQUENCE or_diag.or_diag_seq FROM og_object_other", partialWarnings...)
		notice("RESET SESSION AUTHORIZATION")
		fixture.RoleGrantActor(run, "og_object_other", func() {
			fixture.RoleGrantPermissionDenied(run, "SELECT last_value FROM or_diag.or_diag_seq", "sequence", "or_diag_seq")
			run(fixture.Select("SELECT nextval('or_diag.or_diag_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(10)}}))
		})
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("GRANT SELECT ON SEQUENCE or_diag.or_diag_seq TO og_object_other", 0)) })
		notice("SET SESSION AUTHORIZATION og_object_reader")
		// ALL suppresses only the relation warning, not the column warnings.
		notice("REVOKE ALL ON SEQUENCE or_diag.or_diag_seq FROM og_object_other", partialWarnings[1:]...)
		notice("RESET SESSION AUTHORIZATION")
		acl := func() {
			s := fixture.Select("SELECT unnest(relacl) AS acl FROM pg_class WHERE oid='or_diag.or_diag_seq'::regclass", []string{"acl"}, []string{"ACLITEM"}, [][]any{{owner + "=rwU/" + owner}, {"og_object_reader=r*w/" + owner}, {"og_object_other=w/" + owner}})
			s.Bag = true
			run(s)
		}
		acl()
		notice("REVOKE USAGE ON SEQUENCE or_diag.or_diag_seq FROM og_object_other")
		notice("REVOKE USAGE ON SEQUENCE or_diag.or_diag_seq FROM og_object_other")
		acl()
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Select("SELECT nextval('or_diag.or_diag_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(11)}}))
		})
	})
}

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleMembershipOwnerConsumers(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"mx_owner", "mx_member"}, func(s *fixture.RoleMembershipScenario) {
		t := s.T
		s.Owned("mx_owner")
		s.Exec("CREATE SCHEMA membership_schema AUTHORIZATION mx_owner")
		// Run before pair/schema/role cleanup even when a later assertion calls Fatal.
		defer func() {
			if _, err := s.C.ExecContext(s.P.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
				return
			}
			if _, err := s.C.ExecContext(s.P.Ctx, "DROP SCHEMA IF EXISTS membership_schema CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		s.Actor("mx_owner")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "mx_owner", func() {
			s.Exec("CREATE SEQUENCE membership_sequence")
			s.Exec("CREATE DOMAIN membership_type AS INT")
			s.Exec("CREATE VIEW membership_view AS SELECT id FROM membership_owned")
		})
		s.Actor("")
		s.Exec("GRANT mx_owner TO mx_member WITH INHERIT FALSE, SET FALSE")
		s.Actor("mx_member")
		denied := fixture.RoleReferenceError("CREATE TABLE membership_schema.denied(id INT)", "42501", "permission denied for schema membership_schema", "", s.Prepared, "execute")
		denied.Diagnostic.Position = "14"
		s.Run(denied)
		s.Fail("DROP SEQUENCE membership_sequence", "42501", "must be owner of sequence membership_sequence", "")
		s.Fail("DROP DOMAIN membership_type", "42501", "must be owner of type membership_type", "")
		s.Fail("DROP VIEW membership_view", "42501", "must be owner of view membership_view", "")
		s.Actor("")
		s.Exec("GRANT mx_owner TO mx_member WITH INHERIT TRUE")
		s.Actor("mx_member")
		s.Exec("CREATE TABLE membership_schema.allowed(id INT)")
		s.Run(fixture.Select("SELECT nextval('membership_sequence')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		s.Run(fixture.Select("SELECT id FROM membership_view ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		s.Exec("DROP VIEW membership_view")
		s.Exec("DROP DOMAIN membership_type")
		s.Exec("DROP SEQUENCE membership_sequence")
		s.Exec("DROP TABLE membership_schema.allowed")
		s.Exec("DROP SCHEMA membership_schema")
	})
}

func TestReferenceRoleMembershipSchemaOwnerSequenceDrop(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"my_schemaowner", "my_producer", "my_reader"}, func(s *fixture.RoleMembershipScenario) {
		t := s.T
		s.Exec("CREATE SCHEMA membership_drop_schema AUTHORIZATION my_schemaowner")
		defer func() {
			if _, err := s.C.ExecContext(s.P.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
				return
			}
			if _, err := s.C.ExecContext(s.P.Ctx, "DROP SCHEMA IF EXISTS membership_drop_schema CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		s.Exec("GRANT my_schemaowner TO my_producer,my_reader WITH INHERIT TRUE, SET FALSE")
		s.Actor("my_producer")
		s.Exec("CREATE SEQUENCE membership_drop_schema.foreign_sequence")
		s.Actor("my_reader")
		// The inherited schema owner may DROP but does not own the sequence;
		// namespace ownership must not confer ALTER authority.
		s.Fail("ALTER SEQUENCE membership_drop_schema.foreign_sequence RESTART 9", "42501", "must be owner of sequence foreign_sequence", "")
		s.Exec("DROP SEQUENCE membership_drop_schema.foreign_sequence")
		s.Exec("DROP SCHEMA membership_drop_schema")
	})
}

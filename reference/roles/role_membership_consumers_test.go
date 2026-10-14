package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleMembershipOwnerConsumers(t *testing.T) {
	membershipCases(t, []string{"mx_owner", "mx_member"}, func(s *membershipScenario) {
		t := s.t
		s.owned("mx_owner")
		s.exec("CREATE SCHEMA membership_schema AUTHORIZATION mx_owner")
		// Run before pair/schema/role cleanup even when a later assertion calls Fatal.
		defer func() {
			if _, err := s.c.ExecContext(s.p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
				return
			}
			if _, err := s.c.ExecContext(s.p.Ctx, "DROP SCHEMA IF EXISTS membership_schema CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		s.actor("mx_owner")
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "mx_owner", func() {
			s.exec("CREATE SEQUENCE membership_sequence")
			s.exec("CREATE DOMAIN membership_type AS INT")
			s.exec("CREATE VIEW membership_view AS SELECT id FROM membership_owned")
		})
		s.actor("")
		s.exec("GRANT mx_owner TO mx_member WITH INHERIT FALSE, SET FALSE")
		s.actor("mx_member")
		denied := fixture.RoleReferenceError("CREATE TABLE membership_schema.denied(id INT)", "42501", "permission denied for schema membership_schema", "", s.prepared, "execute")
		denied.Diagnostic.Position = "14"
		s.run(denied)
		s.fail("DROP SEQUENCE membership_sequence", "42501", "must be owner of sequence membership_sequence", "")
		s.fail("DROP DOMAIN membership_type", "42501", "must be owner of type membership_type", "")
		s.fail("DROP VIEW membership_view", "42501", "must be owner of view membership_view", "")
		s.actor("")
		s.exec("GRANT mx_owner TO mx_member WITH INHERIT TRUE")
		s.actor("mx_member")
		s.exec("CREATE TABLE membership_schema.allowed(id INT)")
		s.run(fixture.Select("SELECT nextval('membership_sequence')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		s.run(fixture.Select("SELECT id FROM membership_view ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		s.exec("DROP VIEW membership_view")
		s.exec("DROP DOMAIN membership_type")
		s.exec("DROP SEQUENCE membership_sequence")
		s.exec("DROP TABLE membership_schema.allowed")
		s.exec("DROP SCHEMA membership_schema")
	})
}

func TestReferenceRoleMembershipSchemaOwnerSequenceDrop(t *testing.T) {
	membershipCases(t, []string{"my_schemaowner", "my_producer", "my_reader"}, func(s *membershipScenario) {
		t := s.t
		s.exec("CREATE SCHEMA membership_drop_schema AUTHORIZATION my_schemaowner")
		defer func() {
			if _, err := s.c.ExecContext(s.p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
				return
			}
			if _, err := s.c.ExecContext(s.p.Ctx, "DROP SCHEMA IF EXISTS membership_drop_schema CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		s.exec("GRANT my_schemaowner TO my_producer,my_reader WITH INHERIT TRUE, SET FALSE")
		s.actor("my_producer")
		s.exec("CREATE SEQUENCE membership_drop_schema.foreign_sequence")
		s.actor("my_reader")
		// The inherited schema owner may DROP but does not own the sequence;
		// namespace ownership must not confer ALTER authority.
		s.fail("ALTER SEQUENCE membership_drop_schema.foreign_sequence RESTART 9", "42501", "must be owner of sequence foreign_sequence", "")
		s.exec("DROP SEQUENCE membership_drop_schema.foreign_sequence")
		s.exec("DROP SCHEMA membership_drop_schema")
	})
}

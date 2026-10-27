package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

// Restore before ownershipCases runs ordinary-role DROP OWNED cleanup. In
// particular, a failing native-red assertion must not leave public source-owned.
func ownershipProtectedRestore(s *fixture.RoleMembershipScenario, database string) {
	s.T.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION"} {
		if _, err := s.C.ExecContext(ctx, q); err != nil {
			s.T.Error("protected fixture reset", err)
		}
	}
	if database != "" {
		if _, err := s.C.ExecContext(ctx, "ALTER DATABASE "+pq.QuoteIdentifier(database)+" OWNER TO "+pq.QuoteIdentifier(s.Bootstrap)); err != nil {
			s.T.Error("restore actual bootstrap database owner", err)
		}
	}
	// Renamed public is known only through this test's supported SQL spelling.
	// DROP/CREATE also works after the successful destructive case removed it.
	for _, q := range []string{
		"DROP SCHEMA IF EXISTS own_public_renamed CASCADE",
		"DROP SCHEMA IF EXISTS public CASCADE",
		"CREATE SCHEMA public AUTHORIZATION " + pq.QuoteIdentifier(s.Bootstrap),
		"DROP SCHEMA IF EXISTS own_protected_control CASCADE",
	} {
		if _, err := s.C.ExecContext(ctx, q); err != nil {
			s.T.Error("restore public fixture", q, err)
		}
	}
	if s.Backend.PG {
		if _, err := s.C.ExecContext(ctx, "GRANT USAGE ON SCHEMA public TO PUBLIC"); err != nil {
			s.T.Error("restore fixture public rights", err)
		}
	}
}

func TestReferenceRoleOwnershipPublicSchema(t *testing.T) {
	t.Run("reassign-renamed-identity", func(t *testing.T) {
		ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
			defer ownershipProtectedRestore(s, "")
			s.Exec("CREATE TABLE public.own_public_identity(id INT)")
			s.Run(fixture.Exec("INSERT INTO public.own_public_identity VALUES(7)", 1))
			var namespace int64
			if err := s.C.QueryRowContext(s.P.Ctx, "SELECT relnamespace::BIGINT FROM pg_class WHERE oid='public.own_public_identity'::regclass::oid").Scan(&namespace); err != nil || namespace <= 0 {
				s.T.Fatal("public namespace identity", namespace, err)
			}
			// PostgreSQL's serialized fixture recreates public. Native starts with
			// its fixed canonical identity; neither identity implies protection.
			if !s.Backend.PG && namespace != 2200 {
				s.T.Fatal("native initial public identity", namespace)
			}
			s.Exec("ALTER SCHEMA public OWNER TO own_source")
			s.Exec("ALTER SCHEMA public RENAME TO own_public_renamed")
			s.Exec("REASSIGN OWNED BY own_source TO own_target")
			s.Exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(ownershipDatabaseName(s)) + " TO own_target")
			s.Exec("GRANT own_other TO own_target WITH INHERIT FALSE, SET TRUE")
			s.Actor("own_target")
			s.Exec("CREATE TABLE own_public_renamed.own_public_successor(id INT)")
			// Actual changed-owner authority, not PUBLIC CREATE or a same-owner
			// no-op. Schema rename has an independent dialect admission gate.
			s.Exec("ALTER SCHEMA own_public_renamed OWNER TO own_other")
			s.Actor("")
			s.Exec("ALTER SCHEMA own_public_renamed OWNER TO own_target")
			s.Exec("ALTER SCHEMA own_public_renamed RENAME TO public")
			ownershipSelect(s, "SELECT relnamespace::BIGINT AS n FROM pg_class WHERE oid='public.own_public_identity'::regclass::oid", []string{"n"}, []string{"INT8"}, [][]any{{namespace}})
			ownershipOwner(s, "public.own_public_identity", s.Bootstrap)
			ownershipRows(s, "public.own_public_identity")
			ownershipOwner(s, "public.own_public_successor", "own_target")
			if s.Backend.PG {
				ownershipSelect(s, "SELECT r.rolname FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname='public'", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_target"}})
			}
			s.Exec("DROP ROLE own_source")
			ownershipCount(s, "SELECT count(*) FROM pg_roles WHERE rolname='own_source'", 0)
			// Restore canonical ownership without bulk cleanup of the successor.
			s.Exec("ALTER SCHEMA public OWNER TO " + pq.QuoteIdentifier(s.Bootstrap))
		})
	})
	t.Run("restrict-and-cascade", func(t *testing.T) {
		ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
			defer ownershipProtectedRestore(s, "")
			s.Exec("CREATE SCHEMA own_protected_control AUTHORIZATION " + pq.QuoteIdentifier(s.Bootstrap))
			s.Exec("CREATE TABLE own_protected_control.sentinel(id INT)")
			s.Run(fixture.Exec("INSERT INTO own_protected_control.sentinel VALUES(9)", 1))
			s.Exec("GRANT SELECT ON own_protected_control.sentinel TO own_source")
			s.Exec("CREATE TABLE public.own_public_foreign(id INT)")
			s.Run(fixture.Exec("INSERT INTO public.own_public_foreign VALUES(7)", 1))
			s.Exec("CREATE VIEW own_protected_control.consumer AS SELECT id FROM public.own_public_foreign")
			s.Exec("ALTER SCHEMA public OWNER TO own_source")
			s.ClearNotices()
			ownershipFailureDetailBag(s, "DROP OWNED BY own_source RESTRICT", fixture.Diagnostic{
				Code: "2BP01", Message: "cannot drop schema public because other objects depend on it",
				Detail: "table own_public_foreign depends on schema public\nview own_protected_control.consumer depends on table own_public_foreign",
				Hint:   "Use DROP ... CASCADE to drop the dependent objects too.",
			})
			ownershipNotices(s)
			ownershipRows(s, "public.own_public_foreign")
			ownershipRows(s, "own_protected_control.consumer")
			ownershipOwner(s, "public.own_public_foreign", s.Bootstrap)
			s.Actor("own_source")
			s.Exec("CREATE TABLE public.own_public_authority(id INT)")
			s.Exec("DROP TABLE public.own_public_authority")
			s.Actor("")
			// An explicit table grant is checked independently of namespace rights:
			// temporarily give inherited schema authority, then remove it again.
			s.Exec("ALTER SCHEMA own_protected_control OWNER TO own_other")
			s.Exec("GRANT own_other TO own_source WITH INHERIT TRUE, SET FALSE")
			s.Actor("own_source")
			ownershipSelect(s, "SELECT id FROM own_protected_control.sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
			s.Actor("")
			s.Exec("REVOKE own_other FROM own_source")
			s.ClearNotices()
			s.Exec("DROP OWNED BY own_source CASCADE")
			ownershipNoticesDetailBag(s, ownershipCascadeNotice("drop cascades to 2 other objects", "drop cascades to table own_public_foreign\ndrop cascades to view own_protected_control.consumer"))
			ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_public_foreign' OR relname='consumer'", 0)
			ownershipSelect(s, "SELECT id FROM own_protected_control.sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
			if s.Backend.PG {
				ownershipCount(s, "SELECT count(*) FROM pg_namespace WHERE nspname='public'", 0)
			}
			// Recreate before any later body or ordinary role cleanup needs public.
			s.Exec("CREATE SCHEMA public AUTHORIZATION " + pq.QuoteIdentifier(s.Bootstrap))
			if s.Backend.PG {
				s.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC")
			}
			s.Exec("GRANT own_other TO own_source WITH INHERIT TRUE, SET FALSE")
			s.Actor("own_source")
			ownershipFailure(s, "SELECT id FROM own_protected_control.sentinel", fixture.Diagnostic{Code: "42501", Message: "permission denied for table sentinel"})
			s.Actor("")
			s.Exec("REVOKE own_other FROM own_source")
			s.Exec("DROP ROLE own_source")
		})
	})
}

func TestReferenceRoleOwnershipBootstrapProtection(t *testing.T) {
	for _, operation := range []string{"reassign", "restrict", "cascade"} {
		t.Run(operation, func(t *testing.T) {
			ownershipProviderCases(t, func(s *fixture.RoleMembershipScenario, database string) {
				defer ownershipProtectedRestore(s, database)
				// These are genuinely bootstrap-owned, unlike the ordinary PG role
				// named postgres retained in the existing positive-control definitions.
				s.Exec("CREATE TABLE own_bootstrap_rows(id SERIAL,payload INT)")
				s.Run(fixture.Exec("INSERT INTO own_bootstrap_rows(payload) VALUES(7)", 1))
				s.Exec("CREATE SEQUENCE own_bootstrap_generator START WITH 40")
				ownershipSelect(s, "SELECT nextval('own_bootstrap_generator') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(40)}})
				ownershipSeed(s, "own_bootstrap_control", "own_other")
				s.Exec("GRANT SELECT ON own_bootstrap_rows TO own_reader WITH GRANT OPTION")
				s.Exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_actor")
				s.Exec("CREATE SCHEMA own_protected_control AUTHORIZATION own_actor")
				s.Exec("GRANT own_target TO own_actor WITH INHERIT FALSE, SET TRUE")
				s.Exec("GRANT own_other TO own_middle WITH ADMIN TRUE, INHERIT TRUE, SET TRUE")
				localIDs := make(map[string]int64, 4)
				for _, name := range []string{"own_bootstrap_rows", "own_bootstrap_rows_id_seq", "own_bootstrap_generator", "own_bootstrap_control"} {
					var id int64
					if err := s.C.QueryRowContext(s.P.Ctx, "SELECT "+pq.QuoteLiteral(name)+"::regclass::oid::BIGINT").Scan(&id); err != nil || id <= 0 {
						s.T.Fatal("local control identity", name, id, err)
					}
					localIDs[name] = id
				}
				roleIDs := make(map[string]int64, len(ownershipRoles))
				for _, role := range ownershipRoles {
					var id int64
					if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid::BIGINT FROM pg_roles WHERE rolname=$1", role).Scan(&id); err != nil || id <= 0 {
						s.T.Fatal("ordinary control identity", role, id, err)
					}
					roleIDs[role] = id
				}
				var bootstrapID int64
				if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid::BIGINT FROM pg_roles WHERE rolname=$1", s.Bootstrap).Scan(&bootstrapID); err != nil || bootstrapID <= 0 {
					s.T.Fatal("real bootstrap identity", bootstrapID, err)
				}
				q := "DROP OWNED BY " + pq.QuoteIdentifier(s.Bootstrap) + " RESTRICT"
				message := fmt.Sprintf("cannot drop objects owned by role %s because they are required by the database system", s.Bootstrap)
				if operation == "cascade" {
					q = "DROP OWNED BY " + pq.QuoteIdentifier(s.Bootstrap) + " CASCADE"
				} else if operation == "reassign" {
					q = "REASSIGN OWNED BY " + pq.QuoteIdentifier(s.Bootstrap) + " TO own_target"
					message = fmt.Sprintf("cannot reassign ownership of objects owned by role %s because they are required by the database system", s.Bootstrap)
				}
				s.ClearNotices()
				ownershipFailure(s, q, fixture.Diagnostic{Code: "2BP01", Message: message})
				ownershipNotices(s)
				ownershipSelect(s, "SELECT oid::BIGINT AS id FROM pg_roles WHERE rolname="+pq.QuoteLiteral(s.Bootstrap), []string{"id"}, []string{"INT8"}, [][]any{{bootstrapID}})
				for _, role := range ownershipRoles {
					ownershipSelect(s, "SELECT oid::BIGINT AS id FROM pg_roles WHERE rolname="+pq.QuoteLiteral(role), []string{"id"}, []string{"INT8"}, [][]any{{roleIDs[role]}})
				}
				for _, name := range []string{"own_bootstrap_rows", "own_bootstrap_rows_id_seq", "own_bootstrap_generator", "own_bootstrap_control"} {
					ownershipSelect(s, "SELECT "+pq.QuoteLiteral(name)+"::regclass::oid::BIGINT AS id", []string{"id"}, []string{"INT8"}, [][]any{{localIDs[name]}})
				}
				ownershipOwner(s, "own_bootstrap_rows", s.Bootstrap)
				ownershipOwner(s, "own_bootstrap_rows_id_seq", s.Bootstrap)
				ownershipOwner(s, "own_bootstrap_generator", s.Bootstrap)
				ownershipSelect(s, "SELECT id,payload FROM own_bootstrap_rows", []string{"id", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}})
				ownershipSelect(s, "SELECT nextval('own_bootstrap_rows_id_seq') AS n,nextval('own_bootstrap_generator') AS g", []string{"n", "g"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(41)}})
				ownershipOwner(s, "own_bootstrap_control", "own_other")
				ownershipRows(s, "own_bootstrap_control")
				ownershipDatabaseOwner(s, s.Bootstrap)
				s.Actor("own_target")
				ownershipFailure(s, "SET ROLE pg_database_owner", fixture.Diagnostic{Code: "42501", Message: `permission denied to set role "pg_database_owner"`})
				s.Actor("")
				s.Actor("own_actor")
				// Real CREATE ACL authorizes the schema-owner handler after refusal.
				s.Exec("ALTER SCHEMA own_protected_control OWNER TO own_target")
				s.Actor("own_reader")
				ownershipSelect(s, "SELECT payload FROM own_bootstrap_rows", []string{"payload"}, []string{"INT4"}, [][]any{{int64(7)}})
				s.Exec("GRANT SELECT ON own_bootstrap_rows TO own_downstream")
				s.Actor("own_downstream")
				ownershipSelect(s, "SELECT payload FROM own_bootstrap_rows", []string{"payload"}, []string{"INT4"}, [][]any{{int64(7)}})
				s.Actor("own_middle")
				s.Exec("SET ROLE own_other")
				ownershipRows(s, "own_bootstrap_control")
				s.Exec("RESET ROLE")
				s.Exec("GRANT own_other TO own_alt WITH INHERIT FALSE, SET TRUE")
				s.Actor("own_alt")
				s.Exec("SET ROLE own_other")
				s.Exec("RESET ROLE")
				s.Actor("")
				s.Flags("own_other", "own_middle", s.Bootstrap, 1, true, true, true)
			})
		})
	}
}

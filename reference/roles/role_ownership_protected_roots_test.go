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
func ownershipProtectedRestore(s *membershipScenario, database string) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION"} {
		if _, err := s.c.ExecContext(ctx, q); err != nil {
			s.t.Error("protected fixture reset", err)
		}
	}
	if database != "" {
		if _, err := s.c.ExecContext(ctx, "ALTER DATABASE "+pq.QuoteIdentifier(database)+" OWNER TO "+pq.QuoteIdentifier(s.bootstrap)); err != nil {
			s.t.Error("restore actual bootstrap database owner", err)
		}
	}
	// Renamed public is known only through this test's supported SQL spelling.
	// DROP/CREATE also works after the successful destructive case removed it.
	for _, q := range []string{
		"DROP SCHEMA IF EXISTS own_public_renamed CASCADE",
		"DROP SCHEMA IF EXISTS public CASCADE",
		"CREATE SCHEMA public AUTHORIZATION " + pq.QuoteIdentifier(s.bootstrap),
		"DROP SCHEMA IF EXISTS own_protected_control CASCADE",
	} {
		if _, err := s.c.ExecContext(ctx, q); err != nil {
			s.t.Error("restore public fixture", q, err)
		}
	}
	if s.backend.PG {
		if _, err := s.c.ExecContext(ctx, "GRANT USAGE ON SCHEMA public TO PUBLIC"); err != nil {
			s.t.Error("restore fixture public rights", err)
		}
	}
}

func TestReferenceRoleOwnershipPublicSchema(t *testing.T) {
	t.Run("reassign-renamed-identity", func(t *testing.T) {
		ownershipCases(t, func(s *membershipScenario) {
			defer ownershipProtectedRestore(s, "")
			s.exec("CREATE TABLE public.own_public_identity(id INT)")
			s.run(fixture.Exec("INSERT INTO public.own_public_identity VALUES(7)", 1))
			var namespace int64
			if err := s.c.QueryRowContext(s.p.Ctx, "SELECT relnamespace::BIGINT FROM pg_class WHERE oid='public.own_public_identity'::regclass::oid").Scan(&namespace); err != nil || namespace <= 0 {
				s.t.Fatal("public namespace identity", namespace, err)
			}
			// PostgreSQL's serialized fixture recreates public. Native starts with
			// its fixed canonical identity; neither identity implies protection.
			if !s.backend.PG && namespace != 2200 {
				s.t.Fatal("native initial public identity", namespace)
			}
			s.exec("ALTER SCHEMA public OWNER TO own_source")
			s.exec("ALTER SCHEMA public RENAME TO own_public_renamed")
			s.exec("REASSIGN OWNED BY own_source TO own_target")
			s.exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(ownershipDatabaseName(s)) + " TO own_target")
			s.exec("GRANT own_other TO own_target WITH INHERIT FALSE, SET TRUE")
			s.actor("own_target")
			s.exec("CREATE TABLE own_public_renamed.own_public_successor(id INT)")
			// Actual changed-owner authority, not PUBLIC CREATE or a same-owner
			// no-op. Schema rename has an independent dialect admission gate.
			s.exec("ALTER SCHEMA own_public_renamed OWNER TO own_other")
			s.actor("")
			s.exec("ALTER SCHEMA own_public_renamed OWNER TO own_target")
			s.exec("ALTER SCHEMA own_public_renamed RENAME TO public")
			ownershipSelect(s, "SELECT relnamespace::BIGINT AS n FROM pg_class WHERE oid='public.own_public_identity'::regclass::oid", []string{"n"}, []string{"INT8"}, [][]any{{namespace}})
			ownershipOwner(s, "public.own_public_identity", s.bootstrap)
			ownershipRows(s, "public.own_public_identity")
			ownershipOwner(s, "public.own_public_successor", "own_target")
			if s.backend.PG {
				ownershipSelect(s, "SELECT r.rolname FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname='public'", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_target"}})
			}
			s.exec("DROP ROLE own_source")
			ownershipCount(s, "SELECT count(*) FROM pg_roles WHERE rolname='own_source'", 0)
			// Restore canonical ownership without bulk cleanup of the successor.
			s.exec("ALTER SCHEMA public OWNER TO " + pq.QuoteIdentifier(s.bootstrap))
		})
	})
	t.Run("restrict-and-cascade", func(t *testing.T) {
		ownershipCases(t, func(s *membershipScenario) {
			defer ownershipProtectedRestore(s, "")
			s.exec("CREATE SCHEMA own_protected_control AUTHORIZATION " + pq.QuoteIdentifier(s.bootstrap))
			s.exec("CREATE TABLE own_protected_control.sentinel(id INT)")
			s.run(fixture.Exec("INSERT INTO own_protected_control.sentinel VALUES(9)", 1))
			s.exec("GRANT SELECT ON own_protected_control.sentinel TO own_source")
			s.exec("CREATE TABLE public.own_public_foreign(id INT)")
			s.run(fixture.Exec("INSERT INTO public.own_public_foreign VALUES(7)", 1))
			s.exec("CREATE VIEW own_protected_control.consumer AS SELECT id FROM public.own_public_foreign")
			s.exec("ALTER SCHEMA public OWNER TO own_source")
			s.clearNotices()
			ownershipFailureDetailBag(s, "DROP OWNED BY own_source RESTRICT", fixture.Diagnostic{
				Code: "2BP01", Message: "cannot drop schema public because other objects depend on it",
				Detail: "table own_public_foreign depends on schema public\nview own_protected_control.consumer depends on table own_public_foreign",
				Hint:   "Use DROP ... CASCADE to drop the dependent objects too.",
			})
			ownershipNotices(s)
			ownershipRows(s, "public.own_public_foreign")
			ownershipRows(s, "own_protected_control.consumer")
			ownershipOwner(s, "public.own_public_foreign", s.bootstrap)
			s.actor("own_source")
			s.exec("CREATE TABLE public.own_public_authority(id INT)")
			s.exec("DROP TABLE public.own_public_authority")
			s.actor("")
			// An explicit table grant is checked independently of namespace rights:
			// temporarily give inherited schema authority, then remove it again.
			s.exec("ALTER SCHEMA own_protected_control OWNER TO own_other")
			s.exec("GRANT own_other TO own_source WITH INHERIT TRUE, SET FALSE")
			s.actor("own_source")
			ownershipSelect(s, "SELECT id FROM own_protected_control.sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
			s.actor("")
			s.exec("REVOKE own_other FROM own_source")
			s.clearNotices()
			s.exec("DROP OWNED BY own_source CASCADE")
			ownershipNoticesDetailBag(s, ownershipCascadeNotice("drop cascades to 2 other objects", "drop cascades to table own_public_foreign\ndrop cascades to view own_protected_control.consumer"))
			ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_public_foreign' OR relname='consumer'", 0)
			ownershipSelect(s, "SELECT id FROM own_protected_control.sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
			if s.backend.PG {
				ownershipCount(s, "SELECT count(*) FROM pg_namespace WHERE nspname='public'", 0)
			}
			// Recreate before any later body or ordinary role cleanup needs public.
			s.exec("CREATE SCHEMA public AUTHORIZATION " + pq.QuoteIdentifier(s.bootstrap))
			if s.backend.PG {
				s.exec("GRANT USAGE ON SCHEMA public TO PUBLIC")
			}
			s.exec("GRANT own_other TO own_source WITH INHERIT TRUE, SET FALSE")
			s.actor("own_source")
			ownershipFailure(s, "SELECT id FROM own_protected_control.sentinel", fixture.Diagnostic{Code: "42501", Message: "permission denied for table sentinel"})
			s.actor("")
			s.exec("REVOKE own_other FROM own_source")
			s.exec("DROP ROLE own_source")
		})
	})
}

func TestReferenceRoleOwnershipBootstrapProtection(t *testing.T) {
	for _, operation := range []string{"reassign", "restrict", "cascade"} {
		t.Run(operation, func(t *testing.T) {
			ownershipProviderCases(t, func(s *membershipScenario, database string) {
				defer ownershipProtectedRestore(s, database)
				// These are genuinely bootstrap-owned, unlike the ordinary PG role
				// named postgres retained in the existing positive-control definitions.
				s.exec("CREATE TABLE own_bootstrap_rows(id SERIAL,payload INT)")
				s.run(fixture.Exec("INSERT INTO own_bootstrap_rows(payload) VALUES(7)", 1))
				s.exec("CREATE SEQUENCE own_bootstrap_generator START WITH 40")
				ownershipSelect(s, "SELECT nextval('own_bootstrap_generator') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(40)}})
				ownershipSeed(s, "own_bootstrap_control", "own_other")
				s.exec("GRANT SELECT ON own_bootstrap_rows TO own_reader WITH GRANT OPTION")
				s.exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_actor")
				s.exec("CREATE SCHEMA own_protected_control AUTHORIZATION own_actor")
				s.exec("GRANT own_target TO own_actor WITH INHERIT FALSE, SET TRUE")
				s.exec("GRANT own_other TO own_middle WITH ADMIN TRUE, INHERIT TRUE, SET TRUE")
				localIDs := make(map[string]int64, 4)
				for _, name := range []string{"own_bootstrap_rows", "own_bootstrap_rows_id_seq", "own_bootstrap_generator", "own_bootstrap_control"} {
					var id int64
					if err := s.c.QueryRowContext(s.p.Ctx, "SELECT "+pq.QuoteLiteral(name)+"::regclass::oid::BIGINT").Scan(&id); err != nil || id <= 0 {
						s.t.Fatal("local control identity", name, id, err)
					}
					localIDs[name] = id
				}
				roleIDs := make(map[string]int64, len(ownershipRoles))
				for _, role := range ownershipRoles {
					var id int64
					if err := s.c.QueryRowContext(s.p.Ctx, "SELECT oid::BIGINT FROM pg_roles WHERE rolname=$1", role).Scan(&id); err != nil || id <= 0 {
						s.t.Fatal("ordinary control identity", role, id, err)
					}
					roleIDs[role] = id
				}
				var bootstrapID int64
				if err := s.c.QueryRowContext(s.p.Ctx, "SELECT oid::BIGINT FROM pg_roles WHERE rolname=$1", s.bootstrap).Scan(&bootstrapID); err != nil || bootstrapID <= 0 {
					s.t.Fatal("real bootstrap identity", bootstrapID, err)
				}
				q := "DROP OWNED BY " + pq.QuoteIdentifier(s.bootstrap) + " RESTRICT"
				message := fmt.Sprintf("cannot drop objects owned by role %s because they are required by the database system", s.bootstrap)
				if operation == "cascade" {
					q = "DROP OWNED BY " + pq.QuoteIdentifier(s.bootstrap) + " CASCADE"
				} else if operation == "reassign" {
					q = "REASSIGN OWNED BY " + pq.QuoteIdentifier(s.bootstrap) + " TO own_target"
					message = fmt.Sprintf("cannot reassign ownership of objects owned by role %s because they are required by the database system", s.bootstrap)
				}
				s.clearNotices()
				ownershipFailure(s, q, fixture.Diagnostic{Code: "2BP01", Message: message})
				ownershipNotices(s)
				ownershipSelect(s, "SELECT oid::BIGINT AS id FROM pg_roles WHERE rolname="+pq.QuoteLiteral(s.bootstrap), []string{"id"}, []string{"INT8"}, [][]any{{bootstrapID}})
				for _, role := range ownershipRoles {
					ownershipSelect(s, "SELECT oid::BIGINT AS id FROM pg_roles WHERE rolname="+pq.QuoteLiteral(role), []string{"id"}, []string{"INT8"}, [][]any{{roleIDs[role]}})
				}
				for _, name := range []string{"own_bootstrap_rows", "own_bootstrap_rows_id_seq", "own_bootstrap_generator", "own_bootstrap_control"} {
					ownershipSelect(s, "SELECT "+pq.QuoteLiteral(name)+"::regclass::oid::BIGINT AS id", []string{"id"}, []string{"INT8"}, [][]any{{localIDs[name]}})
				}
				ownershipOwner(s, "own_bootstrap_rows", s.bootstrap)
				ownershipOwner(s, "own_bootstrap_rows_id_seq", s.bootstrap)
				ownershipOwner(s, "own_bootstrap_generator", s.bootstrap)
				ownershipSelect(s, "SELECT id,payload FROM own_bootstrap_rows", []string{"id", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}})
				ownershipSelect(s, "SELECT nextval('own_bootstrap_rows_id_seq') AS n,nextval('own_bootstrap_generator') AS g", []string{"n", "g"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(41)}})
				ownershipOwner(s, "own_bootstrap_control", "own_other")
				ownershipRows(s, "own_bootstrap_control")
				ownershipDatabaseOwner(s, s.bootstrap)
				s.actor("own_target")
				ownershipFailure(s, "SET ROLE pg_database_owner", fixture.Diagnostic{Code: "42501", Message: `permission denied to set role "pg_database_owner"`})
				s.actor("")
				s.actor("own_actor")
				// Real CREATE ACL authorizes the schema-owner handler after refusal.
				s.exec("ALTER SCHEMA own_protected_control OWNER TO own_target")
				s.actor("own_reader")
				ownershipSelect(s, "SELECT payload FROM own_bootstrap_rows", []string{"payload"}, []string{"INT4"}, [][]any{{int64(7)}})
				s.exec("GRANT SELECT ON own_bootstrap_rows TO own_downstream")
				s.actor("own_downstream")
				ownershipSelect(s, "SELECT payload FROM own_bootstrap_rows", []string{"payload"}, []string{"INT4"}, [][]any{{int64(7)}})
				s.actor("own_middle")
				s.exec("SET ROLE own_other")
				ownershipRows(s, "own_bootstrap_control")
				s.exec("RESET ROLE")
				s.exec("GRANT own_other TO own_alt WITH INHERIT FALSE, SET TRUE")
				s.actor("own_alt")
				s.exec("SET ROLE own_other")
				s.exec("RESET ROLE")
				s.actor("")
				s.flags("own_other", "own_middle", s.bootstrap, 1, true, true, true)
			})
		})
	}
}

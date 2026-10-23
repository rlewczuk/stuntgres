package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeLifecyclePermissions(t *testing.T) {
	for _, create := range []string{"CREATE TYPE tl_src.t AS ENUM ('z')", "CREATE DOMAIN tl_src.t AS integer", "CREATE TYPE tl_src.t AS(n integer)"} {
		t.Run(create, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"tl_schema_owner", "tl_owner", "tl_successor", "tl_actor", "tl_foreign"})
			typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				domainIdentityDDL(t, p, c, r, "CREATE ROLE tl_schema_owner", "CREATE ROLE tl_owner", "CREATE ROLE tl_successor", "CREATE ROLE tl_actor", "CREATE ROLE tl_foreign", "CREATE SCHEMA tl_src AUTHORIZATION tl_schema_owner", "CREATE SCHEMA tl_dst AUTHORIZATION tl_schema_owner", "GRANT USAGE ON SCHEMA tl_src,tl_dst TO PUBLIC", "GRANT CREATE ON SCHEMA tl_src TO tl_owner", "SET ROLE tl_owner", create, "RESET ROLE", "REVOKE CREATE ON SCHEMA tl_src FROM tl_owner")
				oid := domainAlterOID(t, p, c, "SELECT 'tl_src.t'::regtype::oid")
				array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_src.t'::regtype::oid")
				q := "ALTER TYPE tl_src.t RENAME TO denied"
				held := typeLifecycleHeld(t, p, c, q)
				domainIdentityDDL(t, p, c, r, "SET ROLE tl_actor")
				want := fixture.ControlsReferenceDiagnostic("42501", "must be owner of type tl_src.t")
				s := fixture.ErrorDiagnostic(q, want)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, r, s)
				typeLifecycleFailure(t, p, c, "ALTER TYPE tl_src.t SET SCHEMA tl_dst", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema tl_dst"))
				// Same-owner no-op and missing-recipient precedence are deliberate.
				domainIdentityDDL(t, p, c, r, "ALTER TYPE tl_src.t OWNER TO tl_owner")
				typeLifecycleFailure(t, p, c, "ALTER TYPE tl_src.t OWNER TO tl_absent_role", fixture.ControlsReferenceDiagnostic("42704", `role "tl_absent_role" does not exist`))
				domainIdentityDDL(t, p, c, r, "RESET ROLE", "GRANT CREATE ON SCHEMA tl_dst TO tl_actor", "SET ROLE tl_actor")
				typeLifecycleFailure(t, p, c, "ALTER TYPE tl_src.t SET SCHEMA tl_dst", want)
				domainIdentityDDL(t, p, c, r, "RESET ROLE", "SET ROLE tl_owner")
				typeLifecycleFailure(t, p, c, "ALTER TYPE tl_src.t SET SCHEMA tl_src", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema tl_src"))
				typeLifecycleFailure(t, p, c, "ALTER TYPE tl_src.t OWNER TO tl_successor", fixture.ControlsReferenceDiagnostic("42501", `must be able to SET ROLE "tl_successor"`))
				domainIdentityDDL(t, p, c, r, "RESET ROLE", "GRANT tl_successor TO tl_owner WITH INHERIT FALSE,SET TRUE", "SET ROLE tl_owner")
				typeLifecycleFailure(t, p, c, "ALTER TYPE tl_src.t OWNER TO tl_successor", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema tl_src"))
				domainIdentityDDL(t, p, c, r, "RESET ROLE", "GRANT CREATE ON SCHEMA tl_dst TO tl_owner,tl_successor", "SET ROLE tl_owner", "ALTER TYPE tl_src.t RENAME TO renamed", "ALTER TYPE tl_src.renamed SET SCHEMA tl_dst", "GRANT USAGE ON TYPE tl_dst.renamed TO tl_actor", "ALTER TYPE tl_dst.renamed OWNER TO tl_successor", "RESET ROLE")
				domainIdentitySameOID(t, p, c, "SELECT 'tl_dst.renamed'::regtype::oid", oid)
				domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_dst.renamed'::regtype::oid", array)
				run(fixture.Select("SELECT pg_get_userbyid(typowner) AS owner FROM pg_type WHERE oid=$1::oid OR oid=$2::oid ORDER BY oid", []string{"owner"}, []string{"NAME"}, [][]any{{"tl_successor"}, {"tl_successor"}}, oid, array))
				// Full semantic grant bag, not ACL storage ordering.
				run(fixture.Select("SELECT pg_get_userbyid(a.grantor) AS grantor,pg_get_userbyid(a.grantee) AS grantee,a.privilege_type,a.is_grantable FROM pg_type t CROSS JOIN LATERAL aclexplode(t.typacl) a WHERE t.oid='tl_dst.renamed'::regtype::oid AND a.grantee<>0 ORDER BY grantee", []string{"grantor", "grantee", "privilege_type", "is_grantable"}, []string{"NAME", "NAME", "TEXT", "BOOL"}, [][]any{{"tl_successor", "tl_actor", "USAGE", false}, {"tl_successor", "tl_successor", "USAGE", false}}))
				domainIdentityDDL(t, p, c, r, "SET ROLE tl_schema_owner")
				typeLifecycleFailure(t, p, c, "ALTER TYPE tl_dst.renamed RENAME TO denied", fixture.ControlsReferenceDiagnostic("42501", "must be owner of type tl_dst.renamed"))
				*notices = nil
				domainIdentityDDL(t, p, c, r, "DROP TYPE tl_dst.renamed RESTRICT", "RESET ROLE")
				domainIdentityNotices(t, notices)
				typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_type WHERE oid=$1::oid OR oid=$2::oid", 0, oid, array)
				// Type batch later authorization failure must retain its first root.
				domainIdentityDDL(t, p, c, r, "GRANT CREATE ON SCHEMA tl_src TO tl_owner,tl_foreign", "SET ROLE tl_owner", "CREATE TYPE tl_src.first AS ENUM ()", "RESET ROLE", "SET ROLE tl_foreign", "CREATE TYPE tl_src.last AS ENUM ()", "RESET ROLE", "SET ROLE tl_owner")
				typeLifecycleFailure(t, p, c, "DROP TYPE IF EXISTS tl_src.first,tl_src.last CASCADE", fixture.ControlsReferenceDiagnostic("42501", "must be owner of type tl_src.last"))
				domainIdentityDDL(t, p, c, r, "RESET ROLE")
				typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_type WHERE typnamespace='tl_src'::regnamespace::oid AND typname IN ('first','_first','last','_last')", 4)
				// Supported modifying utility tags are rejected read-only before
				// missing-name/authorization lookup; Prepare itself remains quiet.
				for _, q := range []string{"ALTER TYPE tl_missing.t RENAME TO denied", "DROP TYPE tl_missing.t"} {
					tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
					if err != nil {
						t.Fatal(err)
					}
					verb := "ALTER TYPE"
					if q == "DROP TYPE tl_missing.t" {
						verb = "DROP TYPE"
					}
					s := fixture.Prepared(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+verb+" in a read-only transaction")))
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, tx, r, s)
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
				_ = b // the SQL body and authority checks are shared in both modes.
			})
		})
	}
}

package reference_test

import (
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCollationAuthorization(t *testing.T) {
	dsn := "reference/" + t.Name()
	names := []string{"coll_owner", "coll_other"}
	fixture.RoleReferenceCleanup(t, names)
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.SchemaReferenceCleanup(t, "coll_owner_schema")
		fixture.SchemaReferenceCleanup(t, "coll_other_schema")
		fixture.CollationExec(t, p, b, `CREATE USER coll_owner`, `CREATE USER coll_other`, `CREATE SCHEMA coll_owner_schema AUTHORIZATION coll_owner`, `CREATE SCHEMA coll_other_schema AUTHORIZATION coll_other`)
		if b.PG {
			fixture.CollationExec(t, p, b, `ALTER ROLE coll_owner PASSWORD 'role-core-fixture'`, `ALTER ROLE coll_other PASSWORD 'role-core-fixture'`)
			fixture.CollationExec(t, p, b, `GRANT USAGE ON SCHEMA public TO PUBLIC`)
		}
		owner := fixture.RoleScenarioLoginPool(t, p.NativeConnector.Driver().(*stuntgres.Driver), b.PG, dsn, "coll_owner")
		other := fixture.RoleScenarioLoginPool(t, p.NativeConnector.Driver().(*stuntgres.Driver), b.PG, dsn, "coll_other")
		ownerBackend := b
		ownerBackend.DB = owner
		otherBackend := b
		otherBackend.DB = other
		fixture.CollationExec(t, p, otherBackend, `CREATE COLLATION coll_other_schema.coll_other_identity FROM pg_catalog."C"`)
		otherOwner := fixture.CollationOwnerOID(t, b.DB, "coll_other_identity")
		for _, prepared := range []bool{false, true} {
			for _, nonContext := range []bool{false, true} {
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table pg_collation"}
				if !b.PG {
					want.Code, want.Message = "0A000", "catalog source mutation is unsupported"
				}
				step := fixture.ErrorDiagnostic(`UPDATE pg_catalog.pg_collation SET collversion=collversion WHERE false`, want)
				step.Prepared, step.NonContext, step.ErrorStage = prepared, nonContext, "execute"
				if prepared && !b.PG {
					step.ErrorStage = "prepare"
				}
				fixture.CollationStep(t, p, otherBackend, step)
			}
		}
		fixture.CollationExec(t, p, ownerBackend, `CREATE COLLATION coll_owner_schema.coll_owned FROM pg_catalog."C"`)
		fixture.CollationError(t, p, otherBackend, `CREATE COLLATION coll_owner_schema.coll_rejected FROM pg_catalog."C"`, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema coll_owner_schema"})
		// Nonowners can reach public objects without adding a collation GRANT surface.
		fixture.CollationExec(t, p, b, `CREATE COLLATION coll_public_owned FROM pg_catalog."C"`, `ALTER COLLATION coll_public_owned OWNER TO coll_owner`)
		ownedOID := fixture.CollationOID(t, b.DB, "coll_public_owned")
		for _, q := range []string{`ALTER COLLATION coll_public_owned RENAME TO coll_stolen`, `ALTER COLLATION coll_public_owned REFRESH VERSION`, `ALTER COLLATION coll_public_owned SET SCHEMA coll_other_schema`, `ALTER COLLATION coll_public_owned OWNER TO coll_other`, `DROP COLLATION coll_public_owned`} {
			fixture.CollationError(t, p, otherBackend, q, fixture.Diagnostic{Code: "42501", Message: "must be owner of collation coll_public_owned"})
		}
		fixture.CollationError(t, p, ownerBackend, `ALTER COLLATION coll_owner_schema.coll_owned SET SCHEMA coll_other_schema`, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema coll_other_schema"})
		fixture.CollationError(t, p, ownerBackend, `ALTER COLLATION coll_public_owned OWNER TO coll_other`, fixture.Diagnostic{Code: "42501", Message: `must be able to SET ROLE "coll_other"`})
		if fixture.CollationOID(t, b.DB, "coll_public_owned") != ownedOID {
			t.Fatal("failed authorization changed identity")
		}
		fixture.WithPublicSchemaOwner(t, p, b, "coll_other", func() {
			fixture.CollationExec(t, p, b, `ALTER COLLATION coll_public_owned OWNER TO coll_other`)
		})
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT collowner::bigint AS owner FROM pg_catalog.pg_collation WHERE collname='coll_public_owned'`, []string{"owner"}, []string{"INT8"}, [][]any{{otherOwner}}))
		fixture.CollationExec(t, p, otherBackend, `DROP COLLATION coll_other_schema.coll_other_identity`)
		fixture.CollationExec(t, p, b, `DROP SCHEMA coll_other_schema`)
		fixture.CollationError(t, p, b, `DROP ROLE coll_other`, fixture.Diagnostic{Code: "2BP01", Message: `role "coll_other" cannot be dropped because some objects depend on it`, Detail: "owner of collation coll_public_owned"})
		fixture.CollationExec(t, p, otherBackend, `ALTER COLLATION coll_public_owned REFRESH VERSION`, `DROP COLLATION coll_public_owned`)
		fixture.CollationExec(t, p, b, `DROP SCHEMA coll_owner_schema CASCADE`, `DROP ROLE coll_owner,coll_other`)
	})
}

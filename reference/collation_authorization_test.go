package reference_test

import (
	"stuntgres"
	"testing"
)

func TestReferenceCollationAuthorization(t *testing.T) {
	dsn := "reference/" + t.Name()
	names := []string{"coll_owner", "coll_other"}
	roleReferenceCleanup(t, names)
	collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		schemaReferenceCleanup(t, "coll_owner_schema")
		schemaReferenceCleanup(t, "coll_other_schema")
		collationExec(t, p, b, `CREATE USER coll_owner`, `CREATE USER coll_other`, `CREATE SCHEMA coll_owner_schema AUTHORIZATION coll_owner`, `CREATE SCHEMA coll_other_schema AUTHORIZATION coll_other`)
		if b.pg {
			collationExec(t, p, b, `ALTER ROLE coll_owner PASSWORD 'role-core-fixture'`, `ALTER ROLE coll_other PASSWORD 'role-core-fixture'`)
			collationExec(t, p, b, `GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC`)
		}
		owner := roleScenarioLoginPool(t, p.nativeConnector.Driver().(*stuntgres.Driver), b.pg, dsn, "coll_owner")
		other := roleScenarioLoginPool(t, p.nativeConnector.Driver().(*stuntgres.Driver), b.pg, dsn, "coll_other")
		ownerBackend := b
		ownerBackend.db = owner
		otherBackend := b
		otherBackend.db = other
		collationExec(t, p, otherBackend, `CREATE COLLATION coll_other_schema.coll_other_identity FROM pg_catalog."C"`)
		otherOwner := collationOwnerOID(t, b.db, "coll_other_identity")
		for _, prepared := range []bool{false, true} {
			for _, nonContext := range []bool{false, true} {
				want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table pg_collation"}
				if !b.pg {
					want.Code, want.Message = "0A000", "catalog source mutation is unsupported"
				}
				step := referenceErrorDiagnostic(`UPDATE pg_catalog.pg_collation SET collversion=collversion WHERE false`, want)
				step.prepared, step.nonContext, step.errorStage = prepared, nonContext, "execute"
				if prepared && !b.pg {
					step.errorStage = "prepare"
				}
				collationStep(t, p, otherBackend, step)
			}
		}
		collationExec(t, p, ownerBackend, `CREATE COLLATION coll_owner_schema.coll_owned FROM pg_catalog."C"`)
		collationError(t, p, otherBackend, `CREATE COLLATION coll_owner_schema.coll_rejected FROM pg_catalog."C"`, referenceDiagnostic{Code: "42501", Message: "permission denied for schema coll_owner_schema"})
		// Nonowners can reach public objects without adding a collation GRANT surface.
		collationExec(t, p, b, `CREATE COLLATION coll_public_owned FROM pg_catalog."C"`, `ALTER COLLATION coll_public_owned OWNER TO coll_owner`)
		ownedOID := collationOID(t, b.db, "coll_public_owned")
		for _, q := range []string{`ALTER COLLATION coll_public_owned RENAME TO coll_stolen`, `ALTER COLLATION coll_public_owned REFRESH VERSION`, `ALTER COLLATION coll_public_owned SET SCHEMA coll_other_schema`, `ALTER COLLATION coll_public_owned OWNER TO coll_other`, `DROP COLLATION coll_public_owned`} {
			collationError(t, p, otherBackend, q, referenceDiagnostic{Code: "42501", Message: "must be owner of collation coll_public_owned"})
		}
		collationError(t, p, ownerBackend, `ALTER COLLATION coll_owner_schema.coll_owned SET SCHEMA coll_other_schema`, referenceDiagnostic{Code: "42501", Message: "permission denied for schema coll_other_schema"})
		collationError(t, p, ownerBackend, `ALTER COLLATION coll_public_owned OWNER TO coll_other`, referenceDiagnostic{Code: "42501", Message: `must be able to SET ROLE "coll_other"`})
		if collationOID(t, b.db, "coll_public_owned") != ownedOID {
			t.Fatal("failed authorization changed identity")
		}
		collationExec(t, p, b, `ALTER COLLATION coll_public_owned OWNER TO coll_other`)
		collationRoutes(t, p, b, referenceSelect(`SELECT collowner::bigint AS owner FROM pg_catalog.pg_collation WHERE collname='coll_public_owned'`, []string{"owner"}, []string{"INT8"}, [][]any{{otherOwner}}))
		collationExec(t, p, otherBackend, `DROP COLLATION coll_other_schema.coll_other_identity`)
		collationExec(t, p, b, `DROP SCHEMA coll_other_schema`)
		collationError(t, p, b, `DROP ROLE coll_other`, referenceDiagnostic{Code: "2BP01", Message: `role "coll_other" cannot be dropped because some objects depend on it`, Detail: "owner of collation coll_public_owned"})
		collationExec(t, p, otherBackend, `ALTER COLLATION coll_public_owned REFRESH VERSION`, `DROP COLLATION coll_public_owned`)
		collationExec(t, p, b, `DROP SCHEMA coll_owner_schema CASCADE`, `DROP ROLE coll_owner,coll_other`)
	})
}

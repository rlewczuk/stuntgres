package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalTypesIdentityCollisions(t *testing.T) {
	for _, family := range []string{"DOMAIN", "TYPE"} {
		t.Run(family, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "tt_dest")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					create := "CREATE DOMAIN tt_source AS integer"
					if family == "TYPE" {
						create = "CREATE TYPE tt_source AS(v integer)"
					}
					for _, q := range []string{"CREATE SCHEMA tt_dest", create, "CREATE DOMAIN tt_taken AS integer", "CREATE DOMAIN tt_dest.tt_source AS integer", "CREATE TYPE _tt_free AS ENUM('occupied')"} {
						run(fixture.Exec(q, 0))
					}
					id := domainAlterOID(t, p, c, "SELECT 'tt_source'::regtype::oid")
					array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tt_source'::regtype")
					occupied := domainAlterOID(t, p, c, "SELECT '_tt_free'::regtype::oid")
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("SAVEPOINT identity", 0))
					run(fixture.ErrorDiagnostic("ALTER "+family+" tt_source RENAME TO tt_taken", fixture.ControlsReferenceDiagnostic("42710", `type "tt_taken" already exists`)))
					run(fixture.Exec("ROLLBACK TO identity", 0))
					domainIdentitySameOID(t, p, c, "SELECT 'tt_source'::regtype::oid", id)
					run(fixture.ErrorDiagnostic("ALTER "+family+" tt_source SET SCHEMA tt_dest", fixture.ControlsReferenceDiagnostic("42710", `type "tt_source" already exists in schema "tt_dest"`)))
					run(fixture.Exec("ROLLBACK TO identity", 0))
					domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tt_source'::regtype", array)
					run(fixture.Exec("ALTER "+family+" tt_source RENAME TO tt_free", 0))
					domainIdentitySameOID(t, p, c, "SELECT 'tt_free'::regtype::oid", id)
					domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tt_free'::regtype", array)
					domainIdentitySameOID(t, p, c, "SELECT '_tt_free'::regtype::oid", occupied)
					run(fixture.Exec("ROLLBACK", 0))
					domainIdentitySameOID(t, p, c, "SELECT 'tt_source'::regtype::oid", id)
				})
			}
		})
	}
}

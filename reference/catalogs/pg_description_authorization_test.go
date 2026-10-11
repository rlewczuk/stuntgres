package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDescriptionAuthorization(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pgd_private")
	fixture.RoleReferenceCleanup(t, []string{"pgd_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			pgDescriptionPreserveSharedRole(t, p, backend)
			// Newly created schemas grant no PUBLIC USAGE and tables grant no
			// PUBLIC SELECT; no unsupported REVOKE setup is needed.
			for _, q := range []string{"CREATE ROLE pgd_reader", "CREATE SCHEMA pgd_private", "CREATE TABLE pgd_private.pgd_secret(a INT)", "COMMENT ON TABLE pgd_private.pgd_secret IS 'public description of private object'", "COMMENT ON COLUMN pgd_private.pgd_secret.a IS 'private attribute description'", "COMMENT ON ROLE postgres IS 'shared reader description'"} {
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, 0))
			}
			oid := pgDescriptionRelationID(t, p, backend, "pgd_secret")
			roleOID := pgDescriptionSharedRoleID(t, p, backend)
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET ROLE")
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET ROLE pgd_reader", 0))
			denied := fixture.ErrorDiagnostic("SELECT * FROM pgd_private.pgd_secret", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema pgd_private"))
			denied.SelectRows = true
			denied.ErrorStage = "execute"
			// Source lookup positions are part of the existing query authority.
			denied.Diagnostic.Position = "15"
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, denied)
			for _, prepared := range []bool{false, true} {
				s := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", [][]any{{oid, int64(1259), int64(0), "public description of private object"}, {oid, int64(1259), int64(1), "private attribute description"}}, oid)
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.PgDescriptionExecutorRows(t, p, backend, conn, s)
				shared := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1260", [][]any{}, roleOID)
				if prepared {
					shared = fixture.Prepared(shared)
				}
				fixture.PgDescriptionExecutorRows(t, p, backend, conn, shared)
				inquiry := fixture.Select("SELECT shobj_description($1,'pg_authid')", []string{"shobj_description"}, []string{"TEXT"}, [][]any{{"shared reader description"}}, roleOID)
				if prepared {
					inquiry = fixture.Prepared(inquiry)
				}
				commentInquiryStep(t, p, backend, conn, inquiry)
			}
		})
	}
}

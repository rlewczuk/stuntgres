package reference_test

import (
	"context"
	"testing"
)

func TestReferencePGDescriptionAuthorization(t *testing.T) {
	schemaReferenceCleanup(t, "pgd_private")
	roleReferenceCleanup(t, []string{"pgd_reader"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			pgDescriptionPreserveSharedRole(t, p, backend)
			// Newly created schemas grant no PUBLIC USAGE and tables grant no
			// PUBLIC SELECT; no unsupported REVOKE setup is needed.
			for _, q := range []string{"CREATE ROLE pgd_reader", "CREATE SCHEMA pgd_private", "CREATE TABLE pgd_private.pgd_secret(a INT)", "COMMENT ON TABLE pgd_private.pgd_secret IS 'public description of private object'", "COMMENT ON COLUMN pgd_private.pgd_secret.a IS 'private attribute description'", "COMMENT ON ROLE postgres IS 'shared reader description'"} {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(q, 0))
			}
			oid := pgDescriptionRelationID(t, p, backend, "pgd_secret")
			roleOID := pgDescriptionSharedRoleID(t, p, backend)
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET ROLE")
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("SET ROLE pgd_reader", 0))
			denied := referenceErrorDiagnostic("SELECT * FROM pgd_private.pgd_secret", controlsReferenceDiagnostic("42501", "permission denied for schema pgd_private"))
			denied.selectRows = true
			denied.errorStage = "execute"
			// Source lookup positions are part of the existing query authority.
			denied.diagnostic.Position = "15"
			referenceRunStep(t, p.ctx, conn, backend.recorder, denied)
			for _, prepared := range []bool{false, true} {
				s := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", [][]any{{oid, int64(1259), int64(0), "public description of private object"}, {oid, int64(1259), int64(1), "private attribute description"}}, oid)
				if prepared {
					s = referencePrepared(s)
				}
				pgDescriptionExecutorRows(t, p, backend, conn, s)
				shared := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1260", [][]any{}, roleOID)
				if prepared {
					shared = referencePrepared(shared)
				}
				pgDescriptionExecutorRows(t, p, backend, conn, shared)
				inquiry := referenceSelect("SELECT shobj_description($1,'pg_authid')", []string{"shobj_description"}, []string{"TEXT"}, [][]any{{"shared reader description"}}, roleOID)
				if prepared {
					inquiry = referencePrepared(inquiry)
				}
				commentInquiryStep(t, p, backend, conn, inquiry)
			}
		})
	}
}

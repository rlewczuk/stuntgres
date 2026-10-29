package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func withPublicationNamespaceReader(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"publication_namespace_reader"})
	fixture.SchemaReferenceCleanup(t, "publication_namespace_unrelated")
	fixture.SchemaReferenceCleanup(t, "publication_namespace_tx_schema")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin, actor := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("CREATE ROLE publication_namespace_reader", 0))
			// System ACLs survive public-schema reset. Register repair before mutation;
			// statements close in body before these borrowers and role cleanup.
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if _, err := admin.ExecContext(ctx, "ROLLBACK"); err != nil {
					t.Error(err)
				}
				for _, q := range []string{"GRANT SELECT ON pg_catalog.pg_publication_namespace TO PUBLIC", "REVOKE SELECT ON pg_catalog.pg_publication_namespace FROM publication_namespace_reader"} {
					if _, err := admin.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
				if _, err := actor.ExecContext(ctx, "RESET ROLE"); err != nil {
					t.Error(err)
				}
			}()
			fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.Exec("SET ROLE publication_namespace_reader", 0))
			body(t, p, b, admin, actor)
		})
	}
}

func publicationNamespaceDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, prepared bool) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_publication_namespace"))
	s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func publicationNamespacePrivilegeLifecycle(t *testing.T, smoke bool) {
	withPublicationNamespaceReader(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin, actor *sql.Conn) {
		run := func(q string) {
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(q, 0))
		}
		if smoke {
			publicationNamespaceShape(t, p, b, admin)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, publicationNamespaceEmpty(b.PG, "SELECT * FROM pg_catalog.pg_publication_namespace"))
		}
		steps := []fixture.Step{publicationNamespaceEmpty(b.PG, "SELECT * FROM pg_catalog.pg_publication_namespace")}
		if !smoke {
			steps = append(steps,
				fixture.Select("SELECT pnnspid FROM pg_catalog.pg_publication_namespace", []string{"pnnspid"}, []string{"OID"}, nil),
				publicationNamespaceEmpty(b.PG, "SELECT * FROM pg_catalog.pg_publication_namespace WHERE FALSE"),
				publicationNamespaceEmpty(b.PG, "SELECT * FROM pg_catalog.pg_publication_namespace LIMIT 0"),
				fixture.Select("SELECT 1 FROM pg_catalog.pg_publication_namespace", []string{"?column?"}, []string{"INT4"}, nil))
		}
		steps = append(steps, publicationNamespaceCount())
		held := make([]fixture.HeldStatement, len(steps))
		for i, s := range steps {
			stmt, err := actor.PrepareContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held[i] = fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: s.Query}
		}
		check := func(allowed, public, reader bool) {
			t.Helper()
			publicationNamespaceStoredACL(t, p, b, admin, public, reader)
			rights := fixture.Select("SELECT has_table_privilege('publication_namespace_reader','pg_catalog.pg_publication_namespace','SELECT') AS can_read", []string{"can_read"}, []string{"BOOL"}, [][]any{{allowed}})
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, rights)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Prepared(rights))
			for i, s := range steps {
				for _, e := range []fixture.Executor{actor, held[i]} {
					if allowed {
						fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					} else {
						publicationNamespaceDenied(t, p, b, e, s.Query, false)
					}
				}
				if allowed {
					fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.Prepared(s))
				} else {
					publicationNamespaceDenied(t, p, b, actor, s.Query, true)
				}
			}
			// Privileged owner/superuser remains admitted independently of reader ACL.
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, publicationNamespaceEmpty(b.PG, "SELECT * FROM pg_catalog.pg_publication_namespace"))
		}
		check(true, true, false)
		if smoke {
			run("CREATE TABLE publication_namespace_unrelated(id integer)")
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("INSERT INTO publication_namespace_unrelated VALUES(7)", 1))
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Select("SELECT id FROM publication_namespace_unrelated", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			check(true, true, false)
		}
		run("REVOKE SELECT ON pg_catalog.pg_publication_namespace FROM PUBLIC")
		check(false, false, false)
		run("GRANT SELECT ON pg_catalog.pg_publication_namespace TO publication_namespace_reader")
		check(true, false, true)
		run("REVOKE SELECT ON pg_catalog.pg_publication_namespace FROM publication_namespace_reader")
		check(false, false, false)
		run("GRANT SELECT ON pg_catalog.pg_publication_namespace TO PUBLIC")
		check(true, true, false)
	})
}

func TestReferencePGPublicationNamespacePrivileges(t *testing.T) {
	publicationNamespacePrivilegeLifecycle(t, false)
}

func TestReferencePGPublicationNamespaceFeatureSmoke(t *testing.T) {
	publicationNamespacePrivilegeLifecycle(t, true)
}

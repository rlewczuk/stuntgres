package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func withPublicationReader(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"publication_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin, actor := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("CREATE ROLE publication_reader", 0))
			// System ACLs survive public-schema reset. Register repair before mutation;
			// statements close in body before these borrowers and role cleanup.
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for _, q := range []string{"GRANT SELECT ON pg_catalog.pg_publication TO PUBLIC", "REVOKE SELECT ON pg_catalog.pg_publication FROM publication_reader"} {
					if _, err := admin.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
				if _, err := actor.ExecContext(ctx, "RESET ROLE"); err != nil {
					t.Error(err)
				}
			}()
			fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.Exec("SET ROLE publication_reader", 0))
			body(t, p, b, admin, actor)
		})
	}
}

func publicationDenied(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, prepared bool) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_publication"))
	s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func publicationPrivilegeLifecycle(t *testing.T, smoke bool) {
	withPublicationReader(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin, actor *sql.Conn) {
		run := func(q string) {
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(q, 0))
		}
		if smoke {
			publicationShape(t, p, b, admin)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, publicationEmpty(b.PG, "SELECT * FROM pg_catalog.pg_publication"))
		}
		steps := []fixture.Step{publicationEmpty(b.PG, "SELECT * FROM pg_catalog.pg_publication")}
		if !smoke {
			steps = append(steps,
				fixture.Select("SELECT pubgencols FROM pg_catalog.pg_publication", []string{"pubgencols"}, []string{"CHAR"}, nil),
				publicationEmpty(b.PG, "SELECT * FROM pg_catalog.pg_publication WHERE FALSE"),
				publicationEmpty(b.PG, "SELECT * FROM pg_catalog.pg_publication LIMIT 0"),
				fixture.Select("SELECT 1 FROM pg_catalog.pg_publication", []string{"?column?"}, []string{"INT4"}, nil))
		}
		steps = append(steps, publicationCount())
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
			publicationStoredACL(t, p, b, admin, public, reader)
			rights := fixture.Select("SELECT has_table_privilege('publication_reader','pg_catalog.pg_publication','SELECT') AS can_read", []string{"can_read"}, []string{"BOOL"}, [][]any{{allowed}})
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, rights)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Prepared(rights))
			for i, s := range steps {
				for _, e := range []fixture.Executor{actor, held[i]} {
					if allowed {
						fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					} else {
						publicationDenied(t, p, b, e, s.Query, false)
					}
				}
				if allowed {
					fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.Prepared(s))
				} else {
					publicationDenied(t, p, b, actor, s.Query, true)
				}
			}
			// Privileged owner/superuser remains admitted independently of reader ACL.
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, publicationEmpty(b.PG, "SELECT * FROM pg_catalog.pg_publication"))
		}
		check(true, true, false)
		run("REVOKE SELECT ON pg_catalog.pg_publication FROM PUBLIC")
		check(false, false, false)
		run("GRANT SELECT ON pg_catalog.pg_publication TO publication_reader")
		check(true, false, true)
		run("REVOKE SELECT ON pg_catalog.pg_publication FROM publication_reader")
		check(false, false, false)
		run("GRANT SELECT ON pg_catalog.pg_publication TO PUBLIC")
		check(true, true, false)
	})
}

func TestReferencePGPublicationPrivileges(t *testing.T) {
	publicationPrivilegeLifecycle(t, false)
}

func TestReferencePGPublicationFeatureSmoke(t *testing.T) {
	publicationPrivilegeLifecycle(t, true)
}

package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

var statisticExtMutations = []string{
	"INSERT INTO pg_catalog.pg_statistic_ext(stxname) VALUES ('forbidden')",
	"UPDATE pg_catalog.pg_statistic_ext SET stxstattarget=1 WHERE FALSE",
	"DELETE FROM pg_catalog.pg_statistic_ext WHERE FALSE",
	"TRUNCATE pg_catalog.pg_statistic_ext",
}

func statisticExtError(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q, code, message string, prepared, selectRows bool) {
	t.Helper()
	s := fixture.ImplicitError(q, fixture.ControlsReferenceDiagnostic(code, message))
	s.Prepared, s.ErrorStage, s.SelectRows = prepared, "execute", selectRows
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}

func TestReferencePGStatisticExtPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"stx_acl_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin, reader := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(q string) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(q, 0)) }
			run("CREATE ROLE stx_acl_reader")
			if _, err := reader.ExecContext(p.Ctx, "SET ROLE stx_acl_reader"); err != nil {
				t.Fatal(err)
			}
			defer reader.ExecContext(context.Background(), "RESET ROLE")
			queries := []string{statisticExtQuery, "SELECT * FROM pg_catalog.pg_statistic_ext WHERE FALSE"}
			for _, query := range queries {
				args := []any(nil)
				if query == statisticExtQuery {
					args = []any{int64(0)}
				}
				stmt, err := reader.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{reader, stmt, query}
				statisticExtRead(t, p, b, held, query, args...)
				// Install repair before touching a shared system catalog ACL.
				func() {
					defer func() {
						if _, err := admin.ExecContext(context.Background(), "GRANT SELECT ON pg_catalog.pg_statistic_ext TO PUBLIC"); err != nil {
							t.Error(err)
						}
					}()
					run("REVOKE SELECT ON pg_catalog.pg_statistic_ext FROM PUBLIC")
					for _, e := range []fixture.Executor{reader, held} {
						s := fixture.ImplicitError(query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_statistic_ext"))
						s.SelectRows, s.ErrorStage, s.Args = true, "execute", args
						fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					}
				}()
				statisticExtRead(t, p, b, held, query, args...)
			}
		})
	}
}

func TestReferencePGStatisticExtMutationBoundary(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"stx_mutation_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			admin, reader := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("CREATE ROLE stx_mutation_reader", 0))
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("SET ROLE stx_mutation_reader", 0))
			defer reader.ExecContext(context.Background(), "RESET ROLE")
			statisticExtRead(t, p, b, reader, statisticExtQuery, int64(0))
			for _, q := range statisticExtMutations {
				message := "permission denied for table pg_statistic_ext"
				if q == statisticExtMutations[3] {
					message = `permission denied: "pg_statistic_ext" is a system catalog`
				}
				for _, prepared := range []bool{false, true} {
					statisticExtError(t, p, b, reader, q, "42501", message, prepared, false)
				}
			}
			statisticExtRead(t, p, b, reader, statisticExtQuery, int64(0))
			if !b.PG {
				// The real oracle must never receive privileged system-catalog DML.
				for _, q := range statisticExtMutations {
					for _, prepared := range []bool{false, true} {
						statisticExtError(t, p, b, p.Native, q, "0A000", "catalog source mutation is unsupported", prepared, false)
					}
				}
				statisticExtRead(t, p, b, p.Native, statisticExtQuery, int64(0))
			}
		})
	}
}

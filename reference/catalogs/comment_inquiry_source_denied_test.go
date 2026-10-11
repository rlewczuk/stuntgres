package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceCommentInquirySourceDeniedFunctionSchema(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"inquiry_denied"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			// The original DB methods own one physical authentication session.
			// The recorded companion keeps its separately pinned Conn phase.
			db.SetMaxOpenConns(1)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := db.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Error(err)
				}
			})
			run := func(s fixture.Step) {
				t.Helper()
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, db, backend.Recorder, s)
			}
			for _, q := range []string{"CREATE USER inquiry_denied", "CREATE SCHEMA inquiry_private", "SET SESSION AUTHORIZATION inquiry_denied"} {
				run(fixture.Exec(q, 0))
			}
			s := fixture.ImplicitError("SELECT inquiry_private.col_description(1,1)", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema inquiry_private", Position: "8"})
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			s.ErrorStage = "prepare"
			run(fixture.Prepared(s))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("DROP SCHEMA inquiry_private", 0))
			run(fixture.Exec("DROP ROLE inquiry_denied", 0))
		})
	}
}

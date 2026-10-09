package reference_test

import (
	"context"
	"testing"
	"time"
)

func TestReferenceCommentInquirySourceDeniedFunctionSchema(t *testing.T) {
	roleReferenceCleanup(t, []string{"inquiry_denied"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
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
			run := func(s referenceStep) {
				t.Helper()
				s.nonContext = true
				referenceRunStep(t, p.ctx, db, backend.recorder, s)
			}
			for _, q := range []string{"CREATE USER inquiry_denied", "CREATE SCHEMA inquiry_private", "SET SESSION AUTHORIZATION inquiry_denied"} {
				run(referenceExec(q, 0))
			}
			s := referenceImplicitError("SELECT inquiry_private.col_description(1,1)", referenceDiagnostic{Code: "42501", Message: "permission denied for schema inquiry_private", Position: "8"})
			s.selectRows = true
			s.errorStage = "execute"
			run(s)
			s.errorStage = "prepare"
			run(referencePrepared(s))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("DROP SCHEMA inquiry_private", 0))
			run(referenceExec("DROP ROLE inquiry_denied", 0))
		})
	}
}

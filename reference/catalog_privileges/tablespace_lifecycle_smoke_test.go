package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTablespaceLifecycleFeatureSmoke(t *testing.T) {
	tablespaceLifecycleScenario(t, nil, func(t *testing.T, s *lifecycleCase) {
		for _, prepared := range []bool{false, true} {
			t.Run(lifecycleRoute(prepared), func(t *testing.T) {
				s.begin(t)
				defer s.rollback(t)
				s.exec(t, "CREATE ROLE ts_lifecycle_smoke_owner")
				step := fixture.Exec("ALTER TABLESPACE pg_default RENAME TO ts_lifecycle_smoke_default", 0)
				step.Prepared = prepared
				s.step(t, step)
				s.names(t, "ts_lifecycle_smoke_default", "pg_global")
				s.count(t, "SELECT count(*) AS n FROM pg_tablespace WHERE spcname='pg_default'", 0)
				step = fixture.Exec("ALTER TABLESPACE ts_lifecycle_smoke_default OWNER TO ts_lifecycle_smoke_owner", 0)
				step.Prepared = prepared
				s.step(t, step)
				s.owner(t, "ts_lifecycle_smoke_default", "ts_lifecycle_smoke_owner", "")
				s.error(t, "DROP TABLESPACE ts_lifecycle_smoke_default", tablespaceDiagnostic("25001", "DROP TABLESPACE cannot run inside a transaction block"), false)
				s.owner(t, "ts_lifecycle_smoke_default", "ts_lifecycle_smoke_owner", "")
			})
			s.names(t, "pg_default", "pg_global")
			*s.notices = nil
			step := fixture.Exec("DROP TABLESPACE IF EXISTS ts_lifecycle_smoke_absent", 0)
			step.Prepared = prepared
			s.step(t, step)
			s.noticesWant(t, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "tablespace \"ts_lifecycle_smoke_absent\" does not exist, skipping"})
			s.error(t, "DROP TABLESPACE pg_default", tablespaceDiagnostic("42501", "permission denied for tablespace pg_default"), false)
		}
		if s.b.PG {
			t.Run("pg-only-real-ordinary-blockers-and-empty-drop", func(t *testing.T) { lifecyclePhysicalBlockers(t, s, "smoke") })
		} else {
			t.Run("native-ordinary-capability-boundary", func(t *testing.T) { lifecycleNativeBoundary(t, s) })
			t.Run("native-committed-shared-controls-and-retained-rows", func(t *testing.T) { lifecycleNativeCommitted(t, s, true) })
		}
	})
}

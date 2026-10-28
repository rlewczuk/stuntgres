package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/stored"
)

func TestTriggerModeStatementAdmission(t *testing.T) {
	for _, tc := range []struct{ from, to, skipped string }{
		{"origin", "replica", "origin"}, {"replica", "origin", "replica"},
	} {
		t.Run(tc.from+"-to-"+tc.to, func(t *testing.T) {
			e, scope, read, relation, procedure := triggerUnitFixture(t,
				"BEGIN IF TG_NAME='"+tc.skipped+"' THEN PERFORM 1/0; END IF; RETURN NULL; END")
			parts := triggerUnitDefinition(t, read, relation, procedure, "origin", stored.TriggerInsert).Parts()
			parts.Timing, parts.Level = stored.TriggerAfter, stored.TriggerStatement
			origin, err := stored.NewTrigger(parts)
			if err != nil { t.Fatal(err) }
			parts.Name, parts.OID, parts.Mode = "replica", parts.OID+1, catalog.TriggerReplica
			replica, err := stored.NewTrigger(parts)
			if err != nil { t.Fatal(err) }
			relation = triggerUnitInstall(t, e, relation, origin, replica)
			triggerUnitAdmit(t, scope, relation)
			ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
			setRole := func(role string) {
				t.Helper()
				if _, err := scope.Execute(enginetest.Statement(t, "SET session_replication_role="+role), 0, nil); err != nil { t.Fatal(err) }
			}
			setRole(tc.from)
			if err := scope.WriteHost().AfterStatement(ref, stored.TriggerInsert, nil); err != nil { t.Fatal(err) }
			if frame := core.ScopeAfterTestFrames(scope)[0]; frame.Statements != 2 || frame.Qualified != 0 {
				t.Fatal("producer registration selected mode before completion", frame)
			}
			setRole(tc.to)
			if err := scope.WriteHost().CompleteAfterStatement(ref, stored.TriggerInsert); err != nil { t.Fatal(err) }
			if frame := core.ScopeAfterTestFrames(scope)[0]; frame.Qualified != 2 || frame.Skipped != 1 || frame.StatementCursor != 0 {
				t.Fatal("completion failed to retain event eligibility", frame)
			}
			setRole(tc.from)
			if err := core.ScopeDrainAfterTest(scope); err != nil { t.Fatal("queued eligibility was selected at the wrong boundary", err) }
			if frame := core.ScopeAfterTestFrames(scope)[0]; frame.StatementCursor != 2 || frame.Skipped != 1 {
				t.Fatal("drain changed the qualified events", frame)
			}
		})
	}
}

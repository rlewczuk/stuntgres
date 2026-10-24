package engine

import (
	"testing"

	"stuntgres/internal/engine/pgcatalog"
)

// The two sources deliberately share the same authority and transport fixture;
// source identity and PUBLIC-origin visibility are the only mode differences.
func newRoutinePrivilegesFixture(t *testing.T) roleRoutineGrantsFixture {
	t.Helper()
	f := newRoleRoutineGrantsFixture(t)
	f.sourceOID = pgcatalog.InfoRoutinePrivilegesOID
	return f
}

func TestInfoRoutinePrivilegesMetadata(t *testing.T) {
	testRoutinePrivilegesMetadata(t, newRoutinePrivilegesFixture(t), 13376, 13375)
}

func TestInfoRoutinePrivilegesCapture(t *testing.T) {
	testRoutinePrivilegesCapture(t, newRoutinePrivilegesFixture(t))
}

func TestInfoRoutinePrivilegesCaptureFrontier(t *testing.T) {
	testRoutinePrivilegesCaptureFrontier(t, newRoutinePrivilegesFixture(t))
}

package enginetest

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func BtreeKeys(t testing.TB, root *storage.IndexRoot) []string {
	t.Helper()
	keys := make([]string, len(root.Entries))
	identities := make(map[ids.VersionID]bool)
	for i, entry := range root.Entries {
		if entry.Row == 0 || entry.Version == 0 || identities[entry.Version] {
			t.Fatal("collapsed/invalid identity", entry)
		}
		identities[entry.Version] = true
		value := entry.Keys[0]
		if value.IsNull() {
			keys[i] = "NULL"
		} else {
			var err error
			keys[i], err = value.Type().Output(context.Background(), value)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return keys
}

func DeleteCount(t testing.TB, s *session.Session, query string, want int64) {
	t.Helper()
	result := SessionSQL(t, s, query)
	if result.Count != want || result.Rows != nil {
		t.Fatalf("%s: count=%d rows=%v, want %d/no relation", query, result.Count, result.Rows, want)
	}
}

// Unlike the generic internal SQL fixture this goes through command admission.
// SQL results are reference-tested; these tests inspect retained runtime owners.
func PlannerCostCommand(t testing.TB, s *session.Session, q string) core.Result {
	t.Helper()
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	r, err := core.EngineOf(s).Execute(op, Statement(t, q), 0, nil)
	if err != nil {
		t.Fatal(q, err)
	}
	if r.Rows != nil {
		t.Cleanup(func() { r.Rows.Close() })
	}
	return r
}

// Follow the existing queryJoinsFixture capture seam, but on the actual hash
// owner's engine. No public adapter or testing-only production switch is used.
func HashCaptureQuery(t testing.TB, e *core.Engine, op *session.Operation, q string) (*plan.Query, *exec.Source, func()) {
	t.Helper()
	ast := Statement(t, q)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var bound analyze.Binding
	if err == nil {
		bound, err = core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	}
	var source *exec.Source
	if err == nil {
		// Match executeBoundCommandLocked: retained sources borrow the real
		// bound routine authority, not a statement scope with an empty view.
		scope.RoutineAuth, scope.RoutineDependencies = auth, bound.Dependencies
		// Logical-stage fixtures supply cancellation contexts directly, but keep
		// the actual admission alive until every retained stage is closed.
		source, err = e.CaptureQuerySourcesForInvokerLocked(scope, bound.QuerySources, false, authorization.PrincipalRef{}, plan.RowMark{}, scope.QueryLockContext)
	}
	e.Mu.Unlock()
	if err == nil {
		err = exec.SelectQuerySources(op.Ctx, source)
	}
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		source.Close()
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(release)
	return bound.Query, source, release
}

func UniqueClaimsEmpty(t testing.TB, e *core.Engine) {
	t.Helper()
	LockEmpty(t, e)
	UniqueKeyBucketsEmpty(t, e)
}

func UniqueKeyBucketsEmpty(t testing.TB, e *core.Engine) {
	t.Helper()
	lockState := e.Locks.Inspect()
	if lockState.KeyBuckets != 0 {
		t.Fatal("retained unique key buckets", lockState.KeyBuckets)
	}
}

package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryLockContextsAndReferenceKinds(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(id INT)")
	enginetest.RoleExecute(t, e, op, "CREATE SEQUENCE s")
	enginetest.RoleExecute(t, e, op, "CREATE VIEW identity_view AS SELECT tableoid FROM t")
	for _, tc := range []struct {
		q                               string
		marked, identity, sequenceError bool
	}{
		{"SELECT id FROM t", false, false, false},
		{"SELECT tableoid FROM t", false, true, false},
		{"SELECT tableoid FROM t FOR UPDATE", true, true, false},
		{"SELECT tableoid FROM identity_view", false, true, false},
		{"SELECT id FROM t FOR UPDATE OF t SKIP LOCKED", true, false, false},
		{"SELECT x.id FROM (VALUES(1)) x(id) FOR SHARE OF x", true, false, false},
		{"SELECT t.id FROM t CROSS JOIN s", false, false, false},
		{"SELECT t.id FROM t CROSS JOIN s FOR UPDATE OF t", true, false, true},
	} {
		t.Run(tc.q, func(t *testing.T) {
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := scope.Finish(nil); err != nil {
					t.Error(err)
				}
			})
			raw := enginetest.Statement(t, tc.q).(*parser.Select)
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			binder := core.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
			binder.Query.Auth = &auth
			var query *plan.Query
			if err == nil {
				query, err = binder.BindSelect(raw, false)
			}
			if err == nil {
				err = core.EngineInitializeQueryLocksLocked(e, scope, query.Root, binder.Query.Sources)
			}
			e.Mu.Unlock()
			if tc.sequenceError {
				enginetest.WaitDiagnostic(t, err, "42809", `cannot lock rows in sequence "s"`)
			} else {
				if err != nil || (scope.QueryLockContext != nil) != (tc.marked || tc.identity) {
					t.Fatal(err, scope.QueryLockContext, tc.marked, tc.identity)
				}
				if scope.QueryLockContext != nil && scope.QueryLockContext.IdentityOnly != !tc.marked {
					t.Fatal("identity retention changed rowmark authority", scope.QueryLockContext)
				}
			}
			lockState := e.Locks.Inspect()
			grants := len(lockState.Resources)
			if grants != 0 {
				t.Fatal("kind/context preflight acquired physical resources", grants)
			}
		})
	}
	t.Run("mixed-identity-projection", func(t *testing.T) {
		// Use the actual admission, capture and projection chain that an
		// unmarked tableoid query reaches. Do not supply an identity context
		// or a reconstructed materialized occurrence manifest to the executor.
		wrapper := &fdwUnitWrapper{paths: []catalog.ForeignPath{{Attributes: []int32{1}}}, scan: &fdwUnitScan{position: 3}}
		definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "identity_handler"}, Wrapper: wrapper})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition}, nil, fdwIntegerImport, fdwIntegerExport); err != nil {
			t.Fatal(err)
		}
		for _, command := range []string{
			"CREATE FOREIGN DATA WRAPPER identity_wrapper HANDLER identity_handler",
			"CREATE SERVER identity_server FOREIGN DATA WRAPPER identity_wrapper",
			"CREATE USER MAPPING FOR CURRENT_USER SERVER identity_server",
			"CREATE FOREIGN TABLE identity_child() INHERITS(t) SERVER identity_server",
			"INSERT INTO t VALUES(7)",
		} {
			enginetest.RoleExecute(t, e, op, command)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := scope.Finish(nil); err != nil {
				t.Error(err)
			}
		}()
		raw := enginetest.Statement(t, "SELECT tableoid FROM t").(*parser.Select)
		if err := scope.Gate(raw); err != nil {
			t.Fatal(err)
		}
		if err := scope.AcquireRelation(raw, true); err != nil {
			t.Fatal(err)
		}
		if _, err := scope.AcquireViewSources(false); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		binder := core.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
		binder.Query.Auth = &auth
		var query *plan.Query
		var source *exec.Source
		if err == nil {
			query, err = binder.BindSelect(raw, false)
		}
		if err == nil {
			err = core.EngineInitializeQueryLocksLocked(e, scope, query.Root, binder.Query.Sources)
		}
		if err == nil {
			source, err = e.CaptureQuerySourcesLocked(scope, binder.Query.Sources, false)
		}
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		defer source.Close()
		defer func() {
			if err := exec.FinishForeignSources(op.Context(), source); err != nil {
				t.Error(err)
			}
		}()
		if err := exec.SelectQuerySources(op.Context(), source); err != nil {
			t.Fatal(err)
		}
		state := &exec.State{Source: source}
		stage, err := exec.OpenQueryProjectStage(op.Context(), state, query.Root.(*plan.ProjectNode))
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close()
		row := make([]sqltype.Value, 1)
		present, err := stage.Next(op.Context(), row)
		if err != nil || !present {
			t.Fatal("admitted mixed tableoid projection", present, err)
		}
		oid, err := row[0].OID()
		if err != nil || oid != uint32(source.Ref.Address.ObjectID) {
			t.Fatal("projection lost actual parent physical member", row, err)
		}
		if present, err := stage.Next(op.Context(), row); err != nil || present {
			t.Fatal("empty foreign sibling changed multiplicity", present, err)
		}
		if source.Locks == nil || !source.Locks.IdentityOnly || len(source.Locks.Leaves) != 2 || len(source.Occurrences) != 2 || len(source.Provenance) != 2 || source.Provenance[0] != 0 || source.Provenance[1] != exec.AbsentQueryIndex {
			t.Fatal("actual identity capture lost alternative lineage", source.Locks, source.Occurrences, source.Provenance)
		}
		for resource := range e.Locks.Inspect().Resources {
			if resource.Row != 0 {
				t.Fatal("identity projection acquired a tuple lock", resource)
			}
		}
	})
}

package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Exercise the real domain programme map, actual stored recipe/sequence, and
// the same validated child-owner projection used by RI. Neither principal is a
// raw role OID or a mocked permission result. Both initialization orders matter.
func TestDomainProgramInvokerAndRIOwnerPhases(t *testing.T) {
	for _, firstRI := range []bool{false, true} {
		t.Run(map[bool]string{false: "invoker_first", true: "ri_owner_first"}[firstRI], func(t *testing.T) {
			e := New()
			admin, finish := roleOperation(t, e, "postgres")
			defer finish()
			for _, sql := range []string{
				"CREATE ROLE phase_caller LOGIN",
				"CREATE ROLE phase_child_owner LOGIN",
				"CREATE SEQUENCE public.phase_steps START 1",
				"CREATE DOMAIN public.phase_checked AS integer CHECK(nextval('public.phase_steps'::regclass)>0 AND VALUE>0)",
				"CREATE TABLE public.phase_child(v public.phase_checked)",
				"ALTER TABLE public.phase_child OWNER TO phase_child_owner",
				"GRANT USAGE ON SCHEMA public TO phase_caller,phase_child_owner",
				"GRANT USAGE ON DOMAIN public.phase_checked TO phase_caller,phase_child_owner",
				"GRANT USAGE ON SEQUENCE public.phase_steps TO phase_caller,phase_child_owner",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.int4gt(integer,integer) FROM PUBLIC",
			} {
				roleExecute(t, e, admin, sql)
			}
			allowed := "phase_caller"
			if firstRI {
				allowed = "phase_child_owner"
			}
			roleExecute(t, e, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.int4gt(integer,integer) TO "+allowed)
			caller, callerDone := roleOperation(t, e, "phase_caller")
			defer callerDone()
			scope, err := e.StartStatement(caller)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			result, err := scope.Execute(statement(t, "SELECT 1"), 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.Rows != nil {
				result.Rows.Close()
			}
			e.mu.Lock()
			auth, err := e.authorizationLocked(caller)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			view := auth.catalog
			childRef := view.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "phase_child"})
			child := view.MustRelation(childRef[0])
			ownerAuth := foreignActionOwner(auth, child.Owner)
			e.mu.Unlock()
			domain := domainUnitType(t, view, "phase_checked")
			value, err := sqltype.Integer(domain.SQLType.DomainBase().Type, 1)
			if err != nil {
				t.Fatal(err)
			}
			invoker := EvalContext{Context: context.Background(), statementScope: scope, catalog: view, Input: scope.input, Output: scope.output}
			local := invoker
			local.routineAuthority = &ownerAuth
			check := func(evaluation *EvalContext) error {
				// Use the same command-local owner phase slot as real RI
				// planning/execution, then restore the original caller context.
				if evaluation == &local {
					scope.foreignActionAuth = &ownerAuth
					defer func() { scope.foreignActionAuth = nil }()
				}
				return evaluation.CheckDomain(context.Background(), domain.SQLType, value)
			}
			first, second := &invoker, &local
			if firstRI {
				first, second = second, first
			}
			if err := check(first); err != nil {
				t.Fatal("authorized genuine domain truth", err)
			}
			err = check(second)
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function int4gt"})
			sequence := catalogRef(t, e, view, "phase_steps")
			e.mu.Lock()
			last := e.storage.Sequences[sequence.Address].Last
			e.mu.Unlock()
			if last != 1 {
				t.Fatal("phase admission replayed CHECK truth", last)
			}
			if scope.routineAuth.state.CurrentUser != auth.state.CurrentUser {
				t.Fatal("local RI phase replaced original invoker")
			}
			// Publication re-admits the actual successfully consumed actor, not
			// whichever actor happened to touch the shared map most recently.
			roleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.int4gt(integer,integer) FROM "+allowed)
			e.mu.Lock()
			current, captureErr := e.authorizationLocked(caller)
			e.mu.Unlock()
			if captureErr != nil {
				t.Fatal(captureErr)
			}
			nullCountUnitDiagnostic(t, scope.readmitPreparedPrograms(current), sqlstate.Error{Code: "42501", Message: "permission denied for function int4gt"})
			e.mu.Lock()
			last = e.storage.Sequences[sequence.Address].Last
			e.mu.Unlock()
			if last != 1 {
				t.Fatal("publication repeated volatile CHECK", last)
			}
		})
	}
}

// First body entry is a real internal admission boundary. A programme consumed
// by the invoker must not certify the actual RI owner (or the reverse), even
// when both occurrences select the same immutable outer descriptor and recipe.
func TestRoutineInquiryBodyInvokerAndRIOwnerPhases(t *testing.T) {
	for _, firstRI := range []bool{false, true} {
		t.Run(map[bool]string{false: "invoker_first", true: "ri_owner_first"}[firstRI], func(t *testing.T) {
			e := New()
			admin, finish := roleOperation(t, e, "postgres")
			defer finish()
			for _, sql := range []string{
				"CREATE ROLE inquiry_phase_caller LOGIN",
				"CREATE ROLE inquiry_phase_owner LOGIN",
				"CREATE TABLE inquiry_phase_child(v integer)",
				"ALTER TABLE inquiry_phase_child OWNER TO inquiry_phase_owner",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.oideq(oid,oid) FROM PUBLIC",
			} {
				roleExecute(t, e, admin, sql)
			}
			allowed := "inquiry_phase_caller"
			if firstRI {
				allowed = "inquiry_phase_owner"
			}
			roleExecute(t, e, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.oideq(oid,oid) TO "+allowed)
			caller, callerDone := roleOperation(t, e, "inquiry_phase_caller")
			defer callerDone()
			scope, err := e.StartStatement(caller)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			result, err := scope.Execute(statement(t, "SELECT 1"), 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.Rows != nil {
				result.Rows.Close()
			}
			raw := statement(t, "SELECT obj_description(1::oid,'pg_class')").(*parser.Select).Targets[0]
			e.mu.Lock()
			auth, err := e.authorizationLocked(caller)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			view := auth.catalog
			childRef := view.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "inquiry_phase_child"})
			child := view.MustRelation(childRef[0])
			ownerAuth := foreignActionOwner(auth, child.Owner)
			binder := newExpressionBinder(context.Background(), e, scope.input, caller, nil)
			binder.queryAuth, binder.queryNamespace = &auth, &queryNamespace{}
			node, err := binder.bind(raw, expressionPolicy{context: projectionContext})
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			inquiry, ok := node.(commentInquiryExpression)
			if !ok {
				t.Fatalf("actual inquiry provider %T", node)
			}
			invoker := EvalContext{Context: context.Background(), statementScope: scope, catalog: view, Input: scope.input, Output: scope.output, routineRecipe: true, routineChecks: binder.dependencies}
			local := invoker
			local.routineAuthority = &ownerAuth
			enter := func(evaluation *EvalContext) error {
				if evaluation == &local {
					scope.foreignActionAuth = &ownerAuth
					defer func() { scope.foreignActionAuth = nil }()
				}
				if err := admitRoutineCall(evaluation, node, true, false); err != nil {
					return err
				}
				return admitInquiryBody(evaluation, inquiry)
			}
			first, second := &invoker, &local
			if firstRI {
				first, second = second, first
			}
			if err := enter(first); err != nil {
				t.Fatal("authorized first actual body entry", err)
			}
			nullCountUnitDiagnostic(t, enter(second), sqlstate.Error{Code: "42501", Message: "permission denied for function oideq", Context: `SQL function "obj_description" statement 1`})
			if scope.routineAuth.state.CurrentUser != auth.state.CurrentUser {
				t.Fatal("lazy local owner changed statement invoker")
			}
			roleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.oideq(oid,oid) FROM "+allowed)
			e.mu.Lock()
			current, captureErr := e.authorizationLocked(caller)
			e.mu.Unlock()
			if captureErr != nil {
				t.Fatal(captureErr)
			}
			nullCountUnitDiagnostic(t, scope.readmitPreparedPrograms(current), sqlstate.Error{Code: "42501", Message: "permission denied for function oideq", Context: `SQL function "obj_description" statement 1`})
			// Both are now denied. The actually consumed outer call must win
			// with its original caller diagnostic, not the body's SQL Context.
			roleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.obj_description(oid,name) FROM PUBLIC")
			e.mu.Lock()
			current, captureErr = e.authorizationLocked(caller)
			e.mu.Unlock()
			if captureErr != nil {
				t.Fatal(captureErr)
			}
			nullCountUnitDiagnostic(t, scope.readmitPreparedPrograms(current), sqlstate.Error{Code: "42501", Message: "permission denied for function obj_description"})
		})
	}
}

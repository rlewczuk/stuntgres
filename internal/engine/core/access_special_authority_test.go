package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestAccessSpecialRuntimeAuthority(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	other, _, _ := strictNullUnitFixture(t)
	for _, tc := range []struct {
		text string
		oid  ids.OID
	}{{"has_largeobject_privilege(1::oid,'SELECT')", 6349}, {"has_parameter_privilege('lc_messages','SET')", 6207}} {
		node, b, evaluation := strictNullUnitBind(t, e, op, scope, tc.text)
		call, ok := node.(functions.AccessInquiry)
		if !ok {
			t.Fatal("not typed inquiry", node)
		}
		original := append([]catalog.ExpressionDependency(nil), b.Dependencies...)
		descriptor, err := exec.ExpressionRoutineDescriptor(call)
		if err != nil || descriptor == nil || descriptor.Address().ObjectID != tc.oid || descriptor.Revision() == 0 || descriptor.Builtin == nil {
			t.Fatal("original descriptor", descriptor, err)
		}
		if err := exec.AdmitRoutineCall(&evaluation, call, false, false); err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"missing", "foreign", "epoch", "revision"} {
			probe := evaluation
			checks := append([]catalog.ExpressionDependency(nil), original...)
			if mode == "missing" {
				checks = nil
			} else {
				for i := range checks {
					if checks[i].Ref.Address == descriptor.Address() {
						switch mode {
						case "foreign":
							checks[i].Ref = other.Catalog.Domain.Ref(checks[i].Ref.Epoch, checks[i].Ref.Address)
						case "epoch":
							checks[i].Ref.Epoch++
						case "revision":
							checks[i].Revision++
						}
					}
				}
			}
			probe.RoutineChecks = checks
			if err := exec.AdmitRoutineCall(&probe, call, false, false); err == nil {
				t.Fatal("original lifetime repaired", mode)
			}
		}
		before := call
		if _, err := call.Eval(&evaluation); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(call, before) || !reflect.DeepEqual(original, b.Dependencies) {
			t.Fatal("execution changed retained recipe or manifest")
		}
	}
	node, b, evaluation := strictNullUnitBind(t, e, op, scope, "has_parameter_privilege(4294967294::oid,'lc_messages','SET')")
	original := append([]catalog.ExpressionDependency(nil), b.Dependencies...)
	originalNode := node
	old := e.CatalogRead()
	candidate := session.NewJournal()
	scope.Catalog = candidate
	address := ids.ObjectAddress{ClassID: ids.ParameterClassID, ObjectID: 910293}
	for i, want := range []bool{true, false, true} {
		parameter := &catalog.Parameter{ObjectAddress: address, ObjectRevision: ids.DefinitionRevision(i + 2), ObjectName: "lc_messages", Owner: e.RoleStore.Domain.Principal(10), ACL: authorization.ACLValue{Explicit: true}}
		if want {
			parameter.ACL.Entries = []authorization.ACLEntry{{Grantor: parameter.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SetPrivilege}}
		}
		e.Mu.Lock()
		e.CatalogChangeLocked(candidate, address, parameter)
		e.Mu.Unlock()
		value, err := node.Eval(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.Boolean()
		if err != nil || got != want {
			t.Fatal("retained inquiry cached authority", i, got, want, err)
		}
		if old.Get(address) != nil || !reflect.DeepEqual(originalNode, node) || !reflect.DeepEqual(original, b.Dependencies) {
			t.Fatal("current grant changed original capture/recipe/manifest")
		}
	}
}

func TestAccessSpecialSnapshotAuthority(t *testing.T) {
	t.Run("first inquiry after argument activation", func(t *testing.T) {
		for _, tc := range []struct {
			priv   string
			exists bool
		}{{"SELECT", false}, {"UPDATE", true}, {"UPDATE WITH GRANT OPTION", false}, {"SELECT,UPDATE", true}, {"SELECT,UPDATE WITH GRANT OPTION", false}} {
			t.Run(tc.priv, func(t *testing.T) {
				e, op, scope := strictNullUnitFixture(t)
				_, _, evaluation := strictNullUnitBind(t, e, op, scope, "has_largeobject_privilege(1::oid,'SELECT')")
				// SELECT 1 in the fixture performed real query-source admission.
				// It owns the immutable catalog; no desired LO frontier is installed.
				old := scope.Inquiry.CommentCatalog
				if old.State() == nil || scope.RoutineAuth.Catalog != old || core.ScopeLargeObjectRead(scope).State() != nil {
					t.Fatal("fixture must own an admitted query catalog with an unset LO frontier")
				}
				host := (*core.StatementHost)(scope)
				restore := scope.ActivateSQLRoutine(nil, old)
				result, err := host.LargeObject(functions.LargeObjectRequest{Name: "lo_create", OID: 910292})
				if err != nil {
					_ = restore(err)
					t.Fatal(err)
				}
				if err := restore(nil); err != nil {
					t.Fatal(err)
				}
				address := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: result.OID}
				e.Mu.Lock()
				current := e.PrivateCatalogLocked(scope.PendingCatalog())
				e.Mu.Unlock()
				if core.ScopeLargeObjectRead(scope).State() != nil || scope.RoutineAuth.Catalog != old || old.Get(address) != nil || current.Get(address) == nil {
					t.Fatal("argument activation must restore unset outer frontier and retain candidate")
				}
				parameter, present, err := host.ObjectPrivilege(&evaluation, functions.AccessParameter, nil, functions.AccessRole{Name: "lc_messages"}, "SET")
				if err != nil || !present || !parameter {
					t.Fatal("intervening parameter inquiry", parameter, present, err)
				}
				if scope.RoutineAuth.Catalog.Get(address) == nil || scope.Inquiry.CommentCatalog != old || old.Get(address) != nil {
					t.Fatal("parameter inquiry must refresh current authority without changing query-owned catalog")
				}
				if core.ScopeLargeObjectRead(scope) != old {
					t.Fatal("current-authority consumer must preserve query frontier before first LO inquiry")
				}
				value, exists, err := host.ObjectPrivilege(&evaluation, functions.AccessLargeObject, nil, functions.AccessRole{ByOID: true, OID: uint32(result.OID)}, tc.priv)
				if err != nil || exists != tc.exists || value != tc.exists {
					t.Fatal(tc.priv, value, exists, err)
				}
				if core.ScopeLargeObjectRead(scope) != old {
					t.Fatal("first inquiry replaced actual active query frontier")
				}
			})
		}
	})
	e, op, scope := strictNullUnitFixture(t)
	_, _, evaluation := strictNullUnitBind(t, e, op, scope, "has_largeobject_privilege(1::oid,'SELECT')")
	address := ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 910291}
	old := e.CatalogRead()
	object := &catalog.LargeObject{ObjectAddress: address, ObjectName: "910291", ObjectRevision: 2, Owner: e.RoleStore.Domain.Principal(10)}
	candidate := session.NewJournal()
	e.Mu.Lock()
	e.CatalogChangeLocked(candidate, address, object)
	current := e.PrivateCatalogLocked(candidate)
	e.Mu.Unlock()
	scope.Catalog = candidate
	core.SetScopeLargeObjectRead(scope, old)
	host := (*core.StatementHost)(scope)
	for _, tc := range []struct {
		priv   string
		exists bool
	}{{"SELECT", false}, {"UPDATE", true}, {"UPDATE WITH GRANT OPTION", false}, {"SELECT,UPDATE", true}, {"SELECT,UPDATE WITH GRANT OPTION", false}} {
		value, exists, err := host.ObjectPrivilege(&evaluation, functions.AccessLargeObject, nil, functions.AccessRole{ByOID: true, OID: 910291}, tc.priv)
		if err != nil || exists != tc.exists || value != tc.exists {
			t.Fatal(tc.priv, value, exists, err)
		}
		if core.ScopeLargeObjectRead(scope) != old || old.Get(address) != nil || current.Get(address) != object {
			t.Fatal("inquiry changed immutable frontiers")
		}
	}
	restore := scope.ActivateSQLRoutine(nil, current)
	if core.ScopeLargeObjectRead(scope) != current {
		t.Fatal("body did not select fresh frontier")
	}
	nested := scope.ActivateSQLRoutine(nil, old)
	if core.ScopeLargeObjectRead(scope) != old {
		t.Fatal("nested frontier")
	}
	if err := nested(nil); err != nil {
		t.Fatal(err)
	}
	if core.ScopeLargeObjectRead(scope) != current {
		t.Fatal("nested unwind")
	}
	if err := restore(nil); err != nil {
		t.Fatal(err)
	}
	if core.ScopeLargeObjectRead(scope) != old || scope.Catalog != candidate {
		t.Fatal("outer unwind lost frontier or current candidate")
	}
}

package extension_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
)


func TestInsertUsernameScalarEvaluationAuthority(t *testing.T) {
	e, installed := ownershipUnitInstall(t, "insert_username")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION autoinc")
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	view := auth.Catalog
	provider := view.MustProcedure(installed.Members[0])
	trigger, _ := view.LookupType(2279)
	n := plan.InsertUsernameExpr{
		Info: expr.Info{Usage: trigger.Usage(), Property: expr.Properties{Volatility: expr.Volatile}, RoutineReference: provider},
		Ref: view.Owner().Domain.Ref(view.Epoch(), provider.Address()),
		Namespace: "public",
	}
	evaluation := exec.Context{
		EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view},
		EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth},
	}
	_, err = n.Eval(&evaluation)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != "insert_username: not fired by trigger manager" {
		t.Fatal("genuine scalar invocation lost its protocol diagnostic", err)
	}
	autoinc := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "autoinc"}))
	different := view.MustProcedure(autoinc.Members[0])
	foreign, foreignInstalled := ownershipUnitInstall(t, "insert_username")
	foreignView := foreign.CatalogRead()
	foreignProvider := foreignView.MustProcedure(foreignInstalled.Members[0])
	for _, tc := range []struct {
		name string
		mutate func(*plan.InsertUsernameExpr)
		stale bool
	}{
		{"different_native_provider", func(v *plan.InsertUsernameExpr) {
			v.RoutineReference = different
			v.Ref = view.Owner().Domain.Ref(view.Epoch(), different.Address())
		}, false},
		{"zero_reference", func(v *plan.InsertUsernameExpr) { v.Ref = ids.ObjectRef{} }, false},
		{"foreign_reference", func(v *plan.InsertUsernameExpr) {
			v.Ref = foreignView.Owner().Domain.Ref(view.Epoch(), provider.Address())
		}, false},
		{"foreign_provider", func(v *plan.InsertUsernameExpr) {
			v.RoutineReference = foreignProvider
			v.Ref = foreignView.Owner().Domain.Ref(foreignView.Epoch(), foreignProvider.Address())
		}, false},
		{"stale_epoch", func(v *plan.InsertUsernameExpr) { v.Ref.Epoch = 0 }, true},
		{"future_epoch", func(v *plan.InsertUsernameExpr) { v.Ref.Epoch++ }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := n
			tc.mutate(&bad)
			if tc.name == "different_native_provider" {
				if err := bad.ValidateStoredProvider(); err != nil { t.Fatal("test did not reach concrete authentication", err) }
			}
			_, err := bad.Eval(&evaluation)
			diagnostic = nil
			if err == nil || errors.As(err, &diagnostic) && diagnostic.Code == "XX000" && diagnostic.Message == "insert_username: not fired by trigger manager" {
				t.Fatal("forged execution authority reached helper body", err)
			}
			if tc.stale && !errors.Is(err, ids.ErrCatalogReplaced) { t.Fatal("epoch mismatch did not reject catalog authority", err) }
		})
	}
	if n.RoutineReference != provider || n.Ref != view.Owner().Domain.Ref(view.Epoch(), provider.Address()) {
		t.Fatal("execution mutated retained recipe authority")
	}
}

package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func rangeTypeUnitRemappedConstructor(t *testing.T) (*core.Engine, *catalog.Procedure, *catalog.Procedure) {
	t.Helper()
	source, original, _ := rangeTypeUnitGeneratedCast(t)
	originalType := original.RangeConstructor.Type
	next := catalog.CloneState(source.State())
	target := engine.New()
	roles := authorization.Seed(source.Owner().Principals)
	sourceRoles := roles.Read(source.Owner().Principals.Principal(authorization.BootstrapPrincipalID))
	targetRoles := target.RoleStore.PublishedRead(&target.Catalog.Owner, target.CatalogRead())
	next, principal, err := func() (*catalog.State, catalog.ProcedureRemapper, error) {
		target.Mu.Lock()
		defer target.Mu.Unlock()
		return core.EngineRemapPreparedCatalog(target, next, sourceRoles, targetRoles, nil)
	}()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := typedef.ReownDomainTypes(next, nil, true, routine.RangeTypeRebinder(next, nil)); err != nil {
		t.Fatal(err)
	}
	view := target.Catalog.Read(next)
	remap := stored.NewRoutineRemap(view, nil, nil, principal, false)
	if err := routine.RebindSQLDefinitions(next, remap); err != nil {
		t.Fatal("constructor declaration stabilization reapplied source principal policy", err)
	}
	current := next.MustProcedure(original.Address())
	again, err := remap.Procedure(original)
	if err != nil || again != current {
		t.Fatal("original and catalog consumers lost the operation memo", err)
	}
	if current.Owner != target.Catalog.Owner.Principals.Principal(authorization.BootstrapPrincipalID) || original.Owner == current.Owner {
		t.Fatal("constructor owner was not reprovenanced exactly once")
	}
	if current.RangeConstructor.Type == originalType || original.RangeConstructor.Type != originalType {
		t.Fatal("constructor type generation was transplanted or source mutated")
	}
	if err := catalog.ValidateRangeConstructor(view, current); err != nil {
		t.Fatal(err)
	}
	target.Catalog.PublishLocked(next)
	return target, original, current
}

func TestRangeTypeConstructorOriginalRemap(t *testing.T) {
	_, _, _ = rangeTypeUnitRemappedConstructor(t)
}

func TestRangeTypeGeneratedCastFrozenSlots(t *testing.T) {
	target, _, _ := rangeTypeUnitRemappedConstructor(t)
	for _, sql := range []string{`SELECT ('[1,3]'::public.unit_span)::public.unit_spans`, `SELECT (NULL::public.unit_span)::public.unit_spans`} {
		binder := newTestExpressionBinder(t, context.Background(), target, sqltype.InputEnvironment{}, nil)
		binder.Params.NoParameters = true
		raw := enginetest.Statement(t, sql).(*parser.Select).Targets[0]
		bound, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		if err := binder.CaptureViewLiterals(raw, bound, analyze.ViewTargetLiteral, 0); err != nil {
			t.Fatal("internal negative slots leaked into frozen cast children", err)
		}
	}
}

func rangeTypeUnitGeneratedCast(t *testing.T) (catalog.Read, *catalog.Procedure, *catalog.Cast) {
	t.Helper()
	view, r, m := rangeTypeUnitFamily(t, sqltype.Int4, false)
	owner := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.ID())}).Owner
	provider := &catalog.RangeConstructor{Type: m, Form: catalog.MultirangeConstructorOne}
	signature, err := provider.Signature(view)
	if err != nil {
		t.Fatal(err)
	}
	procedure := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 920004}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 2, ObjectName: m.Name(), Signature: signature, RoutineKind: catalog.FunctionRoutine, Result: ids.OID(m.ID()), Owner: owner, Volatility: expr.Immutable, Strict: true, RangeConstructor: provider, ACL: authorization.DefaultBaseline(owner, authorization.DefaultRoutines, false)}
	view.State().SetDescriptor(procedure.Address(), procedure)
	view.State().BindName(catalog.DescriptorNameKey(procedure), procedure.Address())
	procedures := catalog.ProcedureIndexEdit{Index: view.State().ProcedureNames()}
	procedures.Replace(nil, procedure)
	view.State().SetProcedureNames(procedures.Index)
	cast := &catalog.Cast{OID: 920010, ObjectRevision: 2, Source: r.ID(), Target: m.ID(), Function: procedure.Address(), Context: "e", Method: "f", GeneratedRange: ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.ID())}}
	view.State().SetDescriptor(cast.Address(), cast)
	view.State().BindName(catalog.DescriptorNameKey(cast), cast.Address())
	return view, procedure, cast
}

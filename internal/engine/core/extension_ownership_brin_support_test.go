package core_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestExtensionOwnershipBRINSupportCapability(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION unaccent")
	for _, target := range []string{
		"brin_minmax_opcinfo(internal)",
		"brin_minmax_add_value(internal,internal,internal,internal)",
		"brin_minmax_consistent(internal,internal,internal)",
		"brin_minmax_union(internal,internal,internal)",
	} {
		enginetest.RoleExecute(t, e, op, "ALTER EXTENSION unaccent ADD FUNCTION pg_catalog."+target)
	}
	view := e.CatalogRead()
	for slot := uint8(1); slot <= 4; slot++ {
		p := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(3382 + int(slot))})
		if p.Extension == (ids.ObjectAddress{}) || catalog.ValidateBrinSupport(p) != nil {
			t.Fatal("lawful membership lost intrinsic capability", slot)
		}
		if err := (stored.ProcedureProvider{}).Validate(view, p); err != nil {
			t.Fatal("reciprocal member rejected", slot, err)
		}
		if _, scalar := sqltype.NativeIndexRoutineByOID(uint32(p.Address().ObjectID)); scalar {
			t.Fatal("internal summary acquired scalar ABI", slot)
		}
		for name, mutate := range map[string]func(*catalog.Procedure){
			"address":    func(d *catalog.Procedure) { d.ObjectAddress.ObjectID++ },
			"namespace":  func(d *catalog.Procedure) { d.ObjectNamespace.ObjectID++ },
			"name":       func(d *catalog.Procedure) { d.ObjectName += "_forged" },
			"revision":   func(d *catalog.Procedure) { d.ObjectRevision = 0 },
			"slot":       func(d *catalog.Procedure) { d.BrinSupport = 5 },
			"signature":  func(d *catalog.Procedure) { d.Signature = "23" },
			"result":     func(d *catalog.Procedure) { d.Result = 23 },
			"strict":     func(d *catalog.Procedure) { d.Strict = false },
			"volatility": func(d *catalog.Procedure) { d.Volatility = expr.Volatile },
			"kind":       func(d *catalog.Procedure) { d.RoutineKind = catalog.ProcedureRoutine },
			"language":   func(d *catalog.Procedure) { d.Display.Language = catalog.CRoutineLanguage },
			"source":     func(d *catalog.Procedure) { d.Display.Source = "forged" },
			"builtin":    func(d *catalog.Procedure) { d.Builtin = &catalog.BuiltinRoutineDeclaration{} },
			"dictionary": func(d *catalog.Procedure) { d.Dictionary = d.Address() },
			"dependency": func(d *catalog.Procedure) { d.DeclarationDependencies = []ids.ObjectAddress{d.Address()} },
		} {
			t.Run(p.Name()+"/"+name, func(t *testing.T) {
				bad := *p
				mutate(&bad)
				if catalog.ValidateBrinSupport(&bad) == nil {
					t.Fatal("corrupted intrinsic accepted")
				}
			})
		}
		ext, _ := view.Extension(p.Extension)
		state := catalog.CloneState(view.State())
		broken := *ext
		broken.Members = slices.DeleteFunc(slices.Clone(ext.Members), func(a ids.ObjectAddress) bool { return a == p.Address() })
		state.SetDescriptor(ext.Address(), &broken)
		if err := (stored.ProcedureProvider{}).Validate(e.Catalog.Read(state), p); err == nil {
			t.Fatal("missing reciprocal link accepted")
		}
		bad := *p
		bad.Extension.ObjectID++
		if err := (stored.ProcedureProvider{}).Validate(view, &bad); err == nil {
			t.Fatal("foreign membership owner accepted")
		}
		// Detached declaration is valid only with the reciprocal header changed too.
		bad = *p
		bad.Extension = ids.ObjectAddress{}
		state.SetDescriptor(p.Address(), &bad)
		if err := (stored.ProcedureProvider{}).Validate(e.Catalog.Read(state), &bad); err != nil {
			t.Fatal("detached intrinsic rejected", err)
		}
		if err := catalog.ValidateExtensionMembership(e.Catalog.Read(state), &broken); err != nil {
			t.Fatal("detached reciprocal header rejected", err)
		}
		duplicate := *ext
		duplicate.Members = append(slices.Clone(ext.Members), p.Address())
		if err := catalog.ValidateExtensionMembership(view, &duplicate); err == nil {
			t.Fatal("duplicate intrinsic membership accepted")
		}
		if err := catalog.ValidateExtensionMembership(e.Catalog.Read(state), ext); err == nil {
			t.Fatal("stale header retained detached intrinsic")
		}
		if p.Extension != ext.Address() || !slices.Contains(ext.Members, p.Address()) {
			t.Fatal("private candidate mutated original membership")
		}
	}
}

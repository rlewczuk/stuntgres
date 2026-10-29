package core_test

import (
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestExtensionOwnershipRelocation(t *testing.T) {
	t.Run("immutable_descriptors_and_execution", func(t *testing.T) {
		e, ext := ownershipUnitInstall(t, "unaccent")
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, `CREATE SCHEMA eo_unit_destination`)
		base := e.CatalogRead()
		oldMembers := append([]ids.ObjectAddress(nil), ext.Members...)
		enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent SET SCHEMA eo_unit_destination`)
		current := e.CatalogRead()
		moved := current.MustExtension(ext.Address())
		if moved == ext || moved.Revision() <= ext.Revision() || ext.TargetNamespace == moved.TargetNamespace {
			t.Fatal("extension identity mutation was not copy-on-write")
		}
		if ext.TargetNamespace != base.MustExtension(ext.Address()).TargetNamespace {
			t.Fatal("captured extension changed")
		}
		for i, a := range oldMembers {
			old, next := base.Get(a), current.Get(a)
			if next == old || next.Address() != old.Address() || next.Revision() <= old.Revision() || next.Namespace() != moved.TargetNamespace || old.Namespace() != ext.TargetNamespace || moved.Members[i] != a {
				t.Fatal("member identity/revision/immutable backing", a)
			}
			if p, ok := old.(*catalog.Procedure); ok {
				n := next.(*catalog.Procedure)
				before, err := extension.ProcedureSymbol(base, p)
				if err != nil {
					t.Fatal(err)
				}
				after, err := extension.ProcedureSymbol(current, n)
				if err != nil || before != after || p.Implementation != n.Implementation || p.Installed != n.Installed || p.Owner != n.Owner || p.Dictionary != n.Dictionary {
					t.Fatal("relocation changed executable authority", a, err)
				}
			}
			if d, ok := old.(*catalog.TSDictionary); ok {
				n := next.(*catalog.TSDictionary)
				if n.Runtime != d.Runtime || n.Template != d.Template || n.Owner != d.Owner {
					t.Fatal("dictionary runtime replaced")
				}
			}
		}
	})
	for _, kind := range []string{"revision_exhaustion", "late_predecessor"} {
		t.Run(kind, func(t *testing.T) {
			e, ext := ownershipUnitInstall(t, "unaccent")
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.RoleExecute(t, e, op, `CREATE SCHEMA eo_unit_destination`)
			base := e.CatalogRead()
			p := base.MustProcedure(ext.Members[0])
			changed := *p
			publish := func() {
				e.Mu.Lock()
				defer e.Mu.Unlock()
				state := catalog.CloneState(e.Catalog.State)
				state.SetDescriptor(p.Address(), &changed)
				e.Catalog.PublishLocked(state)
			}
			provider := e.Catalog.Providers[ids.ExtensionClassID]
			var fired atomic.Bool
			if kind == "revision_exhaustion" {
				changed.ObjectRevision = ^ids.DefinitionRevision(0)
				publish()
			} else {
				changed.ObjectRevision = p.Revision() + 1
				e.Catalog.Providers[ids.ExtensionClassID] = alterDropProvider{Provider: provider, callback: func() {
					if fired.CompareAndSwap(false, true) {
						publish()
					}
				}}
				defer func() { e.Catalog.Providers[ids.ExtensionClassID] = provider }()
			}
			err := extensionUnitExecute(t, e, op, enginetest.Statement(t, `ALTER EXTENSION unaccent SET SCHEMA eo_unit_destination`))
			if err == nil {
				t.Fatal("invalid predecessor/revision published relocation")
			}
			if kind == "late_predecessor" && !fired.Load() {
				t.Fatal("outside-lock validation not exercised")
			}
			if e.CatalogRead().Get(ext.Address()) != ext {
				t.Fatal("failed relocation changed extension")
			}
			for _, a := range ext.Members {
				want := base.Get(a)
				if a == p.Address() {
					want = &changed
				}
				if e.CatalogRead().Get(a) != want {
					t.Fatal("failed relocation installed a prefix", a)
				}
			}
		})
	}
}

package schemacmds_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestSchemaDefinitionRevisionFreshness(t *testing.T) {
	checkRename := func(t *testing.T, e *engine.Engine, name string) {
		t.Helper()
		before := e.CatalogRead()
		ref := schemaRef(t, e, name)
		old, _ := before.Descriptor(ref.Address)
		enginetest.Run(t, e, "ALTER SCHEMA "+name+" RENAME TO renamed")
		after := e.CatalogRead()
		fresh, _ := after.Descriptor(ref.Address)
		if schemaRef(t, e, "renamed") != ref || after.Epoch() != before.Epoch() || fresh.Revision() <= old.Revision() {
			t.Fatalf("definition token not fresh with stable identity: old=%d new=%d ref=%v", old.Revision(), fresh.Revision(), ref)
		}
		retained, _ := before.Descriptor(ref.Address)
		if retained != old || retained.Name() != name || retained.Revision() != old.Revision() {
			t.Fatal("mutated retained descriptor")
		}
	}
	t.Run("fresh_builtin", func(t *testing.T) {
		checkRename(t, engine.New(), "public")
	})
	t.Run("first_after_restore", func(t *testing.T) {
		e := engine.New()
		if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE SCHEMA restored;")); err != nil {
			t.Fatal(err)
		}
		checkRename(t, e, "restored")
	})
	for _, foreign := range []bool{false, true} {
		for _, frontier := range []ids.DefinitionRevision{50, 500} {
			t.Run(fmt.Sprintf("import_frontier/foreign_%t/frontier_%d", foreign, frontier), func(t *testing.T) {
				e := engine.New()
				source := e
				if foreign {
					source = engine.New()
				}
				enginetest.Run(t, source, "CREATE SCHEMA imported CREATE TABLE t(id INT)")
				view := source.CatalogRead()
				next := catalog.CloneState(view.State())
				ns := schemaRef(t, source, "imported").Address
				n := *next.MustNamespace(ns)
				n.ObjectRevision = 100
				next.SetDescriptor(ns, &n)
				addr := enginetest.RoleTableRef(t, source, "imported.t").Address
				r := *next.MustRelation(addr)
				r.ObjectRevision = 200
				next.SetDescriptor(addr, &r)
				e.Catalog.InstallRevisionFrontierLocked(frontier)
				if _, err := e.ReplaceCatalog(context.Background(), source, source.Catalog.Read(next)); err != nil {
					t.Fatal(err)
				}
				before := e.CatalogRead()
				ref := schemaRef(t, e, "imported")
				old, _ := before.Descriptor(ref.Address)
				enginetest.Run(t, e, "ALTER SCHEMA imported RENAME TO advanced")
				fresh, _ := e.CatalogRead().Descriptor(ref.Address)
				want := ids.DefinitionRevision(201)
				if frontier > want {
					want = frontier
				}
				if fresh.Revision() != want || schemaRef(t, e, "advanced") != ref || e.CatalogRead().Epoch() != before.Epoch() || old.Name() != "imported" || old.Revision() != 100 {
					t.Fatalf("import frontier/retained descriptor: revision=%d want=%d old=%v", fresh.Revision(), want, old)
				}
				enginetest.Run(t, e, "CREATE SCHEMA following")
				following, _ := e.CatalogRead().Descriptor(schemaRef(t, e, "following").Address)
				if following.Revision() != want+1 {
					t.Fatal("frontier did not advance", following.Revision(), want+1)
				}
			})
		}
	}
	maximum := ids.DefinitionRevision(^uint64(0))
	for _, revision := range []ids.DefinitionRevision{maximum, maximum - 1, maximum - 2} {
		t.Run(fmt.Sprintf("terminal_import/%d", revision), func(t *testing.T) {
			e, source := engine.New(), engine.New()
			enginetest.Run(t, source, "CREATE SCHEMA terminal")
			view := source.CatalogRead()
			next := catalog.CloneState(view.State())
			addr := schemaRef(t, source, "terminal").Address
			n := *next.MustNamespace(addr)
			n.ObjectRevision = revision
			next.SetDescriptor(addr, &n)
			if _, err := e.ReplaceCatalog(context.Background(), source, source.Catalog.Read(next)); err != nil {
				t.Fatal(err)
			}
			name := "terminal"
			if revision == maximum-2 {
				enginetest.Run(t, e, "ALTER SCHEMA terminal RENAME TO last_revision")
				name = "last_revision"
				d, _ := e.CatalogRead().Descriptor(schemaRef(t, e, name).Address)
				if d.Revision() != maximum-1 {
					t.Fatal("last revision not allocated", d.Revision())
				}
			}
			before, storage, sequence := e.CatalogRead(), e.Storage.Relations, e.Visibility.CommitSeq()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			_, err := e.Execute(op, enginetest.Statement(t, "ALTER SCHEMA "+name+" RENAME TO forbidden"), 0, nil)
			done()
			if err == nil || err.Error() != "stuntgres: catalog definition revision exhausted" || e.CatalogRead().State() != before.State() || !reflect.DeepEqual(e.Storage.Relations, storage) || e.Visibility.CommitSeq() != sequence {
				t.Fatal("terminal mutation published or returned wrong failure", err)
			}
			if err := engine.Reset(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			if e.Catalog.NextRevision() != maximum {
				t.Fatal("Reset rewound exhausted frontier", e.Catalog.NextRevision())
			}
		})
	}
	t.Run("canceled_import", func(t *testing.T) {
		e, source := engine.New(), engine.New()
		enginetest.Run(t, source, "CREATE SCHEMA canceled")
		view := source.CatalogRead()
		next := catalog.CloneState(view.State())
		addr := schemaRef(t, source, "canceled").Address
		n := *next.MustNamespace(addr)
		n.ObjectRevision = 1000
		next.SetDescriptor(addr, &n)
		frontier, before := e.Catalog.NextRevision(), e.CatalogRead()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := e.ReplaceCatalog(ctx, source, source.Catalog.Read(next))
		if !errors.Is(err, context.Canceled) || e.Catalog.NextRevision() != frontier || e.CatalogRead().State() != before.State() {
			t.Fatal("canceled import advanced allocator/catalog", err)
		}
	})
	t.Run("failed_import", func(t *testing.T) {
		e, source := engine.New(), engine.New()
		enginetest.Run(t, source, "CREATE USER unavailable_owner")
		enginetest.Run(t, source, "CREATE SCHEMA invalid AUTHORIZATION unavailable_owner")
		view := source.CatalogRead()
		next := catalog.CloneState(view.State())
		addr := schemaRef(t, source, "invalid").Address
		n := *next.MustNamespace(addr)
		n.ObjectRevision = 1000
		next.SetDescriptor(addr, &n)
		frontier, before := e.Catalog.NextRevision(), e.CatalogRead()
		_, err := e.ReplaceCatalog(context.Background(), source, source.Catalog.Read(next))
		if err == nil || e.Catalog.NextRevision() != frontier || e.CatalogRead().State() != before.State() {
			t.Fatal("failed principal remap advanced allocator/catalog", err)
		}
	})
}

package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestSchemaDefinitionRevisionFreshness(t *testing.T) {
	checkRename := func(t *testing.T, e *Engine, name string) {
		t.Helper()
		before := e.CatalogRead()
		ref := schemaRef(t, e, name)
		old, _ := before.Descriptor(ref.Address)
		run(t, e, "ALTER SCHEMA "+name+" RENAME TO renamed")
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
		checkRename(t, New(), "public")
	})
	t.Run("first_after_restore", func(t *testing.T) {
		e := New()
		if err := e.Restore(context.Background(), strings.NewReader("CREATE SCHEMA restored;")); err != nil {
			t.Fatal(err)
		}
		checkRename(t, e, "restored")
	})
	for _, foreign := range []bool{false, true} {
		for _, frontier := range []ids.DefinitionRevision{50, 500} {
			t.Run(fmt.Sprintf("import_frontier/foreign_%t/frontier_%d", foreign, frontier), func(t *testing.T) {
				e := New()
				source := e
				if foreign {
					source = New()
				}
				run(t, source, "CREATE SCHEMA imported CREATE TABLE t(id INT)")
				view := source.CatalogRead()
				next := cloneCatalog(view.state)
				ns := schemaRef(t, source, "imported").Address
				n := *next.descriptors[ns].(*namespaceDescriptor)
				n.revision = 100
				next.descriptors[ns] = &n
				addr := roleTableRef(t, source, "imported.t").Address
				r := *next.descriptors[addr].(*relationDescriptor)
				r.revision = 200
				next.descriptors[addr] = &r
				e.catalog.nextRevision = frontier
				if _, err := e.ReplaceCatalog(context.Background(), CatalogRead{owner: source, state: next}); err != nil {
					t.Fatal(err)
				}
				before := e.CatalogRead()
				ref := schemaRef(t, e, "imported")
				old, _ := before.Descriptor(ref.Address)
				run(t, e, "ALTER SCHEMA imported RENAME TO advanced")
				fresh, _ := e.CatalogRead().Descriptor(ref.Address)
				want := ids.DefinitionRevision(201)
				if frontier > want {
					want = frontier
				}
				if fresh.Revision() != want || schemaRef(t, e, "advanced") != ref || e.CatalogRead().Epoch() != before.Epoch() || old.Name() != "imported" || old.Revision() != 100 {
					t.Fatalf("import frontier/retained descriptor: revision=%d want=%d old=%v", fresh.Revision(), want, old)
				}
				run(t, e, "CREATE SCHEMA following")
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
			e, source := New(), New()
			run(t, source, "CREATE SCHEMA terminal")
			view := source.CatalogRead()
			next := cloneCatalog(view.state)
			addr := schemaRef(t, source, "terminal").Address
			n := *next.descriptors[addr].(*namespaceDescriptor)
			n.revision = revision
			next.descriptors[addr] = &n
			if _, err := e.ReplaceCatalog(context.Background(), CatalogRead{owner: source, state: next}); err != nil {
				t.Fatal(err)
			}
			name := "terminal"
			if revision == maximum-2 {
				run(t, e, "ALTER SCHEMA terminal RENAME TO last_revision")
				name = "last_revision"
				d, _ := e.CatalogRead().Descriptor(schemaRef(t, e, name).Address)
				if d.Revision() != maximum-1 {
					t.Fatal("last revision not allocated", d.Revision())
				}
			}
			before, storage, sequence := e.CatalogRead(), e.storage, e.commitSeq
			op, done := roleOperation(t, e, "postgres")
			_, err := e.Execute(op, statement(t, "ALTER SCHEMA "+name+" RENAME TO forbidden"), 0, nil)
			done()
			if err == nil || err.Error() != "stuntgres: catalog definition revision exhausted" || e.CatalogRead().state != before.state || !reflect.DeepEqual(e.storage, storage) || e.commitSeq != sequence {
				t.Fatal("terminal mutation published or returned wrong failure", err)
			}
			if err := e.Reset(context.Background()); err != nil {
				t.Fatal(err)
			}
			if e.catalog.nextRevision != maximum {
				t.Fatal("Reset rewound exhausted frontier", e.catalog.nextRevision)
			}
		})
	}
	t.Run("canceled_import", func(t *testing.T) {
		e, source := New(), New()
		run(t, source, "CREATE SCHEMA canceled")
		view := source.CatalogRead()
		next := cloneCatalog(view.state)
		addr := schemaRef(t, source, "canceled").Address
		n := *next.descriptors[addr].(*namespaceDescriptor)
		n.revision = 1000
		next.descriptors[addr] = &n
		frontier, before := e.catalog.nextRevision, e.CatalogRead()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := e.ReplaceCatalog(ctx, CatalogRead{owner: source, state: next})
		if !errors.Is(err, context.Canceled) || e.catalog.nextRevision != frontier || e.CatalogRead().state != before.state {
			t.Fatal("canceled import advanced allocator/catalog", err)
		}
	})
	t.Run("failed_import", func(t *testing.T) {
		e, source := New(), New()
		run(t, source, "CREATE USER unavailable_owner")
		run(t, source, "CREATE SCHEMA invalid AUTHORIZATION unavailable_owner")
		view := source.CatalogRead()
		next := cloneCatalog(view.state)
		addr := schemaRef(t, source, "invalid").Address
		n := *next.descriptors[addr].(*namespaceDescriptor)
		n.revision = 1000
		next.descriptors[addr] = &n
		frontier, before := e.catalog.nextRevision, e.CatalogRead()
		_, err := e.ReplaceCatalog(context.Background(), CatalogRead{owner: source, state: next})
		if err == nil || e.catalog.nextRevision != frontier || e.CatalogRead().state != before.state {
			t.Fatal("failed principal remap advanced allocator/catalog", err)
		}
	})
}

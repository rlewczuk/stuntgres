package schemacmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestIsnOrderingTransport(t *testing.T) {
	ctx := context.Background()
	source, target := engine.New(), engine.New()
	owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
	defer closeOwner()
	namespace := intarrayUnitTemporaryNamespace(t, target, owner)
	installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
	extensionUnitPublish(t, target, installer, cubeUnitStage(t, target, installer, "unaccent", namespace.Name()))
	closeInstaller()
	retained := target.CatalogRead().MustExtension(target.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
	collision := retained.Members[len(retained.Members)-1]
	// Force a real retained-TEMP collision at the source's EAN scalar slot.
	source.Mu.Lock()
	for source.Catalog.NextOID() < collision.ObjectID-1 {
		if _, err := source.Catalog.AllocateOIDLocked(); err != nil {
			source.Mu.Unlock()
			t.Fatal(err)
		}
	}
	source.Mu.Unlock()
	op, done := enginetest.RoleOperation(t, source, "postgres")
	defer done()
	extensionUnitPublish(t, source, op, isnUnitStage(t, source, op, "isn", ""))
	original := source.CatalogRead()
	installation := original.MustExtension(original.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "isn"}))
	if installation.Installation.Members[0].ObjectID != collision.ObjectID {
		t.Fatal("source did not collide with genuine retained TEMP member")
	}
	var keys [8]plan.SortKey
	inputs := [8]string{"9781234567897", "123456789X", "9781234567897", "M123456785", "9790123456785", "12345679", "9771234567003", "123456789012"}
	for role := range keys {
		typ := original.MustType(installation.Installation.Members[role*3]).SQLType
		value, err := typ.Input(ctx, inputs[role], -1)
		if err != nil {
			t.Fatal(err)
		}
		class, exists, err := original.DefaultIndexClass(403, typ)
		if err != nil || !exists {
			t.Fatal("missing genuine default", role, err)
		}
		kernel, capture, err := routine.BindCapturedIndexOrdering(original, typ.Usage(), class)
		if err != nil {
			t.Fatal(err)
		}
		keys[role] = plan.SortKey{Expression: plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}, Comparison: kernel.DatumComparison(), Ordering: capture}
	}
	// Advance real current declarations while preserving the retained originals.
	for _, statement := range []string{
		"ALTER TYPE public.ean13 RENAME TO article",
		"ALTER FUNCTION public.btean13cmp(public.article,public.article) RENAME TO compare_articles",
	} {
		domainUnitPublish(t, source, domainUnitStage(t, source, op, statement))
	}
	if source.CatalogRead().MustType(installation.Installation.Members[0]).SQLType == keys[0].Expression.ResultUsage().Type {
		t.Fatal("rename failed to advance the real type generation")
	}
	if _, err := target.ReplaceCatalog(ctx, source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	view := target.CatalogRead()
	imported := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "isn"}))
	candidate := catalog.CloneState(view.State())
	if len(candidate.ScalarImports()) != 0 {
		t.Fatal("published catalog retained transient import authority")
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{}
	types := map[*sqltype.Descriptor]*sqltype.Descriptor{}
	for slot, address := range installation.Installation.Members {
		mapping[address] = imported.Installation.Members[slot]
	}
	for role, key := range keys {
		from := key.Expression.ResultUsage().Type
		to := view.MustType(imported.Installation.Members[role*3]).SQLType
		types[from] = to
		candidate.SetScalarImport(from, to)
		family := imported.Installation.Members[extension.IsnFamilySlot]
		member, exists := view.FamilySupport(family.ObjectID, ids.OID(to.ID()), ids.OID(to.ID()), 1)
		if !exists {
			t.Fatal("missing destination support", role)
		}
		mapping[key.Ordering.References()[2].Address] = member.Address()
	}
	view = view.WithState(candidate)
	if types[keys[0].Expression.ResultUsage().Type].ID() == keys[0].Expression.ResultUsage().Type.ID() {
		t.Fatal("destination did not displace the EAN scalar identity")
	}
	mapper := stored.ViewQueryRemapper{
		Catalog: view,
		Type: func(old *sqltype.Descriptor) *sqltype.Descriptor {
			if next := types[old]; next != nil {
				return next
			}
			if old == nil {
				return nil
			}
			current, _ := view.LookupType(old.ID())
			return current
		},
		Ref: func(ref ids.ObjectRef) ids.ObjectRef {
			address := ref.Address
			if next, exists := mapping[address]; exists {
				address = next
			}
			return view.Owner().Domain.Ref(view.Epoch(), address)
		},
	}
	for role, key := range keys {
		for _, kind := range []string{"sort", "window"} {
			t.Run(inputs[role]+"/"+kind, func(t *testing.T) {
				build := func(key plan.SortKey) plan.Node {
					if kind == "sort" {
						return &plan.SortNode{Child: &plan.ValuesNode{}, Keys: []plan.SortKey{key}}
					}
					return &plan.WindowNode{Child: &plan.ValuesNode{}, Specs: []plan.WindowSpec{{Order: []plan.SortKey{key}, Frame: plan.WindowFrame{Unit: parser.FrameRows}}}}
				}
				extract := func(node plan.Node) plan.SortKey {
					if kind == "sort" {
						return node.(*plan.SortNode).Keys[0]
					}
					return node.(*plan.WindowNode).Specs[0].Order[0]
				}
				root := build(key)
				m := mapper.Fresh()
				mapped, err := m.Query(root)
				if err != nil {
					t.Fatal("genuine ordering transport", err)
				}
				got := extract(mapped)
				usage := types[key.Expression.ResultUsage().Type].Usage()
				if got.Ordering == nil || got.Ordering == key.Ordering || got.Expression.ResultUsage() != usage || !got.Comparison.OrderingFor(usage) {
					t.Fatal("lost destination-owned generation/kernel")
				}
				if err := got.Ordering.Validate(usage, got.Comparison); err != nil {
					t.Fatal(err)
				}
				for i, ref := range key.Ordering.References() {
					if got.Ordering.References()[i] != mapper.Ref(ref) {
						t.Fatal("lost one of four genuine mapped references", i)
					}
				}
				low, err := usage.Type.Input(ctx, inputs[role], -1)
				if err != nil {
					t.Fatal(err)
				}
				high, err := usage.Type.Input(ctx, inputs[role]+"!", -1)
				if err != nil {
					t.Fatal(err)
				}
				if n, err := got.Comparison.Compare(ctx, low, high, sqltype.InputEnvironment{}); err != nil || n != -1 {
					t.Fatal("destination lost packed flag order", n, err)
				}
				nested := plan.SubqueryExpr{QueryPlan: &plan.Query{Root: root}}
				relocated, err := stored.RemapExpression(nested, mapper.Ref, nil)
				if err != nil || extract(relocated.(plan.SubqueryExpr).QueryPlan.Root).Ordering != key.Ordering || !extract(relocated.(plan.SubqueryExpr).QueryPlan.Root).Comparison.Equal(key.Comparison) {
					t.Fatal("reference traversal replaced original capture/kernel", err)
				}
				for _, cause := range []string{"missing_capture", "wrong_pairing", "foreign_destination", "missing_support"} {
					t.Run(cause, func(t *testing.T) {
						bad := mapper.Fresh()
						invalid := key
						switch cause {
						case "missing_capture":
							invalid.Ordering = nil
						case "wrong_pairing":
							invalid.Ordering = keys[(role+1)%len(keys)].Ordering
						case "foreign_destination":
							bad.Ref = func(ref ids.ObjectRef) ids.ObjectRef {
								next := mapper.Ref(ref)
								return original.Owner().Domain.Ref(next.Epoch, next.Address)
							}
						case "missing_support":
							state := catalog.CloneState(view.State())
							state.RemoveDescriptor(got.Ordering.References()[2].Address)
							bad.Catalog = view.WithState(state)
						}
						if _, err := bad.Query(build(invalid)); err == nil {
							t.Fatal("unauthorized ordering publication accepted")
						}
						if err := key.Ordering.Validate(key.Expression.ResultUsage(), key.Comparison); err != nil {
							t.Fatal("rejection mutated original capture", err)
						}
					})
				}
				if extract(root).Ordering != key.Ordering || !extract(root).Comparison.Equal(key.Comparison) || keys[role].Expression.ResultUsage().Type != original.MustType(installation.Installation.Members[role*3]).SQLType {
					t.Fatal("transport changed retained source ownership")
				}
			})
		}
	}
}

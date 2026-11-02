package schemacmds_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestXML2StoredIdentity(t *testing.T) {
	ctx := context.Background()
	source, ext := xml2UnitInstalled(t)
	view := source.CatalogRead()
	b := enginetest.ExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	node, err := b.BindFunctionProvider(enginetest.Statement(t, "SELECT xpath_bool('<r>12</r>','.')").(*parser.Select).Targets[0].(parser.FunctionExpr))
	if err != nil {
		t.Fatal(err)
	}
	n := node.(plan.InstalledXML2Expr)
	if err := stored.ValidateInstalledXML2(n, view); err != nil {
		t.Fatal(err)
	}
	refs, err := stored.ExpressionDependRefs(ctx, view, n)
	if err != nil || !slices.Contains(refs, n.Ref.Address) {
		t.Fatal("routine dependency", refs, err)
	}
	retagged, _, err := stored.RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	got := retagged.(plan.InstalledXML2Expr)
	if got.Ref != n.Ref || got.Procedure != n.Procedure || got.Info != n.Info {
		t.Fatal("same-generation retag changed capture", got)
	}
	for _, render := range []func(catalog.Read, expr.Expr) (string, error){
		func(v catalog.Read, n expr.Expr) (string, error) { return (stored.ScalarRenderer{}).SQL(ctx, v, n) },
		func(v catalog.Read, n expr.Expr) (string, error) {
			return (stored.Deparser{View: v}).SQL(ctx, n, stored.DeparseParent{})
		},
	} {
		text, err := render(view, n)
		if err != nil || !strings.Contains(text, "public") || !strings.Contains(text, "xpath_bool") {
			t.Fatal("qualified installed renderer", text, err)
		}
	}
	foreign := engine.New()
	foreignBool, _ := foreign.CatalogRead().LookupType(sqltype.Bool)
	foreignText, _ := foreign.CatalogRead().LookupType(sqltype.Text)
	for _, mutate := range []func(*plan.InstalledXML2Expr){
		func(n *plan.InstalledXML2Expr) { n.Name = "xml_valid" },
		func(n *plan.InstalledXML2Expr) { n.Ref.Epoch = 0 },
		func(n *plan.InstalledXML2Expr) { n.Ref = new(ids.Domain).Ref(n.Ref.Epoch, n.Ref.Address) },
		func(n *plan.InstalledXML2Expr) { n.RoutineReference = nil },
		func(n *plan.InstalledXML2Expr) { n.Property.Volatility = expr.Stable },
		func(n *plan.InstalledXML2Expr) { n.Property.RowDependent = true },
		func(n *plan.InstalledXML2Expr) { n.Property.ParameterDependent = true },
		func(n *plan.InstalledXML2Expr) { n.Property.DomainDependent = true },
		func(n *plan.InstalledXML2Expr) { n.Property.RuntimeDomainConversion = true },
		func(n *plan.InstalledXML2Expr) {
			copy := *n.Procedure
			origin := *copy.Origin
			origin.Installation = nil
			copy.Origin = &origin
			n.Procedure = &copy
			n.RoutineReference = &copy
		},
		func(n *plan.InstalledXML2Expr) {
			copy := *n.Procedure
			origin := *copy.Origin
			installation := *origin.Installation
			origin.Installation = &installation
			copy.Origin = &origin
			n.Procedure = &copy
			n.RoutineReference = &copy
		},
		func(n *plan.InstalledXML2Expr) {
			copy := *n.Procedure
			copy.Implementation = extension.StockUUIDOssp
			n.Procedure = &copy
			n.RoutineReference = &copy
		},
		func(n *plan.InstalledXML2Expr) { n.Usage = foreignBool.Usage() },
		func(n *plan.InstalledXML2Expr) {
			n.Args = slices.Clone(n.Args)
			literal := n.Args[0].(plan.LiteralExpr)
			literal.Usage = foreignText.Usage()
			var err error
			literal.Value, err = literal.Value.Retag(foreignText)
			if err != nil {
				t.Fatal(err)
			}
			n.Args[0] = literal
		},
	} {
		bad := n
		mutate(&bad)
		if err := stored.ValidateInstalledXML2(bad, view); err == nil {
			t.Fatal("invalid stored identity admitted", bad)
		}
	}
	// Current SQL labels never select the immutable executable declaration.
	state := catalog.CloneState(view.State())
	changed := *n.Procedure
	changed.ObjectName = "renamed_xml2"
	changed.Extension = ids.ObjectAddress{}
	parent := *ext
	parent.Members = slices.Delete(slices.Clone(ext.Members), 3, 4)
	state.SetDescriptor(parent.Address(), &parent)
	changed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	state.UnbindName(catalog.DescriptorNameKey(n.Procedure))
	state.SetDescriptor(changed.Address(), &changed)
	state.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
	current := view.WithState(state)
	renamed := n
	renamed.Procedure, renamed.RoutineReference = &changed, &changed
	if err := stored.ValidateInstalledXML2(renamed, current); err != nil {
		t.Fatal("renamed symbol", err)
	}
	text, err := (stored.ScalarRenderer{}).SQL(ctx, current, renamed)
	if err != nil || !strings.Contains(text, "pg_catalog") || !strings.Contains(text, "renamed_xml2") || strings.Contains(text, "xpath_bool") {
		t.Fatal("renderer used released spelling instead of current identity", text, err)
	}
	// Retained TEMP identities occupy the destination allocator range, forcing
	// genuine import remapping rather than an equal-numeric-ID coincidence.
	target := engine.New()
	owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
	t.Cleanup(closeOwner)
	namespace := intarrayUnitTemporaryNamespace(t, target, owner)
	installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
	extensionUnitPublish(t, target, installer, citextUnitStage(t, target, installer, "unaccent", namespace.Name()))
	closeInstaller()
	if _, err := target.ReplaceCatalog(ctx, source, view); err != nil {
		t.Fatal("destination replacement", err)
	}
	destination := target.CatalogRead()
	imported := destination.MustExtension(destination.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "xml2"}))
	// Replacement may retain the immutable source BOOL codec when no TEMP
	// consumer demands the destination's old generation. Destination catalog
	// authority, not inequality with the source pointer, selects the exact type.
	destinationResult, ok := destination.LookupType(sqltype.Bool)
	if !ok {
		t.Fatal("destination BOOL generation missing")
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): imported.Address()}
	for slot, address := range ext.Members {
		mapping[address] = imported.Members[slot]
	}
	if imported.Members[3] == n.Ref.Address {
		t.Fatal("fixture did not force procedure remapping")
	}
	mapper := stored.ViewQueryRemapper{
		Catalog: destination,
		Type: func(d *sqltype.Descriptor) *sqltype.Descriptor {
			if d == nil {
				return nil
			}
			next, _ := destination.LookupType(d.ID())
			return next
		},
		Ref: func(ref ids.ObjectRef) ids.ObjectRef {
			address := ref.Address
			if next, ok := mapping[address]; ok {
				address = next
			}
			return destination.Domain().Ref(destination.Epoch(), address)
		},
		Procedure: extension.ProcedureRemapper(extension.DescriptorRemapper(mapping), replacementProcedurePolicyFixture(source, target), nil),
	}
	query, err := mapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{n}}})
	if err != nil {
		t.Fatal("authentic destination remap", err)
	}
	mapped := query.(*plan.ValuesNode).Rows[0][0].(plan.InstalledXML2Expr)
	if mapped.Ref.Address != imported.Members[3] || mapped.Ref.Domain() != destination.Domain() || mapped.Ref.Epoch != destination.Epoch() || mapped.Name != n.Name || mapped.Usage != destinationResult.Usage() || mapped.Procedure == n.Procedure || mapped.RoutineReference != mapped.Procedure || !exec.SameRoutineDeclaration(destination.MustProcedure(imported.Members[3]), mapped.Procedure) || !analyze.ValidInstalledXML2Shape(mapped) {
		t.Fatal("remap lost exact provider/ref/type generation", mapped)
	}
	if err := stored.ValidateInstalledXML2(mapped, destination); err != nil {
		t.Fatal(err)
	}
	if err := stored.ValidateInstalledXML2(n, destination); err == nil {
		t.Fatal("source identity leaked into destination")
	}
	if err := stored.ValidateInstalledXML2(mapped, view); err == nil {
		t.Fatal("destination identity accepted by source")
	}
	if source.CatalogRead().State() != view.State() || view.Get(n.Ref.Address) != n.Procedure {
		t.Fatal("destination mutated immutable source")
	}
}

package schemacmds_test

import (
	"context"
	"reflect"
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

func TestSSLInfoCertificateStoredIdentity(t *testing.T) {
	ctx := context.Background()
	source, ext := sslInfoUnitInstalled(t)
	// DDL captures genuine owner-column expressions and their revision manifest;
	// public SQL outcomes and sequence effects belong to the reference scenarios.
	enginetest.Run(t, source, "CREATE TABLE ssl_certificate_owner(field text, CHECK(ssl_client_dn_field(field) IS NULL), CHECK(ssl_issuer_field(field) IS NULL))")
	view := source.CatalogRead()
	owner := view.MustRelation(enginetest.RoleTableRef(t, source, "ssl_certificate_owner").Address)
	if len(owner.Constraints.Check) != 2 {
		t.Fatal("missing authentic owner constraints", owner)
	}
	var calls []plan.InstalledSSLInfoExpr
	originalDependencies := make([][]catalog.ExpressionDependency, 2)
	for i, definition := range owner.Constraints.Check {
		n := definition.Expression.(plan.NullTestExpr).Child.(plan.InstalledSSLInfoExpr)
		column, ok := n.Arg.(plan.ColumnExpr)
		attribute := view.Domain().Ref(view.Epoch(), owner.Address())
		attribute.Address.SubID = owner.Attributes[0]
		if !ok || column.Origin != attribute || column.Slot != 0 || column.Usage != owner.Columns[0].Usage || !column.Properties().RowDependent || !n.Properties().RowDependent || n.Properties().ParameterDependent || n.Properties().StageDependent || analyze.ClosedSSLInfoDefault(n) || !analyze.ValidInstalledSSLInfoShape(n) {
			t.Fatal("lost actual owner/attribute/argument properties", n, column)
		}
		canonical, ok := ext.Installation.Declarations[i+7].(*catalog.Procedure)
		if !ok || canonical == n.Procedure || canonical.Origin != nil || canonical.Address() != n.Ref.Address || canonical.Signature != n.Procedure.Signature || canonical.Result != n.Procedure.Result || canonical.Installed != n.Procedure.Installed || canonical.Display != n.Procedure.Display || n.Procedure != view.MustProcedure(ext.Members[i+7]) || n.RoutineReference != n.Procedure || n.Procedure.Origin.Installation != ext.Installation || n.Procedure.Origin.Declaration != i+7 {
			t.Fatal("lost released backing", n)
		}
		captured := make(map[ids.ObjectRef]catalog.ExpressionDependency)
		for _, dependency := range definition.Dependencies {
			root := dependency.Ref.Address
			root.SubID = 0
			descriptor := view.Get(root)
			if descriptor == nil || dependency.Ref.Domain() != view.Domain() || dependency.Ref.Epoch != view.Epoch() || dependency.Purpose != catalog.DefinitionDependency || dependency.Revision != descriptor.Revision() {
				t.Fatal("stale/foreign dependency manifest", dependency)
			}
			captured[dependency.Ref] = dependency
		}
		for _, ref := range []ids.ObjectRef{n.Ref, attribute} {
			if _, ok := captured[ref]; !ok {
				t.Fatal("lost real procedure/argument dependency", ref, definition.Dependencies)
			}
		}
		originalDependencies[i] = slices.Clone(definition.Dependencies)
		if err := stored.ValidateInstalledSSLInfo(n, view); err != nil {
			t.Fatal(err)
		}
		refs, err := stored.ExpressionDependRefs(ctx, view, n)
		if err != nil || !slices.Contains(refs, n.Ref.Address) || !slices.Contains(refs, attribute.Address) {
			t.Fatal("child dependency traversal", refs, err)
		}
		retagged, _, err := stored.RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil || !reflect.DeepEqual(retagged, n) {
			t.Fatal("same-generation retag lost real child", retagged, err)
		}
		groupBinder := enginetest.ExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
		grouped, err := groupBinder.RewriteGroup(n, []expr.Expr{column})
		if err != nil {
			t.Fatal(err)
		}
		g := grouped.(plan.InstalledSSLInfoExpr)
		group, ok := g.Arg.(plan.GroupExpr)
		if !ok || group.Slot != 0 || group.Usage != column.Usage || g.Properties().RowDependent || !g.Properties().StageDependent || !analyze.ValidInstalledSSLInfoShape(g) || g.Procedure != n.Procedure || g.Ref != n.Ref || analyze.ClosedSSLInfoDefault(g) {
			t.Fatal("group failed to replace real row child and merge properties", g)
		}
		if !analyze.ClosedPgstattupleCheck(n, view) || analyze.ClosedPgstattupleDefault(n, view) {
			t.Fatal("argument owner-row/default closure boundary")
		}
		badOwner := n
		wrongColumn := column
		wrongColumn.Origin.Address.SubID++
		badOwner.Arg = wrongColumn
		if _, err := (stored.ScalarRenderer{Owner: owner, CapturedChecks: definition.Dependencies}).SQL(ctx, view, badOwner); err == nil {
			t.Fatal("renderer accepted uncaptured owner attribute")
		}
		for _, render := range []func(catalog.Read, expr.Expr) (string, error){
			func(v catalog.Read, node expr.Expr) (string, error) {
				return (stored.ScalarRenderer{Owner: owner, CapturedChecks: definition.Dependencies}).SQL(ctx, v, node)
			},
			func(v catalog.Read, node expr.Expr) (string, error) {
				return (stored.Deparser{View: v, Relation: owner}).SQL(ctx, node, stored.DeparseParent{})
			},
		} {
			text, err := render(view, n)
			if err != nil || !strings.Contains(text, "public") || !strings.Contains(text, n.Name) || !strings.Contains(text, "field") {
				t.Fatal("renderer erased actual row argument", text, err)
			}
		}
		calls = append(calls, n)
	}
	b := enginetest.ExpressionBinder(t, ctx, source, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	node, err := b.BindFunctionProvider(enginetest.Statement(t, "SELECT ssl_extension_info()").(*parser.Select).Targets[0].(parser.FunctionExpr))
	if err != nil {
		t.Fatal(err)
	}
	calls = append(calls, node.(plan.InstalledSSLInfoExpr))
	foreign := engine.New()
	foreignText, _ := foreign.CatalogRead().LookupType(sqltype.Text)
	for i, n := range calls {
		for _, mutate := range []func(*plan.InstalledSSLInfoExpr){
			func(n *plan.InstalledSSLInfoExpr) { n.Ref.Epoch = 0 },
			func(n *plan.InstalledSSLInfoExpr) { n.Ref.Epoch++ },
			func(n *plan.InstalledSSLInfoExpr) { n.Ref = new(ids.Domain).Ref(n.Ref.Epoch, n.Ref.Address) },
			func(n *plan.InstalledSSLInfoExpr) { n.RoutineReference = nil },
			func(n *plan.InstalledSSLInfoExpr) { n.Property.RowDependent = !n.Property.RowDependent },
			func(n *plan.InstalledSSLInfoExpr) { n.Property.ParameterDependent = !n.Property.ParameterDependent },
			func(n *plan.InstalledSSLInfoExpr) {
				p := *n.Procedure
				origin := *p.Origin
				origin.Declaration = 6
				p.Origin = &origin
				n.Procedure, n.RoutineReference = &p, &p
			},
			func(n *plan.InstalledSSLInfoExpr) {
				p := *n.Procedure
				p.Signature = "16"
				n.Procedure, n.RoutineReference = &p, &p
			},
			func(n *plan.InstalledSSLInfoExpr) {
				p := *n.Procedure
				installation := *p.Origin.Installation
				origin := *p.Origin
				origin.Installation = &installation
				p.Origin = &origin
				n.Procedure, n.RoutineReference = &p, &p
			},
		} {
			bad := n
			mutate(&bad)
			if stored.ValidateInstalledSSLInfo(bad, view) == nil {
				t.Fatal("forged certificate capture accepted", i, bad)
			}
		}
		if i < 2 {
			bad := n
			column := bad.Arg.(plan.ColumnExpr)
			column.Usage = foreignText.Usage()
			bad.Arg = column
			if stored.ValidateInstalledSSLInfo(bad, view) == nil {
				t.Fatal("foreign same-OID argument generation admitted")
			}
			bad = n
			bad.Usage = foreignText.Usage()
			if stored.ValidateInstalledSSLInfo(bad, view) == nil {
				t.Fatal("foreign same-OID result generation admitted")
			}
		} else {
			for _, change := range []string{"name", "order", "foreign_usage", "attribute"} {
				members := make([]sqltype.CompositeMember, 3)
				for j := range members {
					members[j] = n.Usage.Type.CompositeMember(j)
				}
				switch change {
				case "name":
					members[0].Name = "other"
				case "order":
					members[0], members[1] = members[1], members[0]
				case "foreign_usage":
					members[0].Usage, members[1].Usage = foreignText.Usage(), foreignText.Usage()
				case "attribute":
					members[0].Attribute = 9
				}
				shape, err := sqltype.RecordShape(members)
				if err != nil {
					t.Fatal(err)
				}
				bad := n
				bad.Usage = shape.Usage()
				if stored.ValidateInstalledSSLInfo(bad, view) == nil {
					t.Fatal("corrupt OUT shape/generation admitted", change)
				}
			}
		}
	}
	// Rename/move both a scalar and the SRF in an immutable candidate snapshot.
	state := catalog.CloneState(view.State())
	for _, index := range []int{0, 2} {
		n := calls[index]
		changed := *n.Procedure
		changed.ObjectName = []string{"renamed_ssl_field", "", "renamed_ssl_extensions"}[index]
		changed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
		state.UnbindName(catalog.DescriptorNameKey(n.Procedure))
		state.SetDescriptor(changed.Address(), &changed)
		state.BindName(catalog.DescriptorNameKey(&changed), changed.Address())
		renamed := n
		renamed.Procedure, renamed.RoutineReference = &changed, &changed
		current := view.WithState(state)
		if err := stored.ValidateInstalledSSLInfo(renamed, current); err != nil {
			t.Fatal("current-name identity", err)
		}
		var text string
		var err error
		if index == 0 {
			text, err = (stored.ScalarRenderer{Owner: owner, CapturedChecks: owner.Constraints.Check[0].Dependencies}).SQL(ctx, current, renamed)
		} else {
			// The SRF is not a CHECK expression and has no owner-check manifest.
			text, err = (stored.Deparser{View: current, Relation: owner}).SQL(ctx, renamed, stored.DeparseParent{})
		}
		if err != nil || !strings.Contains(text, "pg_catalog") || !strings.Contains(text, changed.Name()) || strings.Contains(text, n.Name) || index == 0 && !strings.Contains(text, "field") {
			t.Fatal("current qualified call/argument rendering", text, err)
		}
	}
	// Retained TEMP consumers force a genuine destination identity remap.
	target := engine.New()
	xml2UnitRetainTemporaryCollisions(t, target, ext.Members)
	if _, err := target.ReplaceCatalog(ctx, source, view); err != nil {
		t.Fatal("destination replacement", err)
	}
	destination := target.CatalogRead()
	imported := destination.MustExtension(destination.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "sslinfo"}))
	importedOwner := destination.MustRelation(destination.NameAddress(catalog.DescriptorNameKey(owner)))
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): imported.Address(), owner.Address(): importedOwner.Address()}
	for slot, address := range ext.Members {
		mapping[address] = imported.Members[slot]
	}
	mapper := stored.ViewQueryRemapper{
		Catalog: destination,
		Type: analyze.RecordTypeMapper(func(d *sqltype.Descriptor) *sqltype.Descriptor {
			if d == nil {
				return nil
			}
			next, _ := destination.LookupType(d.ID())
			return next
		}),
		Ref: func(ref ids.ObjectRef) ids.ObjectRef {
			address := ref.Address
			attribute := address.SubID
			address.SubID = 0
			if next, ok := mapping[address]; ok {
				address = next
			}
			address.SubID = attribute
			return destination.Domain().Ref(destination.Epoch(), address)
		},
		Procedure: extension.ProcedureRemapper(extension.DescriptorRemapper(mapping), replacementProcedurePolicyFixture(source, target), nil),
	}
	for i, n := range calls {
		if imported.Members[i+7] == n.Ref.Address {
			t.Fatal("fixture failed to force remapping")
		}
		query, err := mapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{n}}})
		if err != nil {
			t.Fatal("actual child/record remap", err)
		}
		mapped := query.(*plan.ValuesNode).Rows[0][0].(plan.InstalledSSLInfoExpr)
		if mapped.Ref.Address != imported.Members[i+7] || mapped.Ref.Domain() != destination.Domain() || mapped.Ref.Epoch != destination.Epoch() || mapped.Procedure == n.Procedure || mapped.RoutineReference != mapped.Procedure || !exec.SameRoutineDeclaration(destination.MustProcedure(imported.Members[i+7]), mapped.Procedure) || mapped.Properties() != n.Properties() || !analyze.ValidInstalledSSLInfoShape(mapped) {
			t.Fatal("destination authority/property mismatch", mapped)
		}
		if i < 2 {
			currentText, _ := destination.LookupType(sqltype.Text)
			column, ok := mapped.Arg.(plan.ColumnExpr)
			if !ok || column.Origin.Domain() != destination.Domain() || column.Origin.Epoch != destination.Epoch() || column.Origin.Address.ObjectID != importedOwner.Address().ObjectID || column.Origin.Address.SubID != importedOwner.Attributes[0] || column.Usage != importedOwner.Columns[0].Usage || column.Usage != currentText.Usage() || mapped.Usage != currentText.Usage() || !column.Properties().RowDependent {
				t.Fatal("remap erased actual destination child owner/Usage", mapped.Arg)
			}
		} else {
			if mapped.Arg != nil || !mapped.Procedure.Installed.ReturnsSet || mapped.Procedure.Installed.Outputs == nil || mapped.Procedure.Installed.Outputs.Len() != 3 || mapped.Usage.Type.CompositeLen() != 3 {
				t.Fatal("destination lost fixed OUT declaration", mapped)
			}
			for j, name := range []string{"name", "value", "critical"} {
				field := mapped.Usage.Type.CompositeMember(j)
				current, _ := destination.LookupType([]sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Bool}[j])
				if field.Name != name || field.Attribute != int32(j+1) || field.Usage != current.Usage() || mapped.Procedure.Installed.Outputs.Name(j) != name || mapped.Procedure.Installed.Outputs.Type(j) != ids.OID(current.ID()) {
					t.Fatal("destination record member/OUT generation", field)
				}
			}
		}
		if err := stored.ValidateInstalledSSLInfo(mapped, destination); err != nil {
			t.Fatal(err)
		}
		if stored.ValidateInstalledSSLInfo(n, destination) == nil || stored.ValidateInstalledSSLInfo(mapped, view) == nil {
			t.Fatal("source/destination authority domains interchanged")
		}
	}
	if source.CatalogRead().State() != view.State() {
		t.Fatal("destination mutated source snapshot")
	}
	for i, dependencies := range originalDependencies {
		if !reflect.DeepEqual(owner.Constraints.Check[i].Dependencies, dependencies) || !reflect.DeepEqual(owner.Constraints.Check[i].Expression.(plan.NullTestExpr).Child, calls[i]) {
			t.Fatal("source argument/manifest backing mutated")
		}
	}
}

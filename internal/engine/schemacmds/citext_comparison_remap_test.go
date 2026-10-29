package schemacmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestCitextComparisonStoredRemap(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	source, sourceExtension, sourceType := citextUnitInstalled(t)
	sourceView := source.CatalogRead()
	value, err := sourceType.SQLType.Input(ctx, "ABC", -1)
	if err != nil {
		t.Fatal(err)
	}
	// Retain a genuine TEMP provider so replacement must remap imported identities.
	target := engine.New()
	owner, closeOwner := extensionTemporaryOperation(t, target, "postgres")
	t.Cleanup(closeOwner)
	namespace := intarrayUnitTemporaryNamespace(t, target, owner)
	installer, closeInstaller := extensionTemporaryOperation(t, target, "postgres")
	extensionUnitPublish(t, target, installer, citextUnitStage(t, target, installer, "unaccent", namespace.Name()))
	closeInstaller()
	if _, err := target.ReplaceCatalog(ctx, source, sourceView); err != nil {
		t.Fatal(err)
	}
	view := target.CatalogRead()
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "citext"}))
	imported := view.MustType(installation.Members[0])
	if imported.SQLType.ID() == sourceType.SQLType.ID() {
		t.Fatal("fixture did not remap the citext signature type")
	}
	published := view.State()
	if len(published.ScalarImports()) != 0 {
		t.Fatal("publication retained candidate-only scalar import authority")
	}
	// ReplaceCatalog has already published and cleared its temporary scalar
	// translations. Retagging an independent source literal requires the same
	// explicit source/destination authority on an unpublished candidate.
	candidate := catalog.CloneState(published)
	candidate.SetScalarImport(sourceType.SQLType, imported.SQLType)
	view = view.WithState(candidate)
	mapType := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		if old == sourceType.SQLType {
			return imported.SQLType
		}
		current, _ := view.LookupType(old.ID())
		return current
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{sourceExtension.Address(): installation.Address()}
	for i, address := range sourceExtension.Members {
		mapping[address] = installation.Members[i]
	}
	mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sourceType.Array)}] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(imported.Array)}
	mapper := stored.ViewQueryRemapper{
		Catalog: view, Type: mapType,
		Ref: func(ref ids.ObjectRef) ids.ObjectRef {
			address := ref.Address
			if mapped, ok := mapping[address]; ok {
				address = mapped
			}
			return view.Owner().Domain.Ref(view.Epoch(), address)
		},
		Procedure: extension.ProcedureRemapper(extension.DescriptorRemapper(mapping), replacementProcedurePolicyFixture(source, target), nil),
	}
	mapOriginal := func(node expr.Expr) (expr.Expr, error) {
		query, err := mapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{node}}})
		if err != nil {
			return nil, err
		}
		return query.(*plan.ValuesNode).Rows[0][0], nil
	}
	for kind, name := range []string{"citext_eq", "citext_ne", "citext_lt", "citext_le", "citext_gt", "citext_ge"} {
		t.Run(name, func(t *testing.T) {
			captured := sourceView.MustProcedure(sourceExtension.Members[3+kind])
			current := view.MustProcedure(installation.Members[3+kind])
			if captured.Name() != name || current.Name() != name {
				t.Fatal("wrong selected member", name, captured.Name(), current.Name())
			}
			result, _ := sourceView.LookupType(sqltype.ID(captured.Result))
			comparison, err := sqltype.BindCitextComparison(sqltype.ComparisonOperator(kind), sourceType.SQLType, sourceType.SQLType, result, sourceType.SQLType.Usage().Collation, false)
			if err != nil {
				t.Fatal(err)
			}
			literal := plan.LiteralExpr{Info: expr.Info{Usage: sourceType.SQLType.Usage()}, Value: value}
			original := plan.CitextComparisonExpr{
				Info: expr.Info{Usage: result.Usage(), RoutineReference: captured, Property: expr.Properties{Volatility: expr.Immutable}},
				Ref:  sourceView.Owner().Domain.Ref(sourceView.Epoch(), captured.Address()),
				Args: [2]expr.Expr{literal, literal}, Comparison: comparison, Kind: uint8(kind), Namespace: "public", Collation: sourceType.SQLType.Usage().Collation,
			}
			mappedRef := view.Owner().Domain.Ref(view.Epoch(), current.Address())
			relocated, err := stored.RemapExpression(original, func(ref ids.ObjectRef) ids.ObjectRef {
				if ref != original.Ref {
					t.Fatal("unexpected routine reference", ref)
				}
				return mappedRef
			}, nil)
			if err != nil || relocated.(plan.CitextComparisonExpr).Ref != mappedRef {
				t.Fatal("selected routine reference relocation", err)
			}
			if _, _, err := stored.RetagTypes(view.WithState(published), relocated, mapType); err == nil {
				t.Fatal("descriptor mapping alone authorized a cross-ID source literal")
			}
			rebound, err := mapOriginal(original)
			if err != nil {
				t.Fatal("mapped citext declaration and signature", err)
			}
			mapped := rebound.(plan.CitextComparisonExpr)
			mappedRoutine, ok := mapped.RoutineReference.(*catalog.Procedure)
			if !ok || mapped.Ref != mappedRef || !exec.SameRoutineDeclaration(current, mappedRoutine) || mappedRoutine.Revision() != captured.Revision() || mapped.Kind != original.Kind || mapped.Properties() != original.Properties() || mapped.SourcePosition() != original.SourcePosition() {
				t.Fatal("lost selected routine capture", mapped)
			}
			for _, argument := range mapped.Args {
				if argument.ResultUsage().Type != imported.SQLType || argument.(plan.LiteralExpr).Value.Type() != imported.SQLType {
					t.Fatal("retained a source argument type", argument)
				}
			}
			want := kind == 0 || kind == 3 || kind == 5
			if n, err := mapped.Comparison.Evaluate(ctx, mapped.Args[0].(plan.LiteralExpr).Value, mapped.Args[1].(plan.LiteralExpr).Value); err != nil || n != sqltype.Boolean(mapped.ResultUsage().Type, want) {
				t.Fatal("remapped typed comparison", n, err)
			}
			if original.Ref == mappedRef || original.RoutineReference != captured || original.Args[0].ResultUsage().Type != sourceType.SQLType || original.Args[0].(plan.LiteralExpr).Value != value || captured.Signature != extension.CitextComparisonSignature(sourceType.SQLType.ID()) {
				t.Fatal("remapping changed the captured source")
			}
			missing := relocated.(plan.CitextComparisonExpr)
			missing.Ref = ids.ObjectRef{}
			if _, _, err := stored.RetagTypes(view, missing, mapType); err == nil {
				t.Fatal("recovered a missing mapped identity from its name or source")
			}
			if _, _, err := stored.RetagTypes(catalog.NewRead(nil, view.State()), relocated, mapType); err == nil {
				t.Fatal("admitted a routine without registered catalog authority")
			}
			for _, signature := range []catalog.ProcedureSignature{
				extension.CitextSignature(imported.SQLType.ID()) + ",25",
				"25," + extension.CitextSignature(imported.SQLType.ID()),
			} {
				candidate := catalog.CloneState(view.State())
				wrong := *current
				wrong.Signature = signature
				candidate.SetDescriptor(wrong.Address(), &wrong)
				badMapper := mapper
				badMapper.Catalog = view.WithState(candidate)
				if _, err := badMapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{original}}}); err == nil {
					t.Fatal("admitted an incorrect mapped signature slot", signature)
				}
			}
		})
	}
}

// replacementProcedurePolicyFixture supplies the real host policy for private
// address/type remap fixtures; it does not manufacture numeric principal links.
func replacementProcedurePolicyFixture(source, target *core.Engine) catalog.ProcedureRemapper {
	sourceRoles := source.RoleStore.PublishedRead(&source.Catalog.Owner, source.Catalog.Current())
	targetRoles := target.RoleStore.PublishedRead(&target.Catalog.Owner, target.Catalog.Current())
	return catalog.NewProcedureRemapper(target.RoleStore.PrincipalRemap(&target.Catalog, sourceRoles, targetRoles, nil, routine.OperatorSeeded))
}

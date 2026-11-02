package enginetests_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// The same selected family name in the index owner covers roots; here it owns
// the actual callable's immutable typed expression and remapped declaration.
func TestBtreeGINCompoundBinding(t *testing.T) {
	ctx := context.Background()
	source := engine.New()
	for _, q := range []string{"CREATE EXTENSION btree_gin VERSION '1.3'", "CREATE TYPE compound_expr_enum AS ENUM('z','a','m')", "ALTER TYPE compound_expr_enum ADD VALUE 'q' BEFORE 'a'"} {
		enginetest.Run(t, source, q)
	}
	view := source.CatalogRead()
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "btree_gin"}))
	procedure := view.MustProcedure(installation.Installation.Members[1])
	typ := view.MustType(view.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "compound_expr_enum"}))
	integer, _ := view.LookupType(sqltype.Int4)
	leftValue, err := typ.SQLType.Input(ctx, "q", -1)
	if err != nil {
		t.Fatal(err)
	}
	rightValue, err := typ.SQLType.Input(ctx, "a", -1)
	if err != nil {
		t.Fatal(err)
	}
	left := plan.LiteralExpr{Info: expr.Info{Usage: typ.SQLType.Usage()}, Value: leftValue}
	right := plan.LiteralExpr{Info: left.Info, Value: rightValue}
	comparison, err := sqltype.BindComparison(typ.SQLType, typ.SQLType, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	original := plan.GinEnumCompareExpr{Info: expr.Info{Usage: integer.Usage(), RoutineReference: procedure, Property: expr.Properties{Volatility: expr.Immutable}}, Ref: view.Owner().Domain.Ref(view.Epoch(), procedure.Address()), Args: [2]expr.Expr{left, right}, Comparison: comparison, Namespace: "public"}
	if err := original.ValidateStoredProvider(); err != nil {
		t.Fatal(err)
	}
	operands, ok, err := original.Capture(expr.CaptureFunction)
	if err != nil || !ok || operands.Count != 2 || !operands.SelectedImplicit || operands.Inline[0] != original.Args[0] || operands.Inline[1] != original.Args[1] {
		t.Fatal("callable capture lost exact original occurrences", operands, ok, err)
	}
	rebuilt, err := original.Rebuild(original.Info, []expr.Expr{right, left})
	if err != nil {
		t.Fatal(err)
	}
	reverse := rebuilt.(plan.GinEnumCompareExpr)
	if reverse.RoutineReference != procedure || reverse.Ref != original.Ref || reverse.Args[0] != original.Args[1] || original.Args[0] != left || original.Args[1] != right {
		t.Fatal("rebuild mutated original or lost selected authority")
	}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: view}}
	value, err := reverse.Eval(evaluation)
	sign, failure := value.Integer()
	if err != nil || failure != nil || sign != 1 {
		t.Fatal("rebound typed comparator did not use enum rank", sign, err, failure)
	}
	bad := right
	bad.Info.Usage = integer.Usage()
	bad.Value, _ = sqltype.Integer(integer, 1)
	if _, err := original.Rebuild(original.Info, []expr.Expr{left, bad}); err == nil {
		t.Fatal("rebuild accepted non-enum argument")
	}
	// Real Dump/Restore produces destination enum/procedure generations. The
	// separate public test owns its SQL and metadata assertions.
	var dump bytes.Buffer
	if err := engine.Dump(ctx, source, &dump); err != nil {
		t.Fatal(err)
	}
	target := engine.New()
	if err := engine.Restore(ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	currentView := target.CatalogRead()
	currentInstallation := currentView.MustExtension(currentView.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "btree_gin"}))
	currentProcedure := currentView.MustProcedure(currentInstallation.Installation.Members[1])
	currentType := currentView.MustType(currentView.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "compound_expr_enum"}))
	if currentType.SQLType == typ.SQLType || currentInstallation.Installation.IndexAuthority() == installation.Installation.IndexAuthority() {
		t.Fatal("restore retained source concrete generation/installation authority")
	}
	state := catalog.CloneState(currentView.State())
	state.SetScalarImport(typ.SQLType, currentType.SQLType)
	mappedView := currentView.WithState(state)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{installation.Address(): currentInstallation.Address(), typ.Address(): currentType.Address(), {ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Array)}: {ClassID: ids.TypeClassID, ObjectID: ids.OID(currentType.Array)}}
	for i, address := range installation.Installation.Members {
		mapping[address] = currentInstallation.Installation.Members[i]
	}
	mapType := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		if old == typ.SQLType {
			return currentType.SQLType
		}
		current, _ := mappedView.LookupType(old.ID())
		return current
	}
	mapper := stored.ViewQueryRemapper{Catalog: mappedView, Type: mapType, Ref: func(ref ids.ObjectRef) ids.ObjectRef {
		address := ref.Address
		if mapped, ok := mapping[address]; ok {
			address = mapped
		}
		return mappedView.Owner().Domain.Ref(mappedView.Epoch(), address)
	}, Procedure: extension.ProcedureRemapper(extension.DescriptorRemapper(mapping), enginetest.ReplacementProcedurePolicyFixture(source, target), nil)}
	query, err := mapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{original}}})
	if err != nil {
		t.Fatal("original declaration/type remap", err)
	}
	mapped := query.(*plan.ValuesNode).Rows[0][0].(plan.GinEnumCompareExpr)
	mappedProcedure, ok := mapped.RoutineReference.(*catalog.Procedure)
	if !ok || mapped.Ref.Domain() != currentView.Owner().Domain || mapped.Ref.Address != currentProcedure.Address() || !exec.SameRoutineDeclaration(currentProcedure, mappedProcedure) || mappedProcedure.Revision() != procedure.Revision() || mapped.Properties() != original.Properties() {
		t.Fatal("remap lost original callable provenance", mapped)
	}
	for _, arg := range mapped.Args {
		literal := arg.(plan.LiteralExpr)
		if literal.ResultUsage().Type != currentType.SQLType || literal.Value.Type() != currentType.SQLType {
			t.Fatal("remap retained source operand generation")
		}
	}
	if _, err := original.Comparison.Compare(ctx, mapped.Args[0].(plan.LiteralExpr).Value, mapped.Args[1].(plan.LiteralExpr).Value, sqltype.InputEnvironment{}); err == nil {
		t.Fatal("original comparison accepted destination operands")
	}
	if sign, err := mapped.Comparison.Compare(ctx, mapped.Args[0].(plan.LiteralExpr).Value, mapped.Args[1].(plan.LiteralExpr).Value, sqltype.InputEnvironment{}); err != nil || sign >= 0 {
		t.Fatal("destination comparison was not rebound", sign, err)
	}
	if original.RoutineReference != procedure || original.Args[0].ResultUsage().Type != typ.SQLType || original.Args[0].(plan.LiteralExpr).Value != leftValue || original.Ref.Domain() == mapped.Ref.Domain() {
		t.Fatal("remap mutated retained original")
	}
	missing := mapped
	missing.Ref = ids.ObjectRef{}
	if _, _, err := stored.RetagTypes(mappedView, missing, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }); err == nil {
		t.Fatal("retag recovered missing original authority from SQL names")
	}
}

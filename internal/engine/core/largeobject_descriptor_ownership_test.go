package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestLargeObjectDescriptorRecipeOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
	parse := func() {
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("lo_recipe", "SELECT loread(0,COALESCE(-(7::integer),0))", nil)
			return err
		})
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "lo_recipe", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	parse()
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("lo_recipe")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil { t.Fatal("missing genuine definition ownership") }
	owner := definition.Description.Folds
	if owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 { t.Fatal("Parse evaluated descriptor operands") }
	command(func(scope *core.StatementScope) error { _, err := scope.DescribeProtocolStatement("lo_recipe"); return err })
	if definition.Description.Folds != owner || owner.Current().Generation != 0 { t.Fatal("Describe replaced owner or evaluated call") }
	bind("lo_first")
	first := registry.Binding("lo_first")
	accepted := owner.Current()
	if first == nil || first.Folds == nil || first.Resource == nil || first.Resource.Root == nil || len(accepted.Entries) == 0 || first.Entry.Description.Folds != owner || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] { t.Fatal("Bind lost actual accepted generation/backing") }
	root := first.Resource.Root
	if len(root.Expressions) != 1 { t.Fatal("missing scalar descriptor recipe") }
	node, ok := root.Expressions[0].(functions.LargeObjectCall)
	if !ok || node.Name != "loread" || node.PlanningFold() != expr.FoldNever || node.Properties().Volatility != expr.Volatile { t.Fatal("descriptor call folded or replaced") }
	declaration, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || declaration == nil || declaration.Address().ObjectID != 954 { t.Fatal("provider incarnation lost", err) }
	if _, err := exec.ValidateStoredRoutineDescriptor(e.CatalogRead(), declaration); err != nil { t.Fatal("provider incarnation lost", err) }
	if len(accepted.Dependencies) == 0 { t.Fatal("missing accepted provider dependencies") }
	for _, dependency := range accepted.Dependencies {
		if dependency.Ref.Epoch != e.Catalog.State.Epoch() { t.Fatal("accepted provider crossed incarnation") }
	}
	for _, entry := range accepted.Entries {
		if entry.Callable != nil && entry.Callable.RoutineOID() >= 952 && entry.Callable.RoutineOID() <= 955 {
			t.Fatal("retained descriptor runtime result", entry)
		}
	}
	// The accepted unary computation owns its original provenance and inputs.
	// COALESCE then replaces its executable child with a literal carrying the
	// conditional's provenance, not the unary occurrence. Check those two real
	// stages separately, linking their immutable datum rather than their stamps.
	child := preparedFoldEntry(t, definition.Description, sqltype.Int4, "-7")
	if child.Source == (expr.FoldSourceOccurrence{}) || child.Frame != child.Source.Frame || len(child.Inputs) == 0 {
		t.Fatal("retained unary child lost source provenance or inputs", child)
	}
	provider, ok := child.Callable.(*catalog.Procedure)
	if !ok || provider.Volatility != expr.Immutable {
		t.Fatal("retained unary child lost immutable provider", child.Callable)
	}
	if _, err := exec.ValidateStoredRoutineDescriptor(e.CatalogRead(), provider); err != nil {
		t.Fatal("retained child provider crossed incarnation", err)
	}
	foundInput := false
	for _, input := range child.Inputs {
		if input.Value.Type() != nil {
			value, err := input.Value.Integer()
			if err == nil && value == 7 { foundInput = true }
		}
	}
	if !foundInput { t.Fatal("retained unary child lost original input", child.Inputs) }
	literal, ok := node.Args[1].(plan.LiteralExpr)
	if !ok || literal.ResultUsage() != child.Result || !sqltype.SameDatum(literal.Value, child.Value) {
		t.Fatal("executable conditional lost retained child datum", node.Args[1])
	}
	childIndex := -1
	for i, entry := range accepted.Entries {
		if samePublishedFold(entry, child) { childIndex = i; break }
	}
	if childIndex < 0 { t.Fatal("child absent from accepted generation") }
	bind("lo_second")
	second := registry.Binding("lo_second")
	if second == first || second.Resource == first.Resource || second.Resource.Root == root || second.Folds.Generation != accepted.Generation || &second.Folds.Entries[0] != &accepted.Entries[0] { t.Fatal("independent binding lost shared immutable backing or private graph") }
	if !samePublishedFold(second.Folds.Entries[childIndex], child) || &second.Folds.Entries[childIndex].Inputs[0] != &accepted.Entries[childIndex].Inputs[0] {
		t.Fatal("second binding changed child provenance or immutable input backing")
	}
	secondNode, ok := second.Resource.Root.Expressions[0].(functions.LargeObjectCall)
	if !ok { t.Fatal("second binding lost descriptor recipe") }
	secondLiteral, ok := secondNode.Args[1].(plan.LiteralExpr)
	if !ok || !sqltype.SameDatum(secondLiteral.Value, child.Value) {
		t.Fatal("second executable graph lost accepted child datum")
	}
	publication, err := core.BeginFoldPublication(&definition.Description, definition.Body, owner.Environment, e.CatalogRead(), nil)
	if err != nil || publication == nil { t.Fatal("missing real publication attempt", err) }
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("lo_recipe") })
	if owner.Live() || first.Resource.Root != root || first.Folds.Generation != accepted.Generation { t.Fatal("definition retirement mutated accepted graph") }
	retired := owner.Current()
	if err := publication.Accept(context.Background(), e.CatalogRead(), owner.Environment, e.Catalog.State.Epoch()); !errors.Is(err, ids.ErrCatalogReplaced) { t.Fatal("retired definition accepted publication", err) }
	if owner.Current().Generation != retired.Generation || len(owner.Current().Entries) != len(retired.Entries) || first.Folds.Generation != accepted.Generation || second.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] || &second.Folds.Entries[0] != &accepted.Entries[0] || !samePublishedFold(first.Folds.Entries[childIndex], child) {
		t.Fatal("rejected publication changed retired owner or retained portal backing")
	}
	parse()
	if registry.Lookup("lo_recipe").Description.Folds == owner { t.Fatal("new definition adopted retired runtime owner") }
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("lo_first") })
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("lo_second") })
	enginetest.SessionSQL(t, s, "ROLLBACK")
}

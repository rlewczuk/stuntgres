package core_test

import (
	"context"
	"errors"
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Conditional demand and ordinary installed-call preflight share the accepted
// immutable fold owner while retaining their own expression provenance.
func TestPgcryptoArmorConditionalAcceptedOwner(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION pgcrypto")
	transaction := enginetest.TransactionBegin(t, s)
	defer enginetest.TransactionEnd(t, s, transaction, false)
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) {
		enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn)
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("armor_owned", "SELECT COALESCE(armor('test'::bytea),'') AS value", nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("armor_owned")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing actual registered armor definition")
	}
	owner := definition.Description.Folds
	if !owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("analysis published codec work or lost definition ownership")
	}
	bind := func(name string) *core.PreparedBinding {
		t.Helper()
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "armor_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		binding := registry.Binding(name)
		if binding == nil || binding.Entry.Description.Folds != owner || binding.Folds == nil || binding.Resource == nil || binding.Resource.Root == nil {
			t.Fatal("missing actual accepted armor binding")
		}
		return binding
	}
	first := bind("armor_first")
	golden := "-----BEGIN PGP MESSAGE-----\n\ndGVzdA==\n=+G7Q\n-----END PGP MESSAGE-----\n"
	entry := enginetest.PreparedFoldEntry(t, definition.Description, 25, golden)
	before := enginetest.SnapshotFoldDescription(definition.Description)
	view := e.CatalogRead()
	extension := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pgcrypto"}))
	if entry.Callable == nil || ids.OID(entry.Callable.RoutineOID()) != extension.Members[14].ObjectID || entry.Value.Type() != entry.Result.Type || entry.Source == (expr.FoldSourceOccurrence{}) || entry.Occurrence == 0 || len(entry.Inputs) != 1 {
		t.Fatal("accepted armor lost exact installed overload/generation", entry)
	}
	raw, err := entry.Inputs[0].Value.Binary()
	if err != nil || raw != "test" || entry.Inputs[0].Usage.Type.ID() != sqltype.Bytea {
		t.Fatal("accepted conditional armor lost its original typed input", raw, err)
	}
	firstLiteral, ok := first.Resource.Root.Expressions[0].(plan.LiteralExpr)
	if !ok || firstLiteral.Source == (expr.FoldSourceOccurrence{}) || firstLiteral.Occurrence == 0 {
		t.Fatal("conditional result lost its own source provenance")
	}
	assertGraph := func(root *plan.ProjectNode, saved plan.LiteralExpr) {
		t.Helper()
		if root == nil || len(root.Expressions) != 1 {
			t.Fatal("missing actual private armor graph")
		}
		literal, ok := root.Expressions[0].(plan.LiteralExpr)
		if !ok || !sqltype.SameDatum(literal.Value, entry.Value) || literal.ResultUsage() != entry.Result || literal.Info != saved.Info {
			t.Fatal("accepted graph lost exact armor datum/provenance")
		}
	}
	firstRoot := first.Resource.Root
	firstFolds := *first.Folds
	if firstFolds.Generation != before.Accepted.Generation || &firstFolds.Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("first binding copied accepted definition backing")
	}
	assertGraph(firstRoot, firstLiteral)
	enginetest.SessionSQL(t, s, "SET extra_float_digits=-3")
	second := bind("armor_second")
	secondRoot := second.Resource.Root
	secondLiteral, ok := secondRoot.Expressions[0].(plan.LiteralExpr)
	if !ok || secondLiteral.Source == (expr.FoldSourceOccurrence{}) || secondLiteral.Occurrence == 0 {
		t.Fatal("second conditional result lost its own source provenance")
	}
	secondFolds := *second.Folds
	after := enginetest.PreparedFoldEntry(t, definition.Description, 25, golden)
	current := owner.Current()
	if registry.Lookup("armor_owned") != definition || definition.Description.Folds != owner || current.Generation != before.Accepted.Generation || &current.Entries[0] != &before.Accepted.Entries[0] || !enginetest.SamePublishedFold(entry, after) || !sqltype.SameDatum(entry.Value, after.Value) || &entry.Inputs[0] != &after.Inputs[0] {
		t.Fatal("rebinding replaced accepted definition backing")
	}
	if second == first || secondRoot == firstRoot || second.Resource == first.Resource || secondFolds.Generation != before.Accepted.Generation || &secondFolds.Entries[0] != &before.Accepted.Entries[0] || first.Resource.Root != firstRoot {
		t.Fatal("bindings copied immutable backing or shared mutable resources")
	}
	assertGraph(firstRoot, firstLiteral)
	assertGraph(secondRoot, secondLiteral)
	func() {
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		environment, err := e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			t.Fatal(err)
		}
		publication, err := core.BeginFoldPublication(&definition.Description, definition.Body, environment, auth.Catalog, nil)
		if err != nil || publication == nil {
			t.Fatal("missing actual armor publication", err)
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		err = publication.Accept(canceled, auth.Catalog, environment, e.Catalog.State.Epoch())
		current := owner.Current()
		if !errors.Is(err, context.Canceled) || publication.Group.Sealed || current.Generation != before.Accepted.Generation || &current.Entries[0] != &before.Accepted.Entries[0] || first.Resource.Root != firstRoot || second.Resource.Root != secondRoot {
			t.Fatal("rejected publication mutated accepted armor owner", err)
		}
	}()
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("armor_rejected", "SELECT COALESCE(armor('test'::bytea),''),COALESCE(dearmor('bad'),''::bytea)", nil)
		return err
	})
	bad := registry.Lookup("armor_rejected")
	if bad == nil || bad.Description.Folds == nil || len(bad.Description.Folds.Current().Entries) != 0 {
		t.Fatal("missing actual unplanned sibling definition")
	}
	err = enginetest.PreparedFoldProtocolTry(t, e, s, cycle, false, func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("armor_failed", "armor_rejected", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "39000" || diagnostic.Message != "Corrupt ascii-armor" {
		t.Fatal("missing genuine failed sibling codec planning", err)
	}
	if registry.Binding("armor_failed") != nil || len(bad.Description.Folds.Current().Entries) != 0 || bad.Description.Folds.Current().Generation != 0 {
		t.Fatal("failed sibling published partial codec backing")
	}
	current = owner.Current()
	if registry.Lookup("armor_owned") != definition || definition.Description.Folds != owner || current.Generation != before.Accepted.Generation || &current.Entries[0] != &before.Accepted.Entries[0] || !enginetest.SamePublishedFold(entry, enginetest.PreparedFoldEntry(t, before, 25, golden)) {
		t.Fatal("rejected sibling replaced retained definition")
	}
	// Retain the real accepted payloads and graphs independently of any live
	// producer cleanup caused by the failed command.
	if firstFolds.Generation != before.Accepted.Generation || &firstFolds.Entries[0] != &before.Accepted.Entries[0] || secondFolds.Generation != before.Accepted.Generation || &secondFolds.Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("failed sibling changed accepted binding backing")
	}
	assertGraph(firstRoot, firstLiteral)
	assertGraph(secondRoot, secondLiteral)
}

func TestPgcryptoArmorAcceptedOwner(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION pgcrypto")
	transaction := enginetest.TransactionBegin(t, s)
	defer enginetest.TransactionEnd(t, s, transaction, false)
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) {
		enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn)
	}
	parse := func() {
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("armor_recipe", "SELECT armor('test'::bytea) AS value", nil)
			return err
		})
	}
	parse()
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("armor_recipe")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing actual installed scalar definition")
	}
	owner, analysis := definition.Description.Folds, definition.Description.Analysis
	dependencies := analysis.Dependencies
	body, source := definition.Body, definition.Source
	if !owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 || len(dependencies) == 0 {
		t.Fatal("Parse evaluated the scalar or lost its analysis owner")
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.DescribeProtocolStatement("armor_recipe")
		return err
	})
	// Describe publishes refreshed immutable analysis while preserving the
	// definition's fold owner; retain the analysis actually consumed by Bind.
	analysis = definition.Description.Analysis
	dependencies = analysis.Dependencies
	bind := func(name string) *core.PreparedBinding {
		t.Helper()
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "armor_recipe", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		binding := registry.Binding(name)
		if binding == nil || binding.Entry.Description.Folds != owner || binding.Entry.Description.Analysis != analysis || binding.Folds == nil || binding.Resource == nil || binding.Resource.Root == nil {
			t.Fatal("missing genuine owner-bound private scalar binding")
		}
		return binding
	}
	view := e.CatalogRead()
	if definition.Description.CatalogOwner != view.Owner() || definition.Description.CatalogEpoch != view.Epoch() || definition.Description.PlanOwner != s.SessionToken || analysis.Domain != e.Catalog.Domain || analysis.Epoch != view.Epoch() || source != "SELECT armor('test'::bytea) AS value" {
		t.Fatal("definition lost its actual source or catalog/session incarnation")
	}
	installed := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pgcrypto"}))
	routine := view.MustProcedure(installed.Members[14])
	if extension.PgcryptoProviderOf(routine) != extension.StockPgcrypto {
		t.Fatal("retained scalar is not the sealed installed provider")
	}
	if err := extension.StockPgcrypto.ValidateProcedure(view, routine); err != nil {
		t.Fatal("retained installed declaration is not authentic", err)
	}
	reference := e.Catalog.Domain.Ref(view.Epoch(), routine.Address())
	signature := sqltype.PgcryptoSignature{Inputs: [4]sqltype.ID{sqltype.Bytea}, Count: 1, Result: sqltype.Text, SQL: "17"}
	assertRecipe := func(call plan.InstalledCryptoExpr) plan.LiteralExpr {
		t.Helper()
		if call.Procedure != routine || call.Ref != reference || call.RoutineReference != routine || call.Plan.Name() != "armor" || call.Plan.Signature() != signature || len(call.Args) != 1 || call.Usage.Type.ID() != sqltype.Text || call.Properties().Volatility != expr.Immutable || call.Source == (expr.FoldSourceOccurrence{}) || call.Occurrence == 0 {
			t.Fatal("original installed graph lost checked recipe, identity or provenance", call)
		}
		if call.Usage.Type != analysis.FoldTypes[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Text)}] {
			t.Fatal("retained result lost the definition's exact type generation")
		}
		if err := call.Validate(); err != nil {
			t.Fatal("retained scalar recipe is not checked", err)
		}
		input, ok := call.Args[0].(plan.LiteralExpr)
		if !ok || input.Value.Type() != input.Usage.Type || input.Usage.Type.ID() != sqltype.Bytea || input.Source == (expr.FoldSourceOccurrence{}) || input.Usage.Type != analysis.FoldTypes[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Bytea)}] {
			t.Fatal("retained scalar lost its immutable typed operand")
		}
		raw, err := input.Value.Binary()
		if err != nil || raw != "test" {
			t.Fatal("retained scalar changed its original operand", raw, err)
		}
		return input
	}
	original := func() plan.InstalledCryptoExpr {
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		query := enginetest.BindSelect(t, e, op, source)
		root, ok := query.Root.(*plan.ProjectNode)
		if !ok || len(root.Expressions) != 1 {
			t.Fatal("missing original installed scalar graph", query.Root)
		}
		call, ok := root.Expressions[0].(plan.InstalledCryptoExpr)
		if !ok {
			t.Fatal("original binding prematurely folded installed scalar", root.Expressions[0])
		}
		return call
	}()
	originalInput := assertRecipe(original)
	children := append([]expr.Expr(nil), original.Args...)
	rebuilt, err := original.Rebuild(original.Info, children)
	if err != nil {
		t.Fatal(err)
	}
	rebuiltCall, ok := rebuilt.(plan.InstalledCryptoExpr)
	if !ok {
		t.Fatal("rebuild lost installed scalar carrier", rebuilt)
	}
	rebuiltInput := assertRecipe(rebuiltCall)
	if rebuiltCall.Info != original.Info || rebuiltCall.Plan != original.Plan || rebuiltCall.Properties() != original.Properties() || &rebuiltCall.Args[0] == &original.Args[0] || rebuiltCall.Args[0] != original.Args[0] || rebuiltInput.Info != originalInput.Info || !sqltype.SameDatum(rebuiltInput.Value, originalInput.Value) {
		t.Fatal("rebuild changed genuine recipe, provenance or immutable operand")
	}
	first := bind("armor_recipe_first")
	firstRoot := first.Resource.Root
	firstFolds := *first.Folds
	accepted := owner.Current()
	golden := "-----BEGIN PGP MESSAGE-----\n\ndGVzdA==\n=+G7Q\n-----END PGP MESSAGE-----\n"
	entry := enginetest.PreparedFoldEntry(t, definition.Description, sqltype.Text, golden)
	if accepted.Generation == 0 || len(accepted.Entries) != 1 || len(accepted.Dependencies) == 0 || firstFolds.Generation != accepted.Generation || &firstFolds.Entries[0] != &accepted.Entries[0] || &firstFolds.Dependencies[0] != &accepted.Dependencies[0] || owner.Analysis != analysis || analysis.FoldCallables[routine.Address()] != routine {
		t.Fatal("ordinary scalar lost actual accepted fold backing or analysis")
	}
	if entry.Callable != routine || entry.Result != original.ResultUsage() || entry.Value.Type() != entry.Result.Type || entry.Source == (expr.FoldSourceOccurrence{}) || entry.Occurrence == 0 || entry.Provider != "" || len(entry.Inputs) != 1 || entry.Inputs[0].Usage != originalInput.Usage || entry.Inputs[0].UnknownNull != originalInput.UnknownNull || !sqltype.SameDatum(entry.Inputs[0].Value, originalInput.Value) {
		t.Fatal("accepted armor lost authentic callable, type generation or original typed input", entry)
	}
	for _, address := range []ids.ObjectAddress{routine.Address(), {ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Text)}, {ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Bytea)}} {
		found := false
		for _, dependency := range accepted.Dependencies {
			descriptor := view.Get(address)
			if descriptor != nil && dependency.Ref == e.Catalog.Domain.Ref(view.Epoch(), address) && dependency.Descriptor == descriptor && dependency.Stamp == descriptor.Revision() {
				found = true
			}
		}
		if !found {
			t.Fatal("accepted fold lost exact installed routine or type dependency", address)
		}
	}
	assertLiteral := func(binding *core.PreparedBinding) plan.LiteralExpr {
		t.Helper()
		root := binding.Resource.Root
		if root == nil || len(root.Expressions) != 1 {
			t.Fatal("missing retained private scalar graph")
		}
		literal, ok := root.Expressions[0].(plan.LiteralExpr)
		if !ok || literal.ResultUsage() != entry.Result || !sqltype.SameDatum(literal.Value, entry.Value) || literal.Source != entry.Source || literal.Occurrence != entry.Occurrence || literal.Pos.Offset != entry.Offset || literal.RoutineReference != nil {
			t.Fatal("ordinary preflight lost accepted armor datum or source provenance", root.Expressions[0])
		}
		return literal
	}
	firstLiteral := assertLiteral(first)
	found := false
	for _, dependency := range dependencies {
		if dependency.Ref == reference && dependency.Revision == routine.Revision() {
			found = true
		}
	}
	if !found {
		t.Fatal("retained definition lost exact provider owner/epoch/revision")
	}
	enginetest.SessionSQL(t, s, "SET extra_float_digits=-3")
	second := bind("armor_recipe_second")
	secondRoot := second.Resource.Root
	secondLiteral := assertLiteral(second)
	secondFolds := *second.Folds
	rebound := owner.Current()
	if rebound.Generation != accepted.Generation || len(rebound.Entries) != len(accepted.Entries) || len(rebound.Dependencies) != len(accepted.Dependencies) || &rebound.Entries[0] != &accepted.Entries[0] || &rebound.Dependencies[0] != &accepted.Dependencies[0] || !enginetest.SamePublishedFold(entry, rebound.Entries[0]) || &rebound.Entries[0].Inputs[0] != &entry.Inputs[0] {
		t.Fatal("rebinding replaced accepted armor owner, generation or immutable backing")
	}
	if first == second || first.Resource == second.Resource || firstRoot == secondRoot || &firstRoot.Expressions[0] == &secondRoot.Expressions[0] {
		t.Fatal("independent bindings shared mutable graph or expression slots")
	}
	assertRetained := func() {
		t.Helper()
		for _, saved := range []struct {
			binding *core.PreparedBinding
			root    *plan.ProjectNode
			literal plan.LiteralExpr
			folds   uint64
		}{{first, firstRoot, firstLiteral, firstFolds.Generation}, {second, secondRoot, secondLiteral, secondFolds.Generation}} {
			if saved.binding.Resource.Root != saved.root || saved.binding.Entry.Description.Analysis != analysis || saved.binding.Entry.Description.Folds != owner || saved.binding.Folds.Generation != saved.folds || saved.folds != accepted.Generation || len(saved.binding.Folds.Entries) != len(accepted.Entries) || len(saved.binding.Folds.Dependencies) != len(accepted.Dependencies) || &saved.binding.Folds.Entries[0] != &accepted.Entries[0] || &saved.binding.Folds.Dependencies[0] != &accepted.Dependencies[0] {
				t.Fatal("retained binding changed its actual graph, owner or accepted generation/backing")
			}
			literal := assertLiteral(saved.binding)
			if literal.Info != saved.literal.Info || !sqltype.SameDatum(literal.Value, saved.literal.Value) || !enginetest.SamePublishedFold(entry, saved.binding.Folds.Entries[0]) || &saved.binding.Folds.Entries[0].Inputs[0] != &entry.Inputs[0] {
				t.Fatal("retained accepted datum, occurrence or immutable input backing changed")
			}
		}
		input, rebuiltInput := assertRecipe(original), assertRecipe(rebuiltCall)
		if input.Info != originalInput.Info || !sqltype.SameDatum(input.Value, originalInput.Value) || rebuiltInput.Info != originalInput.Info || !sqltype.SameDatum(rebuiltInput.Value, originalInput.Value) || original.Args[0] != children[0] || rebuiltCall.Args[0] != children[0] {
			t.Fatal("preflight or publication mutated retained original/rebuilt recipe")
		}
		if definition.Body != body || definition.Source != source || definition.Description.CatalogOwner != view.Owner() || definition.Description.CatalogEpoch != view.Epoch() || definition.Description.Analysis != analysis || &analysis.Dependencies[0] != &dependencies[0] || analysis.FoldCallables[routine.Address()] != routine {
			t.Fatal("binding replaced retained immutable definition analysis")
		}
	}
	assertRetained()
	publication := func() *core.FoldPublication {
		t.Helper()
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		environment, err := e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			t.Fatal(err)
		}
		p, err := core.BeginFoldPublication(&definition.Description, definition.Body, environment, auth.Catalog, nil)
		if err != nil || p == nil {
			t.Fatal("missing real scalar publication attempt", err)
		}
		return p
	}
	reject := func(p *core.FoldPublication, ctx context.Context, want error) {
		t.Helper()
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		environment, err := e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Accept(ctx, auth.Catalog, environment, e.Catalog.State.Epoch()); !errors.Is(err, want) || p.Group.Sealed {
			t.Fatal("rejected scalar publication changed acceptance", err)
		}
	}
	candidate := publication()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	reject(candidate, canceled, context.Canceled)
	current := owner.Current()
	if current.Generation != accepted.Generation || !owner.Live() || len(current.Entries) != len(accepted.Entries) || len(current.Dependencies) != len(accepted.Dependencies) || &current.Entries[0] != &accepted.Entries[0] || &current.Dependencies[0] != &accepted.Dependencies[0] {
		t.Fatal("canceled publication changed the ordinary scalar owner/backing")
	}
	assertRetained()
	candidate = publication()
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("armor_recipe") })
	retired := owner.Current()
	if owner.Live() || retired.Generation != accepted.Generation+1 || len(retired.Entries) != len(accepted.Entries) || len(retired.Dependencies) != len(accepted.Dependencies) || &retired.Entries[0] != &accepted.Entries[0] || &retired.Dependencies[0] != &accepted.Dependencies[0] || registry.Lookup("armor_recipe") != nil {
		t.Fatal("definition retirement lost exact scalar owner generation")
	}
	reject(candidate, context.Background(), ids.ErrCatalogReplaced)
	if owner.Current().Generation != retired.Generation {
		t.Fatal("rejected retired publication changed owner generation")
	}
	assertRetained()
	parse()
	if registry.Lookup("armor_recipe").Description.Folds == owner {
		t.Fatal("replacement definition adopted retired scalar ownership")
	}
	assertRetained()
}

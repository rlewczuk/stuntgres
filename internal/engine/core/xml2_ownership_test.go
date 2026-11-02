package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Retain the real prepared definition, accepted folds and portal graphs. Public
// SQL values and live ACL/mode/control witnesses live in reference/extension_xml2.
func TestXML2PreparedOwnership(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	err := enginetest.ExtensionUnitExecute(t, e, op, &parser.CreateExtension{Name: parser.Identifier{Text: "xml2"}})
	done()
	if err != nil {
		t.Fatal(err)
	}
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	const query = "SELECT xml_valid('<r/>'),xpath_string('<r>owned</r>','.'),xpath_number('<r>16777217</r>','.'),xpath_bool('<r/>','.')"
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("xml2_owned", query, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("xml2_owned")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing actual definition")
	}
	analysis, owner := definition.Description.Analysis, definition.Description.Folds
	body, source := definition.Body, definition.Source
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "xml2"}))
	if definition.Description.CatalogOwner != view.Owner() || definition.Description.CatalogEpoch != view.Epoch() || definition.Description.PlanOwner != s.SessionToken || analysis.Domain != view.Domain() || analysis.Epoch != view.Epoch() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("prepare owner/generation or premature folding")
	}
	var procedures [4]*catalog.Procedure
	for slot, a := range ext.Members {
		p := view.MustProcedure(a)
		procedures[slot] = p
		found := false
		for _, d := range analysis.Dependencies {
			if d.Ref.Address == a && d.Ref.Domain() == view.Domain() && d.Ref.Epoch == view.Epoch() && d.Revision == p.Revision() {
				found = true
			}
		}
		if !found || analysis.FoldCallables[a] != p || p.Origin.Installation != ext.Installation {
			t.Fatal("lost exact callable backing", slot)
		}
	}
	bind := func(name string) *core.PreparedBinding {
		t.Helper()
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "xml2_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		b := registry.Binding(name)
		if b == nil || b.Resource == nil || b.Resource.Root == nil || b.Folds == nil || b.Entry.Description.Folds != owner || b.Entry.Description.Analysis != analysis {
			t.Fatal("missing accepted binding")
		}
		return b
	}
	first := bind("xml2_first")
	firstResource, firstRoot, firstHolder := first.Resource, first.Resource.Root, first.Folds
	accepted := owner.Current()
	if len(accepted.Entries) != 3 || accepted.Generation == 0 {
		t.Fatal("expected three actual immutable scalar folds", accepted)
	}
	indices := [4]int{-1, -1, -1, -1}
	for i, entry := range accepted.Entries {
		for slot, p := range procedures {
			if entry.Callable == p {
				indices[slot] = i
			}
		}
		if entry.Source == (expr.FoldSourceOccurrence{}) || entry.Occurrence == 0 || len(entry.Inputs) != 2 || entry.Value.Type() != entry.Result.Type {
			t.Fatal("lost fold provenance/input generation", entry)
		}
		for _, input := range entry.Inputs {
			if input.Usage.Type != analysis.FoldTypes[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 25}] || input.Value.Type() != input.Usage.Type || input.Value.IsNull() {
				t.Fatal("operand backing", input)
			}
		}
	}
	if indices[0] != -1 || indices[1] < 0 || indices[2] < 0 || indices[3] < 0 {
		t.Fatal("STABLE fold or missing genuine installed callable", indices)
	}
	second := bind("xml2_second")
	secondResource, secondRoot, secondHolder := second.Resource, second.Resource.Root, second.Folds
	if first == second || firstResource == secondResource || firstRoot == secondRoot || firstHolder == secondHolder || first.Birth == 0 || first.Birth == second.Birth {
		t.Fatal("shared mutable portal ownership")
	}
	firstFolds, secondFolds := *firstHolder, *secondHolder
	savedEntries := slices.Clone(accepted.Entries)
	savedDependencies := slices.Clone(analysis.Dependencies)
	savedCallables, savedTypes := maps.Clone(analysis.FoldCallables), maps.Clone(analysis.FoldTypes)
	assertFolds := func(g exec.AcceptedFolds) {
		t.Helper()
		if g.Generation != accepted.Generation || len(g.Entries) != 3 || &g.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(g.Dependencies, accepted.Dependencies) {
			t.Fatal("accepted fold generation/backing replaced")
		}
		for i, entry := range g.Entries {
			if !enginetest.SamePublishedFold(entry, savedEntries[i]) || &entry.Inputs[0] != &accepted.Entries[i].Inputs[0] || !sqltype.SameDatum(entry.Value, savedEntries[i].Value) {
				t.Fatal("immutable fold inputs/provenance/datum changed", i)
			}
		}
	}
	assertRoot := func(root *plan.ProjectNode) {
		t.Helper()
		if root == nil || len(root.Expressions) != 4 {
			t.Fatal("lost actual graph")
		}
		stable, ok := root.Expressions[0].(plan.InstalledXML2Expr)
		if !ok || stable.Procedure != procedures[0] || stable.RoutineReference != procedures[0] || stable.Ref.Address != ext.Members[0] || stable.Name != "xml_valid" || stable.IntrinsicVolatility() != expr.Stable || len(stable.Args) != 1 {
			t.Fatal("stable operation lost genuine recipe")
		}
		for slot := 1; slot < 4; slot++ {
			literal, ok := root.Expressions[slot].(plan.LiteralExpr)
			entry := accepted.Entries[indices[slot]]
			if !ok || literal.RoutineReference != nil || literal.Source != entry.Source || literal.Occurrence != entry.Occurrence || literal.ResultUsage() != entry.Result || !sqltype.SameDatum(literal.Value, entry.Value) {
				t.Fatal("graph does not consume real installed fold", slot)
			}
		}
	}
	assertRoot(firstRoot)
	assertRoot(secondRoot)
	assertFolds(owner.Current())
	assertFolds(firstFolds)
	assertFolds(secondFolds)
	for _, cause := range []string{"cancellation", "retirement", "generation"} {
		t.Run(cause, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			operation, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer operation.Done()
			scope, err := e.StartStatement(operation)
			if err != nil {
				t.Fatal(err)
			}
			local := enginetest.CloneFoldDescription(t, definition.Description)
			scope.Expected = &local
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(operation)
			if err != nil {
				e.Mu.Unlock()
				scope.Finish(err)
				t.Fatal(err)
			}
			environment, err := e.PreparedEnvironmentLocked(auth, true)
			if err != nil {
				e.Mu.Unlock()
				scope.Finish(err)
				t.Fatal(err)
			}
			publication, err := core.BeginFoldPublication(&local, definition.Body, environment, auth.Catalog, nil)
			if err != nil || publication == nil {
				e.Mu.Unlock()
				scope.Finish(err)
				t.Fatal("real publication", err)
			}
			publication.Group.Entries = slices.Clone(publication.Group.Entries)
			publication.Group.Private = true
			publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
			switch cause {
			case "cancellation":
				cancel()
			case "retirement":
				local.Retire()
			case "generation":
				local.Folds.Invalidate(true)
			}
			fenced := local.Folds.Current()
			err = publication.Accept(operation.Ctx, auth.Catalog, environment, view.Epoch())
			e.Mu.Unlock()
			_ = scope.Finish(err)
			if cause == "cancellation" && !errors.Is(err, context.Canceled) || cause != "cancellation" && !errors.Is(err, ids.ErrCatalogReplaced) || publication.Group.Sealed {
				t.Fatal("rejected publication accepted", err)
			}
			current := local.Folds.Current()
			if current.Generation != fenced.Generation || &current.Entries[0] != &accepted.Entries[0] {
				t.Fatal("rejected publication mutated backing")
			}
			if registry.Lookup("xml2_owned") != definition || definition.Body != body || definition.Source != source || definition.Description.Analysis != analysis || definition.Description.Folds != owner || !reflect.DeepEqual(analysis.Dependencies, savedDependencies) || !maps.Equal(analysis.FoldCallables, savedCallables) || !maps.Equal(analysis.FoldTypes, savedTypes) || first.Resource != firstResource || second.Resource != secondResource || first.Resource.Root != firstRoot || second.Resource.Root != secondRoot {
				t.Fatal("rejected publication mutated accepted ownership")
			}
			assertFolds(owner.Current())
			assertFolds(*first.Folds)
			assertFolds(*second.Folds)
			assertRoot(firstRoot)
			assertRoot(secondRoot)
		})
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("xml2_owned") })
	if owner.Live() || owner.Current().Generation <= accepted.Generation || first.Retired || second.Retired {
		t.Fatal("definition retirement changed portal lifetime")
	}
	assertFolds(*first.Folds)
	assertFolds(*second.Folds)
	for _, name := range []string{"xml2_first", "xml2_second"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	if !first.Retired || !second.Retired || first.Folds != nil || second.Folds != nil || firstResource.Root != nil || secondResource.Root != nil {
		t.Fatal("portal resources leaked")
	}
	assertFolds(accepted)
	assertFolds(firstFolds)
	assertFolds(secondFolds)
	assertFolds(*firstHolder)
	assertFolds(*secondHolder)
	assertRoot(firstRoot)
	assertRoot(secondRoot)
	operation, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(operation, cycle)
	operation.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}

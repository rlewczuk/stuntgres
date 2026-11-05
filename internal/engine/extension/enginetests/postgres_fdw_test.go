package enginetests

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

func TestPostgresFDWRecipe(t *testing.T) {
	e := engine.New()
	// SQL is installation setup only; assertions inspect sealed descriptors,
	// retained backing and remapping, not duplicate consumer SQL outcomes.
	enginetest.Run(t, e, "CREATE EXTENSION postgres_fdw")
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "postgres_fdw"}))
	if !ext.Installation.Valid() || len(ext.Installation.Members) != 3 || ext.Version != "1.2" {
		t.Fatal("missing exact sealed recipe")
	}
	validator := view.MustProcedure(ext.Members[1])
	canonical := ext.Installation.Declarations[1].(*catalog.Procedure)
	if validator.Foreign != canonical.Foreign || !validator.Strict || validator.Volatility != expr.Volatile || validator.Result != 2278 {
		t.Fatal("live declaration lost canonical capability")
	}
	for _, mutate := range []struct {
		name  string
		apply func(*catalog.Procedure)
	}{
		{"copied token", func(p *catalog.Procedure) { token := *p.Foreign; p.Foreign = &token }},
		{"missing origin", func(p *catalog.Procedure) { p.Origin = nil }},
		{"different provider", func(p *catalog.Procedure) { p.Implementation = extension.StockFileFDW }},
		{"wrong slot", func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 0; p.Origin = &origin }},
		{"wrong result", func(p *catalog.Procedure) { p.Result = 16 }},
		{"non-strict", func(p *catalog.Procedure) { p.Strict = false }},
		{"immutable", func(p *catalog.Procedure) { p.Volatility = expr.Immutable }},
		{"foreign principal", func(p *catalog.Procedure) { p.Owner = new(authorization.Domain).Principal(p.Owner.ID()) }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			state := catalog.CloneState(view.State())
			copy := *validator
			mutate.apply(&copy)
			state.SetDescriptor(copy.Address(), &copy)
			if err := extension.StockPostgresFDW.ValidateProcedure(view.WithState(state), &copy); err == nil {
				t.Fatal("forged executable recipe admitted")
			}
		})
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	query := enginetest.BindSelect(t, e, op, "SELECT postgres_fdw_validator(ARRAY['fetch_size=1'],1417::oid)")
	projection, ok := query.Root.(*plan.ProjectNode)
	if !ok || len(projection.Expressions) != 1 {
		done()
		t.Fatal("missing real installed call")
	}
	node, ok := projection.Expressions[0].(plan.StockFDWExpr)
	if !ok {
		done()
		t.Fatal("wrong prepared carrier")
	}
	if node.RoutineReference != validator || node.Ref.Domain() != e.Catalog.Domain || node.Ref.Address != validator.Address() || node.PlanningFold() != expr.FoldNever {
		done()
		t.Fatal("prepared owner or provider authority changed")
	}
	children := slices.Clone(node.Args)
	rebuilt, err := node.Rebuild(node.Info, children)
	if err != nil {
		done()
		t.Fatal(err)
	}
	next := rebuilt.(plan.StockFDWExpr)
	if next.RoutineReference != node.RoutineReference || next.Ref != node.Ref || &next.Args[0] == &node.Args[0] || !reflect.DeepEqual(next.Args, node.Args) {
		done()
		t.Fatal("rebuild lost retained provenance or adopted operand backing")
	}
	done()
	for _, q := range []string{"CREATE SCHEMA pgfdw_private", "ALTER FUNCTION postgres_fdw_validator(text[],oid) RENAME TO actual_validator", "ALTER FUNCTION actual_validator(text[],oid) SET SCHEMA pgfdw_private", "ALTER FOREIGN DATA WRAPPER postgres_fdw RENAME TO actual_wrapper"} {
		enginetest.Run(t, e, q)
	}
	current := e.CatalogRead()
	live := current.MustProcedure(validator.Address())
	if err := extension.StockPostgresFDW.ValidateProcedure(current, live); err != nil {
		t.Fatal(err)
	}
	if live.Foreign != validator.Foreign || live.Origin.Installation != validator.Origin.Installation || live.Name() == canonical.Name() || live.Namespace() == canonical.Namespace() {
		t.Fatal("rename/move rewrote immutable recipe or capability")
	}
	if canonical.Name() != "postgres_fdw_validator" || ext.Installation.Declarations[1] != canonical {
		t.Fatal("retained declaration backing mutated")
	}
	mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
	for _, a := range append([]ids.ObjectAddress{ext.Address()}, ext.Installation.Members...) {
		moved := a
		moved.ObjectID += 200000
		mapping[a] = moved
	}
	state := catalog.CloneState(current.State())
	if err := control.RemapReplacementIdentities(current, state, nil, mapping, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	remapped := current.WithState(state)
	moved := remapped.MustProcedure(mapping[live.Address()])
	wrapper := remapped.MustForeign(mapping[ext.Members[2]])
	if moved.Foreign != live.Foreign || moved.Origin.Installation == live.Origin.Installation || moved.Origin.Installation != wrapper.Origin.Installation || wrapper.Validator != moved.Address() {
		t.Fatal("replacement mixed installed cohorts or callbacks")
	}
	if err := extension.StockPostgresFDW.ValidateProcedure(remapped, moved); err != nil {
		t.Fatal(err)
	}
	if live.Origin.Installation.Declarations[1] != canonical || live.Origin.Installation.Members[1] != validator.Address() {
		t.Fatal("remap mutated original recipe backing")
	}
}

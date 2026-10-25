package engine

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
)

func TestGeneratedCompositeAttributeProviders(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE TYPE public.gen_pair AS(i integer,t text)",
		"CREATE TYPE public.gen_outer AS(p public.gen_pair,n integer)",
		"CREATE TABLE public.gen_holder(id integer,w public.gen_outer,g integer GENERATED ALWAYS AS ((w).p.i) STORED)",
		"CREATE TABLE public.gen_foreign(w public.gen_outer)",
		"INSERT INTO public.gen_holder(id,w) VALUES(1,ROW(ROW(7,'x')::public.gen_pair,8)::public.gen_outer)",
		"UPDATE public.gen_holder SET w=ROW(ROW(9,'y')::public.gen_pair,10)::public.gen_outer WHERE id=1",
	} {
		roleExecute(t, e, op, query)
	}
	view := e.CatalogRead()
	holder := view.MustRelation(roleTableRef(t, e, "gen_holder").Address)
	foreignRef := roleTableRef(t, e, "gen_foreign")
	foreign := view.MustRelation(foreignRef.Address)
	pair := domainUnitType(t, view, "gen_pair")
	outer := domainUnitType(t, view, "gen_outer")
	generationAttribute := holder.Attributes[holder.Index["g"]]
	definition := holder.ColumnMeta.Generations[generationAttribute]
	selected := -1
	for _, typ := range []*catalog.Type{pair, outer} {
		backing := view.MustRelation(typ.OwningRelation)
		want := backing.Address()
		want.SubID = backing.Attributes[0]
		found := false
		for i, ref := range definition.Dependencies {
			if ref.Address == want {
				found = true
				if typ == pair {
					selected = i
				}
			}
		}
		if !found {
			t.Fatal("missing actual stable composite member dependency", typ.Name(), want)
		}
	}
	if selected < 0 {
		t.Fatal("missing selected pair member")
	}
	if err := table.ValidateGenerationProviders(view, holder); err != nil {
		t.Fatal(err)
	}
	if err := (RelationProvider).Validate(view, holder); err != nil {
		t.Fatal(err)
	}
	result := roleExecute(t, e, op, "SELECT id,g FROM public.gen_holder")
	defer result.Rows.Close()
	rows := tuples(result)
	if len(rows) != 1 || len(rows[0]) != 2 || rows[0][0] != int64(1) || rows[0][1] != int64(9) {
		t.Fatal("genuine generated field evaluation changed", rows)
	}

	withDependency := func(change func(*catalog.ColumnGeneration)) *catalog.Relation {
		bad := *holder
		generation := definition
		generation.Dependencies = slices.Clone(definition.Dependencies)
		change(&generation)
		bad.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{generationAttribute: generation}
		return &bad
	}
	backing := view.MustRelation(pair.OwningRelation)
	for _, change := range []func(*catalog.ColumnGeneration){
		func(g *catalog.ColumnGeneration) {
			g.Dependencies[selected].Address.SubID = backing.Attributes[len(backing.Attributes)-1] + 1
		},
		func(g *catalog.ColumnGeneration) { g.Dependencies[selected].Address.SubID = -1 },
		func(g *catalog.ColumnGeneration) { g.Dependencies[selected].Epoch++ },
	} {
		bad := withDependency(change)
		if err := table.ValidateGenerationProviders(view, bad); err == nil {
			t.Fatal("forged generation field provider admitted", bad.ColumnMeta.Generations[generationAttribute].Dependencies[selected])
		}
	}
	wrongOwner := *pair
	wrongOwner.OwningRelation = holder.Address()
	candidate := catalog.CloneState(view.State())
	candidate.SetDescriptor(pair.Address(), &wrongOwner)
	if err := table.ValidateGenerationProviders(e.Catalog.Read(candidate), holder); err == nil {
		t.Fatal("unrelated backing relation accepted as named rowtype authority")
	}

	unrelated := withDependency(func(g *catalog.ColumnGeneration) {
		address := foreign.Address()
		address.SubID = foreign.Attributes[0]
		g.Dependencies[selected] = view.Owner().Domain.Ref(view.Epoch(), address)
	})
	if err := (RelationProvider).Validate(view, unrelated); err == nil {
		t.Fatal("live unrelated metadata attribute replaced genuine expression manifest")
	}
	var ownColumn plan.ColumnExpr
	foundColumn := false
	plan.WalkExpression(definition.Planned, func(node expr.Expr) {
		if column, ok := node.(plan.ColumnExpr); ok {
			ownColumn = column
			foundColumn = true
		}
	})
	if !foundColumn {
		t.Fatal("generation control lost its actual row input")
	}
	address := foreign.Address()
	address.SubID = foreign.Attributes[0]
	ownColumn.Origin = view.Owner().Domain.Ref(view.Epoch(), address)
	ownColumn.Slot = 0
	if err := table.ValidateGenerationPlan(view, holder, ownColumn); err == nil {
		t.Fatal("provider metadata admitted a cross-table data read")
	}
	if e.CatalogRead().State() != view.State() || view.MustType(pair.Address()) != pair {
		t.Fatal("forged validation changed actual catalog authority")
	}
}

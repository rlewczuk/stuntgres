package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func inheritanceUnitParents(parents ...ids.ObjectAddress) []catalog.InheritanceParent {
	edges := make([]catalog.InheritanceParent, len(parents))
	for i, parent := range parents {
		edges[i] = catalog.InheritanceParent{Relation: parent, Seqno: int32(i + 1)}
	}
	return edges
}

func TestInheritanceAlterOrigins(t *testing.T) {
	for _, kind := range []string{"column", "notnull", "check"} {
		for _, only := range []bool{false, true} {
			for _, other := range []bool{false, true} {
				for _, local := range []bool{false, true} {
					e := New()
					p := inheritanceUnitRelation(e, "p", 22000, "a")
					q := inheritanceUnitRelation(e, "q", 22001, "a")
					c := inheritanceUnitRelation(e, "c", 22002, "extra", "a")
					c.Attributes = []int32{2, 7}
					c.ColumnMeta.HighWater = 7
					parents := []ids.ObjectAddress{p.Address()}
					origins := []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}}
					if other {
						parents = append(parents, q.Address())
						origins = append(origins, catalog.InheritanceOrigin{Parent: q.Address(), Attribute: 1})
					}
					c.Inheritance = &catalog.TableInheritance{Parents: inheritanceUnitParents(parents...), Columns: map[int32]catalog.InheritedColumn{2: {Local: true}, 7: {Local: local, Origins: origins}}, NotNull: map[int32]catalog.InheritedConstraint{}, Checks: map[string]catalog.InheritedConstraint{}}
					original := catalog.CloneInheritance(c.Inheritance)
					next := *p
					next.ObjectRevision++
					action := parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: "a"}}
					switch kind {
					case "column":
						next.Columns = nil
						next.Attributes = nil
						next.Index = map[string]int{}
					case "notnull":
						p.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23000, Name: "fact", Attribute: 1}}
						q.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23001, Name: "fact", Attribute: 1}}
						c.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23002, Name: "fact", Attribute: 7}}
						nn := append([]catalog.InheritanceOrigin(nil), origins...)
						for i := range nn {
							nn[i].Constraint = 23000 + ids.OID(i)
						}
						c.Inheritance.NotNull[7] = catalog.InheritedConstraint{Local: local, Origins: nn}
						original = catalog.CloneInheritance(c.Inheritance)
						next.Constraints.NotNull = nil
						action = parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: "fact"}}
					case "check":
						p.Constraints.Check = []catalog.CheckConstraint{{OID: 23000, Name: "fact"}}
						q.Constraints.Check = []catalog.CheckConstraint{{OID: 23001, Name: "fact"}}
						c.Constraints.Check = []catalog.CheckConstraint{{OID: 23002, Name: "fact"}}
						checks := append([]catalog.InheritanceOrigin(nil), origins...)
						for i := range checks {
							checks[i].Attribute = 0
							checks[i].Constraint = 23000 + ids.OID(i)
						}
						c.Inheritance.Checks["fact"] = catalog.InheritedConstraint{Local: local, Origins: checks}
						original = catalog.CloneInheritance(c.Inheritance)
						next.Constraints.Check = nil
						action = parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: "fact"}}
					}
					view := inheritanceUnitView(e, p, q, c)
					stage := e.BeginCatalogStage(view)
					stage.UpdateDescriptor(&next)
					state := &InheritanceAlterState{Stage: stage, InheritanceAlterPlan: table.InheritanceAlterPlan{Root: p, Targets: []*catalog.Relation{p, c}, Selected: map[ids.ObjectAddress]bool{p.Address(): true, c.Address(): true}, Only: only, Actions: []parser.AlterTableAction{action}, Events: make(map[ids.ObjectAddress][]table.InheritanceAlterEvent)}}
					state.InheritanceAlterPlan.RecordAction(p, &next, action)
					actions, graph, err := e.inheritedAlterActions(state, c)
					if err != nil {
						t.Fatal(kind, only, other, local, err)
					}
					wantLocal := local || (only && (!other || kind == "column"))
					wantOrigins := 0
					if other {
						wantOrigins = 1
					}
					actualLocal := graph.Columns[7].Local
					actualOrigins := graph.Columns[7].Origins
					if kind == "notnull" {
						actualLocal = graph.NotNull[7].Local
						actualOrigins = graph.NotNull[7].Origins
					}
					if kind == "check" {
						actualLocal = graph.Checks["fact"].Local
						actualOrigins = graph.Checks["fact"].Origins
					}
					if actualLocal != wantLocal || len(actualOrigins) != wantOrigins {
						t.Fatal("wrong retained provenance", kind, only, other, local, graph)
					}
					if other && (actualOrigins[0].Parent != q.Address() || kind != "check" && actualOrigins[0].Attribute != 1) {
						t.Fatal("surviving parent identity/attribute changed", actualOrigins)
					}
					wantDrop := !wantLocal && !other
					if (len(actions) == 1) != wantDrop || len(actions) > 1 {
						t.Fatal("wrong physical target selection", kind, only, other, local, actions)
					}
					if !reflect.DeepEqual(c.Inheritance, original) {
						t.Fatal("captured graph mutated", kind)
					}
					e.DiscardCatalogStage(stage)
				}
			}
		}
	}
}

func TestInheritanceAlterAtomicity(t *testing.T) {
	e, p, c := inheritanceDMLPublishedGraph(t)
	before := e.CatalogRead()
	parentStore, childStore := e.Storage.Relations[p.Address()], e.Storage.Relations[c.Address()]
	parentRows := append([]storage.RowVersion(nil), parentStore.Versions...)
	childRows := append([]storage.RowVersion(nil), childStore.Versions...)
	stage := e.BeginCatalogStage(before)
	defer e.DiscardCatalogStage(stage)
	for _, old := range []*catalog.Relation{p, c} {
		next := *old
		next.ObjectRevision++
		next.Comment = "candidate"
		store := e.Storage.Relations[old.Address()]
		ref := e.Catalog.Domain.Ref(before.Epoch(), old.Address())
		if err := stage.AlterRelation(context.Background(), ref, &next, store, store, len(store.Versions), store.Generation, store.Ends); err != nil {
			t.Fatal(err)
		}
	}
	// A real later physical store observation changes after both descriptor
	// candidates were admitted. Final validation must reject the entire graph.
	childStore.Generation++
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
		t.Fatal("stale later child observation validated")
	}
	if e.CatalogRead().State() != before.State() || e.Storage.Relations[p.Address()] != parentStore || e.Storage.Relations[c.Address()] != childStore {
		t.Fatal("late child veto partially published catalog/store identities")
	}
	if !reflect.DeepEqual(parentRows, parentStore.Versions) || !reflect.DeepEqual(childRows, childStore.Versions) {
		t.Fatal("late child veto changed physical rows")
	}
}

func TestInheritanceAlterDefinitionAuthority(t *testing.T) {
	e := New()
	p := inheritanceUnitRelation(e, "p", 22000, "a")
	c := inheritanceUnitRelation(e, "c", 22001, "a")
	usage := testUsage(e, sqltype.Int4)
	expression := func(n int64) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: inheritanceQueryInt(e, n)}
	}
	p.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {Expression: expression(1)}}
	c.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {Expression: expression(9)}}
	provenance := catalog.InheritedColumn{Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}}}
	view := inheritanceUnitView(e, p, c)
	retained, err := table.RetainInheritedRecipes(view, c, 1, provenance)
	if err != nil || retained.Local || !retained.ExplicitDefault || !reflect.DeepEqual(retained.Origins, provenance.Origins) {
		t.Fatal("default authority changed attribute locality/origins", retained, err)
	}
	c.ColumnMeta.Defaults = nil
	tombstone, err := table.RetainInheritedRecipes(view, c, 1, provenance)
	if err != nil || tombstone.Local || !tombstone.ExplicitDefault {
		t.Fatal("default absence lost explicit authority", tombstone, err)
	}
	p.ColumnMeta.Defaults = nil
	plain, err := table.RetainInheritedRecipes(view, c, 1, provenance)
	if err != nil || plain.ExplicitDefault || plain.Local {
		t.Fatal("ordinary inherited absence fabricated authority", plain, err)
	}
	p = inheritanceUnitRelation(e, "gp", 22002, "a", "g")
	c = inheritanceUnitRelation(e, "gc", 22003, "a", "g")
	p.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {OID: 24000, Kind: catalog.GenerationVirtual, Expression: expression(1)}}
	c.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {OID: 24001, Kind: catalog.GenerationVirtual, Expression: expression(2)}}
	c.Inheritance = &catalog.TableInheritance{Parents: inheritanceUnitParents(p.Address()), Columns: map[int32]catalog.InheritedColumn{1: {Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}}}, 2: {Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 2}}}}, NotNull: map[int32]catalog.InheritedConstraint{}, Checks: map[string]catalog.InheritedConstraint{}}
	view = inheritanceUnitView(e, p, c)
	if err := table.ValidateInheritance(view, c); err != nil {
		t.Fatal("legal ONLY recipe divergence invalidated column origins", err)
	}
	effective, err := table.RetainInheritedRecipes(view, c, 2, c.Inheritance.Columns[2])
	if err != nil || effective.Local || !effective.ExplicitGeneration || c.Inheritance.Columns[2].ExplicitGeneration {
		t.Fatal("capture failed to derive retained recipe without retagging live child", effective, err)
	}
	for _, kind := range []string{"kind", "usage", "origin"} {
		next := *c
		next.Columns = append([]catalog.Column(nil), c.Columns...)
		next.Inheritance = catalog.CloneInheritance(c.Inheritance)
		switch kind {
		case "kind":
			next.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {OID: 24001, Kind: catalog.GenerationStored, Expression: expression(2)}}
		case "usage":
			next.Columns[1].Usage = testUsage(e, sqltype.Int8)
		case "origin":
			next.Inheritance.Columns[2] = catalog.InheritedColumn{Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}}}
		}
		if err := table.ValidateInheritance(view, &next); err == nil {
			t.Fatal("recipe equality relaxation bypassed independent invariant", kind)
		}
	}
	foreign := New()
	definition := c.ColumnMeta.Generations[2]
	definition.Dependencies = []ids.ObjectRef{foreign.Catalog.Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})}
	invalid := *c
	invalid.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: definition}
	if err := table.ValidateGenerationProviders(view, &invalid); err == nil {
		t.Fatal("foreign generation provider gained authority")
	}
	if err := table.ValidateGenerationPlan(view, c, plan.LiteralExpr{}); err == nil {
		t.Fatal("missing own expression Usage passed validation")
	}
}

// Public SQL/rows/errors moved to TestReferenceInheritanceMaintOnlyMixedMigration.
// These units exercise the provenance and physical-recipe seams directly.
func TestInheritanceAlterOnlyMixedActions(t *testing.T) {
	e := New()
	p := inheritanceUnitRelation(e, "p", 22000, "a", "b", "g")
	c := inheritanceUnitRelation(e, "c", 22001, "a", "b", "g")
	c.Inheritance = &catalog.TableInheritance{
		Parents: inheritanceUnitParents(p.Address()),
		Columns: map[int32]catalog.InheritedColumn{},
		NotNull: map[int32]catalog.InheritedConstraint{},
		Checks:  map[string]catalog.InheritedConstraint{},
	}
	for _, attribute := range c.Attributes {
		c.Inheritance.Columns[attribute] = catalog.InheritedColumn{Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: attribute}}}
	}
	usage := testUsage(e, sqltype.Int4)
	generation := catalog.ColumnGeneration{OID: 24000, Kind: catalog.GenerationStored, Expression: plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: inheritanceQueryInt(e, 3)}}
	c.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{3: generation}
	original := catalog.CloneInheritance(c.Inheritance)
	view := inheritanceUnitView(e, p, c)
	stage := e.BeginCatalogStage(view)
	defer e.DiscardCatalogStage(stage)
	next := *p
	next.ObjectRevision++
	next.Columns = []catalog.Column{p.Columns[0], p.Columns[2]}
	next.Attributes = []int32{1, 3}
	next.Index = map[string]int{"a": 0, "g": 1}
	stage.UpdateDescriptor(&next)
	drop := parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: "b"}}
	for _, later := range []parser.AlterTableAction{
		{Action: parser.SetExpression, Column: parser.Identifier{Text: "g"}},
		{Action: parser.AddUnique},
	} {
		state := &InheritanceAlterState{Stage: stage, InheritanceAlterPlan: table.InheritanceAlterPlan{
			Root: p, Targets: []*catalog.Relation{p, c}, Only: true,
			Selected: map[ids.ObjectAddress]bool{p.Address(): true},
			Actions:  []parser.AlterTableAction{drop, later},
			Events:   make(map[ids.ObjectAddress][]table.InheritanceAlterEvent),
		}}
		state.RecordAction(p, &next, drop)
		state.RecordAction(p, &next, later)
		actions, graph, err := e.inheritedAlterActions(state, c)
		if err != nil || len(actions) != 0 {
			t.Fatal("ONLY root recipe/key acquired descendant physical action", actions, err)
		}
		if fact := graph.Columns[2]; !fact.Local || len(fact.Origins) != 0 {
			t.Fatal("ONLY DROP did not localize surviving child column", fact)
		}
		if !reflect.DeepEqual(c.Inheritance, original) || !reflect.DeepEqual(c.ColumnMeta.Generations[3], generation) {
			t.Fatal("candidate changed captured child provenance/generation identity")
		}
	}
}

func TestInheritanceAlterCheckProvenanceAtomicity(t *testing.T) {
	e := New()
	p := inheritanceUnitRelation(e, "p", 22000, "a")
	c := inheritanceUnitRelation(e, "c", 22001, "a")
	p.Constraints.Check = []catalog.CheckConstraint{{OID: 23000, Name: "ck"}}
	c.Constraints.Check = []catalog.CheckConstraint{{OID: 23001, Name: "ck"}}
	c.Inheritance = &catalog.TableInheritance{
		Parents: inheritanceUnitParents(p.Address()),
		Columns: map[int32]catalog.InheritedColumn{1: {Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}}}},
		NotNull: map[int32]catalog.InheritedConstraint{},
		Checks:  map[string]catalog.InheritedConstraint{"ck": {Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Constraint: 23000}}}},
	}
	view := inheritanceUnitView(e, p, c)
	published := e.CatalogRead()
	original := catalog.CloneInheritance(c.Inheritance)
	stage := e.BeginCatalogStage(view)
	nextParent := *p
	nextParent.ObjectRevision++
	nextParent.Constraints.Check = []catalog.CheckConstraint{{OID: 23002, Name: "ck"}}
	stage.UpdateDescriptor(&nextParent)
	drop := parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: "ck"}}
	state := &InheritanceAlterState{Stage: stage, InheritanceAlterPlan: table.InheritanceAlterPlan{
		Root: p, Targets: []*catalog.Relation{p, c}, Descendant: true,
		Selected: map[ids.ObjectAddress]bool{p.Address(): true, c.Address(): true},
		Actions:  []parser.AlterTableAction{drop},
		Events:   make(map[ids.ObjectAddress][]table.InheritanceAlterEvent),
	}}
	state.RecordAction(p, &nextParent, drop)
	_, graph, err := e.inheritedAlterActions(state, c)
	if err != nil {
		t.Fatal(err)
	}
	state.Graph = graph
	nextChild := *c
	nextChild.Constraints.Check = []catalog.CheckConstraint{{OID: 23003, Name: "ck"}}
	candidate, err := table.FinishInheritanceAlterCandidate(&state.InheritanceAlterPlan, e.StageRead(stage), c, &nextChild)
	if err != nil {
		t.Fatal(err)
	}
	fact := candidate.Inheritance.Checks["ck"]
	if fact.Local || len(fact.Origins) != 1 || fact.Origins[0].Parent != p.Address() || fact.Origins[0].Constraint != 23002 {
		t.Fatal("replacement CHECK localized or retained old provider identity", fact)
	}
	stage.UpdateDescriptor(candidate)
	if e.CatalogRead().State() != published.State() || view.Get(p.Address()) != p || view.Get(c.Address()) != c || !reflect.DeepEqual(c.Inheritance, original) {
		t.Fatal("staged CHECK replacement changed captured catalog/provenance")
	}
	e.DiscardCatalogStage(stage)
	if e.CatalogRead().State() != published.State() || view.Get(c.Address()) != c || !reflect.DeepEqual(c.Inheritance, original) || c.Constraints.Check[0].OID != 23001 {
		t.Fatal("discarded CHECK replacement changed catalog/fact identity")
	}
}

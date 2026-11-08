package enginetests_test

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func groupDependencyStoredAggregate(t *testing.T, root plan.Node) *plan.AggregateNode {
	t.Helper()
	var aggregate *plan.AggregateNode
	plan.VisitCTEQuery(root, func(node plan.Node) {
		if found, ok := node.(*plan.AggregateNode); ok && len(found.Dependent) != 0 {
			aggregate = found
		}
	}, func(expr.Expr) {}, true)
	if aggregate == nil {
		t.Fatal("missing accepted grouping dependency")
	}
	return aggregate
}

func groupDependencyEditedView(t *testing.T, original *stored.View, edit func(*plan.AggregateNode)) *stored.View {
	t.Helper()
	root, err := plan.RewriteQueryExpressions(original.Query().Root, func(bound expr.Expr) (expr.Expr, bool, error) {
		return bound, false, nil
	}, func(node plan.Node) (plan.Node, error) {
		if aggregate, ok := node.(*plan.AggregateNode); ok {
			copy := *aggregate
			copy.Dependencies = slices.Clone(aggregate.Dependencies)
			for i := range copy.Dependencies {
				copy.Dependencies[i].Attributes = slices.Clone(copy.Dependencies[i].Attributes)
			}
			copy.Dependent = slices.Clone(aggregate.Dependent)
			copy.QuerySchema = slices.Clone(aggregate.QuerySchema)
			edit(&copy)
			return &copy, nil
		}
		return node, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return original.With(func(parts *stored.ViewParts) {
		query := *parts.Query
		query.Root = root
		parts.Query = &query
		parts.Frames, parts.Queries = analyze.CollectQueryFrames(root)
	})
}

func TestGroupDependencyConstraintProof(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE TABLE gd_constraint(id int PRIMARY KEY,payload json,s text)",
		"CREATE TABLE gd_destination(id int PRIMARY KEY,payload json,s text)",
		"CREATE VIEW gd_constraint_view AS SELECT id,payload,s,count(*) FROM gd_constraint GROUP BY id",
	} {
		enginetest.Run(t, e, sql)
	}
	read := e.CatalogRead()
	parent := enginetest.PrimaryKeyUnitRelation(t, e, "gd_constraint")
	destination := enginetest.PrimaryKeyUnitRelation(t, e, "gd_destination")
	owner := enginetest.PrimaryKeyUnitRelation(t, e, "gd_constraint_view")
	definition := stored.ViewOf(owner)
	aggregate := groupDependencyStoredAggregate(t, definition.Query().Root)
	proof := aggregate.Dependencies[0]
	if _, descriptor := read.Find(proof.Constraint); descriptor {
		t.Fatal("attached constraint unexpectedly requires a standalone descriptor")
	}
	if err := definition.Validate(read, owner); err != nil {
		t.Fatal("authentic attached-constraint proof rejected", err)
	}
	if proof.Parent.Address != parent.Address() || proof.Constraint.ObjectID != parent.Constraints.Unique[0].OID || !definition.DependsOn(proof.Constraint) || definition.DependsOn(destination.Constraints.Unique[0].Index) {
		t.Fatal("view lifetime does not use the genuine supporting constraint")
	}
	references, err := stored.QueryDependRefs(t.Context(), read, nil, definition.Query())
	if err != nil || !slices.Contains(references, proof.Constraint) {
		t.Fatal("SQL dependency projection lost attached proof identity", references, err)
	}
	for _, test := range []struct {
		name string
		edit func(*plan.AggregateNode)
	}{
		{name: "missing", edit: func(n *plan.AggregateNode) { n.Dependencies = nil }},
		{name: "missing_payload", edit: func(n *plan.AggregateNode) { n.Dependent = nil }},
		{name: "missing_payload_and_proof", edit: func(n *plan.AggregateNode) {
			n.Dependent = nil
			n.Dependencies = nil
		}},
		{name: "replaced_constraint", edit: func(n *plan.AggregateNode) {
			n.Dependencies[0].Constraint.ObjectID = destination.Constraints.Unique[0].OID
		}},
		{name: "changed_key_attributes", edit: func(n *plan.AggregateNode) { n.Dependencies[0].Attributes[0]++ }},
		{name: "wrong_parent", edit: func(n *plan.AggregateNode) {
			n.Dependencies[0].Parent = read.Domain().Ref(read.Epoch(), destination.Address())
		}},
		{name: "foreign_domain", edit: func(n *plan.AggregateNode) {
			n.Dependencies[0].Parent = new(ids.Domain).Ref(read.Epoch(), parent.Address())
		}},
		{name: "future_epoch", edit: func(n *plan.AggregateNode) {
			n.Dependencies[0].Parent = read.Domain().Ref(read.Epoch()+1, parent.Address())
		}},
		{name: "missing_epoch", edit: func(n *plan.AggregateNode) { n.Dependencies[0].Parent = read.Domain().Ref(0, parent.Address()) }},
		{name: "wrong_occurrence", edit: func(n *plan.AggregateNode) { n.Dependencies[0].Occurrence++ }},
		{name: "wrong_frame", edit: func(n *plan.AggregateNode) { n.Dependencies[0].Frame++ }},
		{name: "payload_layout", edit: func(n *plan.AggregateNode) { n.QuerySchema = n.QuerySchema[:len(n.QuerySchema)-1] }},
		{name: "extra_payload_layout", edit: func(n *plan.AggregateNode) {
			n.QuerySchema = append(n.QuerySchema, n.QuerySchema[len(n.QuerySchema)-1])
		}},
		{name: "forged_payload", edit: func(n *plan.AggregateNode) {
			column := n.Dependent[0].(plan.ColumnExpr)
			column.Slot++
			n.Dependent[0] = column
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := groupDependencyEditedView(t, definition, test.edit)
			if err := candidate.Validate(read, owner); err == nil {
				t.Fatal("forged or stale proof accepted")
			}
			if err := definition.Validate(read, owner); err != nil {
				t.Fatal("candidate rejection changed retained source", err)
			}
		})
	}
	for _, test := range []struct {
		name string
		edit func(*catalog.Relation)
	}{
		{name: "removed", edit: func(parent *catalog.Relation) { parent.Constraints.Unique = nil }},
		{name: "deferrable", edit: func(parent *catalog.Relation) { parent.Constraints.Unique[0].Deferrable = true }},
		{name: "not_primary", edit: func(parent *catalog.Relation) { parent.Constraints.Unique[0].Primary = false }},
		{name: "same_name_new_identity", edit: func(parent *catalog.Relation) { parent.Constraints.Unique[0].OID++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := catalog.CloneState(read.State())
			changed := *parent
			changed.Constraints.Unique = slices.Clone(parent.Constraints.Unique)
			test.edit(&changed)
			state.SetDescriptor(changed.Address(), &changed)
			current := catalog.NewRead(read.Owner(), state)
			if err := definition.Validate(current, owner); err == nil {
				t.Fatal("canonical parent metadata failed to authenticate proof")
			}
			if _, err := stored.QueryDependRefs(t.Context(), current, nil, definition.Query()); err == nil {
				t.Fatal("SQL projection accepted stale canonical constraint authority")
			}
		})
	}
	t.Run("remap_parent_and_constraint_together", func(t *testing.T) {
		constraint := ids.ObjectAddress{ClassID: 2606, ObjectID: destination.Constraints.Unique[0].OID}
		remap := func(includeConstraint bool) func(ids.ObjectRef) ids.ObjectRef {
			return func(ref ids.ObjectRef) ids.ObjectRef {
				address := ref.Address
				if address.ClassID == parent.Address().ClassID && address.ObjectID == parent.Address().ObjectID {
					address.ObjectID = destination.Address().ObjectID
				}
				if includeConstraint && address == proof.Constraint {
					address = constraint
				}
				return read.Domain().Ref(read.Epoch(), address)
			}
		}
		typ := func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }
		if _, err := stored.RemapViewQuery(read, definition.Query().Root, remap(false), typ); err == nil {
			t.Fatal("parent remap retained another parent's constraint")
		}
		root, err := stored.RemapViewQuery(read, definition.Query().Root, remap(true), typ)
		if err != nil {
			t.Fatal(err)
		}
		mapped := groupDependencyStoredAggregate(t, root)
		if mapped.Dependencies[0].Parent.Address != destination.Address() || mapped.Dependencies[0].Constraint != constraint || aggregate.Dependencies[0].Parent.Address != parent.Address() || &mapped.Dependencies[0] == &aggregate.Dependencies[0] {
			t.Fatal("remap failed destination identity or immutable source ownership")
		}
		adapted := definition.With(func(parts *stored.ViewParts) {
			query := *parts.Query
			query.Root = root
			parts.Query = &query
			parts.Frames, parts.Queries = analyze.CollectQueryFrames(root)
			parts.Sources = slices.Clone(parts.Sources)
			parts.Sources[proof.Occurrence].Ref = mapped.Dependencies[0].Parent
		})
		if err := stored.ValidateViewLeaves(read, adapted); err != nil {
			t.Fatal("remapped proof failed actual destination metadata", err)
		}
		refs, err := stored.QueryDependRefs(t.Context(), read, nil, adapted.Query())
		if err != nil || !slices.Contains(refs, constraint) || slices.Contains(refs, proof.Constraint) {
			t.Fatal("remapped SQL projection retained old constraint", refs, err)
		}
		mapping := map[ids.ObjectAddress]ids.ObjectAddress{parent.Address(): destination.Address(), proof.Constraint: constraint}
		frozen, err := stored.RemapView(read, definition, mapping, remap(true), typ, nil, nil)
		if err != nil {
			t.Fatal("frozen presentation remap lost attached proof", err)
		}
		if err := frozen.Validate(read, owner); err != nil {
			t.Fatal("remapped frozen definition failed destination validation", err)
		}
		if !frozen.DependsOn(constraint) || frozen.DependsOn(proof.Constraint) ||
			definition.Sources()[proof.Occurrence].Ref != proof.Parent ||
			groupDependencyStoredAggregate(t, definition.Query().Root).Dependencies[0].Constraint != proof.Constraint {
			t.Fatal("frozen remap retained old authority or mutated its predecessor")
		}
		for _, dependency := range frozen.Dependencies() {
			if dependency.Ref.Address.ClassID == 2606 ||
				dependency.Ref.Address.ClassID == parent.Address().ClassID && dependency.Ref.Address.ObjectID == parent.Address().ObjectID {
				t.Fatal("frozen descriptor manifest retained old parent or a constraint descriptor")
			}
		}
	})
	for i, sql := range []string{
		"CREATE VIEW gd_nested_scalar AS SELECT (SELECT s FROM gd_constraint GROUP BY id LIMIT 1) AS s",
		"CREATE VIEW gd_nested_derived AS SELECT s FROM (SELECT id,s,count(*) FROM gd_constraint GROUP BY id) d",
		"CREATE VIEW gd_nested_cte AS WITH d AS MATERIALIZED (SELECT id,s,count(*) FROM gd_constraint GROUP BY id) SELECT s FROM d",
	} {
		enginetest.Run(t, e, sql)
		names := []string{"gd_nested_scalar", "gd_nested_derived", "gd_nested_cte"}
		nested := stored.ViewOf(enginetest.PrimaryKeyUnitRelation(t, e, names[i]))
		refs, err := stored.QueryDependRefs(t.Context(), e.CatalogRead(), nil, nested.Query())
		if err != nil || !nested.DependsOn(proof.Constraint) || !slices.Contains(refs, proof.Constraint) {
			t.Fatal("nested lifetime or SQL projection omitted constraint proof", names[i], refs, err)
		}
	}
}

func TestGroupDependencyFrozenSystemAttribute(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE gd_system(id int PRIMARY KEY)")
	enginetest.Run(t, e, "CREATE VIEW gd_system_view AS SELECT id,count(*) FROM gd_system GROUP BY id HAVING tableoid IS NOT NULL AND tableoid IS NOT NULL ORDER BY tableoid")
	read := e.CatalogRead()
	owner := enginetest.PrimaryKeyUnitRelation(t, e, "gd_system_view")
	definition := stored.ViewOf(owner)
	aggregate := groupDependencyStoredAggregate(t, definition.Query().Root)
	if len(aggregate.Dependent) != 1 {
		t.Fatal("repeated system attribute did not share one retained payload")
	}
	retained, ok := aggregate.Dependent[0].(plan.SystemAttributeExpr)
	if !ok {
		t.Fatal("dependent payload lost its system attribute identity")
	}
	positions := make(map[parser.Position]bool)
	plan.VisitCTEQuery(definition.Query().Root, func(plan.Node) {}, func(bound expr.Expr) {
		plan.WalkExpression(bound, func(node expr.Expr) {
			if group, ok := node.(plan.GroupExpr); ok && group.Frame == retained.Frame && group.Slot == aggregate.DependentStart() {
				positions[group.SourcePosition()] = true
			}
		})
	}, true)
	captures := 0
	for _, column := range definition.Columns() {
		if column.SystemAttribute == retained.SubID {
			captures++
			if column.Owner != retained.Owner || column.Frame != retained.Frame || column.Occurrence != retained.QueryOccurrence {
				t.Fatal("system captures lost their lexical source identity")
			}
		}
	}
	if len(positions) != 3 || !positions[retained.SourcePosition()] || captures != 3 {
		t.Fatal("distinct HAVING and ORDER captures did not retain their own positions", positions, captures)
	}
	if err := definition.Validate(read, owner); err != nil {
		t.Fatal("interned system captures failed frozen publication", err)
	}
	for _, test := range []struct {
		name string
		slot int
	}{
		{name: "aggregate_call", slot: len(aggregate.Keys)},
		{name: "grouping_occurrence", slot: aggregate.DependentStart() - 1},
		{name: "outside_payload", slot: aggregate.DependentStart() + len(aggregate.Dependent)},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, err := plan.RewriteQueryExpressions(definition.Query().Root, func(bound expr.Expr) (expr.Expr, bool, error) {
				if group, ok := bound.(plan.GroupExpr); ok && group.Frame == retained.Frame && group.Slot == aggregate.DependentStart() {
					group.Slot = test.slot
					return group, true, nil
				}
				return bound, false, nil
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			candidate := definition.With(func(parts *stored.ViewParts) {
				query := *parts.Query
				query.Root = root
				parts.Query = &query
				parts.Frames, parts.Queries = analyze.CollectQueryFrames(root)
			})
			if err := candidate.Validate(read, owner); err == nil {
				t.Fatal("non-payload system capture slot accepted")
			}
		})
	}
	for _, test := range []struct {
		name string
		edit func(*plan.SystemAttributeExpr)
	}{
		{name: "wrong_occurrence", edit: func(n *plan.SystemAttributeExpr) { n.QueryOccurrence++ }},
		{name: "wrong_frame", edit: func(n *plan.SystemAttributeExpr) { n.Frame++ }},
		{name: "wrong_owner", edit: func(n *plan.SystemAttributeExpr) { n.Owner = ids.ObjectRef{} }},
		{name: "wrong_system_attribute", edit: func(n *plan.SystemAttributeExpr) { n.SubID-- }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := groupDependencyEditedView(t, definition, func(n *plan.AggregateNode) {
				system := n.Dependent[0].(plan.SystemAttributeExpr)
				test.edit(&system)
				n.Dependent[0] = system
			})
			if err := candidate.Validate(read, owner); err == nil {
				t.Fatal("substituted system payload identity accepted")
			}
		})
	}
	current := groupDependencyStoredAggregate(t, definition.Query().Root)
	if current != aggregate || &current.Dependent[0] != &aggregate.Dependent[0] || !reflect.DeepEqual(current.Dependent[0], retained) {
		t.Fatal("rejected candidate changed accepted predecessor backing or identity")
	}
	if err := definition.Validate(read, owner); err != nil {
		t.Fatal("rejected candidate invalidated immutable predecessor", err)
	}
}

func TestGroupDependencyOrdinaryAggregateLayout(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE gd_ordinary(id int,s text)")
	for _, test := range []struct {
		name  string
		query string
	}{
		{name: "global", query: "SELECT count(*) FROM gd_ordinary"},
		{name: "grouped", query: "SELECT id,count(*) FROM gd_ordinary GROUP BY id"},
		{name: "keys_only", query: "SELECT id FROM gd_ordinary GROUP BY id"},
	} {
		t.Run(test.name, func(t *testing.T) {
			name := "gd_ordinary_" + test.name
			enginetest.Run(t, e, "CREATE VIEW "+name+" AS "+test.query)
			read := e.CatalogRead()
			owner := enginetest.PrimaryKeyUnitRelation(t, e, name)
			definition := stored.ViewOf(owner)
			var aggregate *plan.AggregateNode
			plan.VisitCTEQuery(definition.Query().Root, func(node plan.Node) {
				if found, ok := node.(*plan.AggregateNode); ok {
					aggregate = found
				}
			}, func(expr.Expr) {}, true)
			if aggregate == nil || len(aggregate.Dependent) != 0 || len(aggregate.Dependencies) != 0 ||
				len(aggregate.Schema()) != len(aggregate.Keys)+len(aggregate.Calls)+1 {
				t.Fatal("ordinary aggregate does not retain its key/call/occurrence prefix")
			}
			if err := definition.Validate(read, owner); err != nil {
				t.Fatal("ordinary aggregate without dependency metadata rejected", err)
			}
			for _, layout := range []struct {
				name string
				edit func(*plan.AggregateNode)
			}{
				{name: "missing_slot", edit: func(n *plan.AggregateNode) {
					n.QuerySchema = n.QuerySchema[:len(n.QuerySchema)-1]
				}},
				{name: "extra_slot", edit: func(n *plan.AggregateNode) {
					n.QuerySchema = append(n.QuerySchema, n.QuerySchema[len(n.QuerySchema)-1])
				}},
			} {
				t.Run(layout.name, func(t *testing.T) {
					candidate := groupDependencyEditedView(t, definition, layout.edit)
					if err := candidate.Validate(read, owner); err == nil {
						t.Fatal("ordinary aggregate accepted malformed output width")
					}
					if err := definition.Validate(read, owner); err != nil {
						t.Fatal("candidate rejection changed retained ordinary aggregate", err)
					}
				})
			}
		})
	}
}

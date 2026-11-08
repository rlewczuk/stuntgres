package enginetests_test

import (
	"maps"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func groupDependencyRoutine(t *testing.T, read catalog.Read, name string) (*catalog.Procedure, *stored.RoutineProgram) {
	t.Helper()
	for _, descriptor := range read.Descriptors() {
		if d, ok := descriptor.(*catalog.Procedure); ok && d.Name() == name {
			p, err := stored.RoutineProgramOf(d)
			if err != nil {
				t.Fatal(err)
			}
			return d, p
		}
	}
	t.Fatal("missing accepted routine", name)
	return nil, nil
}

func groupDependencyRoutineFixture(t *testing.T, body string) (*core.Engine, catalog.Read, *catalog.Procedure, *stored.RoutineProgram) {
	t.Helper()
	e := engine.New()
	for _, sql := range []string{
		"CREATE TABLE gd_routine_t(id int PRIMARY KEY,s text)",
		"CREATE TABLE gd_routine_other(id int PRIMARY KEY,s text)",
		"CREATE TABLE gd_routine_sink(id int,s text,a int[])",
		"CREATE FUNCTION gd_routine_f() RETURNS text LANGUAGE SQL BEGIN ATOMIC " + body + "; SELECT NULL::text; END",
	} {
		enginetest.Run(t, e, sql)
	}
	read := e.CatalogRead()
	d, p := groupDependencyRoutine(t, read, "gd_routine_f")
	return e, read, d, p
}

func groupDependencyRoutineEdges(t *testing.T, read catalog.Read, p *stored.RoutineProgram) []ids.ObjectAddress {
	t.Helper()
	var edges []ids.ObjectAddress
	if !p.VisitGroupingConstraintDependencies(read, func(a ids.ObjectAddress) bool { edges = append(edges, a); return true }) {
		t.Fatal("genuine routine projection stopped")
	}
	return edges
}

func TestGroupDependencyRoutineRoots(t *testing.T) {
	group := "(SELECT s FROM gd_routine_t GROUP BY id)"
	key := "(SELECT id FROM gd_routine_t GROUP BY id HAVING s IS NOT NULL)"
	for _, tc := range []struct{ name, body string }{
		{"select", "SELECT s FROM gd_routine_t GROUP BY id"},
		{"return", "SELECT s FROM gd_routine_t GROUP BY id HAVING s IS NOT NULL ORDER BY s LIMIT 1"},
		{"system_only", "SELECT tableoid FROM gd_routine_t GROUP BY id"},
		{"scalar", "SELECT " + group},
		{"correlated", "SELECT (SELECT s) FROM gd_routine_t GROUP BY id"},
		{"derived", "SELECT s FROM (SELECT s FROM gd_routine_t GROUP BY id) d"},
		{"set", "SELECT s FROM gd_routine_t GROUP BY id UNION ALL SELECT s FROM gd_routine_t GROUP BY id"},
		{"cte", "WITH q AS MATERIALIZED (SELECT s FROM gd_routine_t GROUP BY id) SELECT s FROM q"},
		{"unused_cte", "WITH q AS (SELECT s FROM gd_routine_t GROUP BY id) SELECT NULL::text"},
		{"recursive_cte", "WITH RECURSIVE q(s) AS (SELECT s FROM gd_routine_t GROUP BY id UNION ALL SELECT s FROM q WHERE false) SELECT s FROM q"},
		{"insert_select", "INSERT INTO gd_routine_sink(s) SELECT s FROM gd_routine_t GROUP BY id"},
		{"insert_values_returning", "INSERT INTO gd_routine_sink(s) VALUES (" + group + ") RETURNING " + group},
		{"update_from", "UPDATE gd_routine_sink SET s=d.s FROM (SELECT s FROM gd_routine_t GROUP BY id) d"},
		{"update_scalar", "UPDATE gd_routine_sink SET s=" + group + " WHERE s=" + group + " RETURNING " + group},
		{"update_subscript", "UPDATE gd_routine_sink SET a[" + key + "]=1"},
		{"update_subscript_slice", "UPDATE gd_routine_sink SET a[" + key + ":" + key + "]=ARRAY[1]"},
		{"delete_using", "DELETE FROM gd_routine_sink USING (SELECT s FROM gd_routine_t GROUP BY id) d WHERE gd_routine_sink.s=d.s"},
		{"delete_scalar", "DELETE FROM gd_routine_sink WHERE s=" + group + " RETURNING " + group},
		{"merge_source", "MERGE INTO gd_routine_sink d USING (SELECT s FROM gd_routine_t GROUP BY id) q ON d.s=q.s WHEN MATCHED THEN UPDATE SET s=q.s"},
		{"merge_arms", "MERGE INTO gd_routine_sink d USING gd_routine_other q ON d.id=q.id WHEN MATCHED AND d.s=" + group + " THEN UPDATE SET s=" + group + " WHEN NOT MATCHED THEN INSERT(s) VALUES (" + group + ")"},
		{"merge_on_returning", "MERGE INTO gd_routine_sink d USING gd_routine_other q ON d.s=" + group + " WHEN MATCHED THEN DO NOTHING RETURNING " + group},
		{"modifier_update", "WITH q AS (UPDATE gd_routine_sink SET s=" + group + " RETURNING s) SELECT s FROM q"},
		{"modifier_insert", "WITH q AS (INSERT INTO gd_routine_sink(s) SELECT s FROM gd_routine_t GROUP BY id RETURNING s) SELECT s FROM q"},
		{"modifier_delete", "WITH q AS (DELETE FROM gd_routine_sink USING (SELECT s FROM gd_routine_t GROUP BY id) d RETURNING gd_routine_sink.s) SELECT s FROM q"},
		{"modifier_merge", "WITH q AS (MERGE INTO gd_routine_sink d USING (SELECT s FROM gd_routine_t GROUP BY id) q ON d.s=q.s WHEN MATCHED THEN UPDATE SET s=q.s RETURNING d.s) SELECT s FROM q"},
		{"unused_modifier", "WITH q AS (UPDATE gd_routine_sink SET s=" + group + " RETURNING s) SELECT NULL::text"},
		{"multi_modifier_nested_queries", "WITH q AS (UPDATE gd_routine_sink SET s=(WITH grouped AS (SELECT s FROM gd_routine_t GROUP BY id) SELECT s FROM grouped) RETURNING s), r AS (INSERT INTO gd_routine_sink(s) SELECT " + group + " FROM q RETURNING s) SELECT s FROM r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, read, d, p := groupDependencyRoutineFixture(t, tc.body)
			parent := enginetest.PrimaryKeyUnitRelation(t, e, "gd_routine_t")
			if tc.name == "multi_modifier_nested_queries" {
				modifiers := p.Action(0).Parts().ModifierPresentations
				if len(modifiers) != 2 {
					t.Fatal("incomplete modifier chain", len(modifiers))
				}
				for _, presentation := range modifiers {
					groupDependencyStoredAggregate(t, presentation.Query().Root)
				}
			}
			want := ids.ObjectAddress{ClassID: 2606, ObjectID: parent.Constraints.Unique[0].OID}
			if err := p.ValidateMetadata(read); err != nil {
				t.Fatal("metadata", err)
			}
			if err := p.Validate(read); err != nil {
				t.Fatal("action/publication", err)
			}
			if got := groupDependencyRoutineEdges(t, read, p); !slices.Equal(got, []ids.ObjectAddress{want}) {
				t.Fatal("root coverage", got, want)
			}
			// The retained recursive baseline is independent of parent projection.
			baseline := map[ids.ObjectAddress]bool{}
			for i := range p.Len() {
				a := p.Action(i)
				presentations := []*stored.View{a.Presentation()}
				for _, v := range a.Parts().ModifierPresentations {
					presentations = append(presentations, v)
				}
				for _, v := range presentations {
					plan.VisitCTEQuery(v.Query().Root, func(n plan.Node) {
						if a, ok := n.(*plan.AggregateNode); ok {
							for _, proof := range a.Dependencies {
								baseline[proof.Constraint] = true
							}
						}
					}, func(expr.Expr) {}, true)
					for _, dep := range v.Dependencies() {
						if dep.Ref.Address.ClassID == 2606 {
							t.Fatal("presentation constraint descriptor")
						}
					}
				}
				for _, dep := range a.Binding().Dependencies {
					if dep.Ref.Address.ClassID == 2606 {
						t.Fatal("action constraint descriptor")
					}
				}
			}
			if len(baseline) != 1 || !baseline[want] {
				t.Fatal("accepted root baseline", baseline)
			}
			for _, dep := range p.Dependencies() {
				if dep.Ref.Address.ClassID == 2606 {
					t.Fatal("union constraint descriptor")
				}
			}
			seen := 0
			catalog.VisitProcedureCapturedDefinitionDependencies(read, read, d, func(a ids.ObjectAddress) bool {
				if a == want {
					seen++
				}
				return true
			})
			if seen != 1 {
				t.Fatal("shared catalog caller", seen)
			}
			calls := 0
			if p.VisitGroupingConstraintDependencies(read, func(ids.ObjectAddress) bool { calls++; return false }) || calls != 1 {
				t.Fatal("callback stop", calls)
			}
			if allocs := testing.AllocsPerRun(10, func() { p.VisitGroupingConstraintDependencies(read, func(ids.ObjectAddress) bool { return true }) }); allocs != 0 {
				t.Fatal("projection allocations", allocs)
			}
		})
	}
}

func TestGroupDependencyRoutineDedup(t *testing.T) {
	e, read, _, p := groupDependencyRoutineFixture(t, "SELECT a.s,b.s FROM gd_routine_t a JOIN gd_routine_t b ON a.id=b.id GROUP BY a.id,b.id; SELECT s FROM gd_routine_other GROUP BY id; SELECT s FROM gd_routine_t GROUP BY id")
	want := map[ids.ObjectAddress]bool{}
	for _, name := range []string{"gd_routine_t", "gd_routine_other"} {
		parent := enginetest.PrimaryKeyUnitRelation(t, e, name)
		want[ids.ObjectAddress{ClassID: 2606, ObjectID: parent.Constraints.Unique[0].OID}] = true
	}
	for _, edge := range groupDependencyRoutineEdges(t, read, p) {
		if !want[edge] {
			t.Fatal("duplicate/forged edge", edge)
		}
		delete(want, edge)
	}
	if len(want) != 0 {
		t.Fatal("missing distinct parent", want)
	}
	calls := 0
	if p.VisitGroupingConstraintDependencies(read, func(ids.ObjectAddress) bool { calls++; return false }) || calls != 1 {
		t.Fatal("distinct parent early stop", calls)
	}
}

func TestGroupDependencyRoutineAuthentication(t *testing.T) {
	for _, kind := range []string{"missing_proof", "missing_payload", "missing_both", "foreign", "stale", "missing_epoch", "wrong_parent", "forged_constraint", "constraint_subobject", "constraint_class", "parent_subobject", "changed_attributes", "frame", "occurrence", "layout", "forged_payload", "missing_frame", "missing_parent"} {
		t.Run(kind, func(t *testing.T) {
			e, read, d, original := groupDependencyRoutineFixture(t, "SELECT s FROM gd_routine_t GROUP BY id")
			action := original.Action(0)
			old := groupDependencyStoredAggregate(t, action.Presentation().Query().Root)
			proof := old.Dependencies[0]
			attributes := slices.Clone(proof.Attributes)
			before := slices.Clone(original.Dependencies())
			presentation := groupDependencyEditedView(t, action.Presentation(), func(n *plan.AggregateNode) {
				switch kind {
				case "missing_proof":
					n.Dependencies = nil
				case "missing_payload":
					n.Dependent = nil
				case "missing_both":
					n.Dependent, n.Dependencies = nil, nil
				case "foreign":
					n.Dependencies[0].Parent = new(ids.Domain).Ref(read.Epoch(), proof.Parent.Address)
				case "stale":
					n.Dependencies[0].Parent = read.Domain().Ref(read.Epoch()+1, proof.Parent.Address)
				case "missing_epoch":
					n.Dependencies[0].Parent = read.Domain().Ref(0, proof.Parent.Address)
				case "wrong_parent":
					n.Dependencies[0].Parent = read.Domain().Ref(read.Epoch(), enginetest.PrimaryKeyUnitRelation(t, e, "gd_routine_other").Address())
				case "forged_constraint":
					n.Dependencies[0].Constraint.ObjectID++
				case "constraint_subobject":
					n.Dependencies[0].Constraint.SubID = 1
				case "constraint_class":
					n.Dependencies[0].Constraint.ClassID = ids.RelationClassID
				case "parent_subobject":
					n.Dependencies[0].Parent.Address.SubID = 1
				case "changed_attributes":
					n.Dependencies[0].Attributes[0]++
				case "frame":
					n.Dependencies[0].Frame++
				case "occurrence":
					n.Dependencies[0].Occurrence++
				case "layout":
					n.QuerySchema = n.QuerySchema[:len(n.QuerySchema)-1]
				case "forged_payload":
					column := n.Dependent[0].(plan.ColumnExpr)
					column.Slot++
					n.Dependent[0] = column
				}
			})
			if kind == "missing_frame" {
				presentation = presentation.With(func(v *stored.ViewParts) { v.Frames = nil })
			}
			candidate := action.With(func(a *stored.RuleActionParts) {
				a.Presentation = presentation
				if kind == "missing_parent" {
					a.Binding.QuerySources = nil
					remove := func(ds []catalog.ExpressionDependency) []catalog.ExpressionDependency {
						return slices.DeleteFunc(slices.Clone(ds), func(d catalog.ExpressionDependency) bool {
							return d.Ref.Address.ClassID == proof.Parent.Address.ClassID && d.Ref.Address.ObjectID == proof.Parent.Address.ObjectID
						})
					}
					a.Binding.Dependencies = remove(a.Binding.Dependencies)
					a.Presentation = a.Presentation.With(func(v *stored.ViewParts) { v.Dependencies = remove(v.Dependencies) })
				}
			})
			bad := stored.NewRoutineProgram([]stored.RuleAction{candidate, original.Action(1)})
			if err := bad.ValidateMetadata(read); err == nil {
				t.Error("metadata accepted", kind)
			}
			if err := bad.ValidateAction(read, 0); err == nil {
				t.Error("action accepted", kind)
			}
			if err := bad.Validate(read); err == nil {
				t.Error("publication accepted", kind)
			}
			copy := *d
			copy.SQLBody = bad
			if err := (stored.ProcedureProvider{}).Validate(read, &copy); err == nil {
				t.Error("provider publication accepted", kind)
			}
			if _, err := bad.Rebind(read, nil, stored.NewRoutineRemap(read, nil, nil, nil, false)); err == nil {
				t.Error("rebind accepted", kind)
			}
			if !slices.Equal(before, original.Dependencies()) || !slices.Equal(attributes, old.Dependencies[0].Attributes) || old.Dependencies[0].Parent != proof.Parent || old.Dependencies[0].Constraint != proof.Constraint {
				t.Fatal("mutated predecessor")
			}
			if err := original.Validate(read); err != nil {
				t.Fatal("predecessor lost authority", err)
			}
		})
	}
}

func TestGroupDependencyRoutineMissingModifier(t *testing.T) {
	_, read, _, p := groupDependencyRoutineFixture(t, "WITH q AS (UPDATE gd_routine_sink SET s=(SELECT s FROM gd_routine_t GROUP BY id) RETURNING s) SELECT NULL::text")
	for _, missing := range []bool{false, true} {
		a := p.Action(0).With(func(a *stored.RuleActionParts) {
			a.ModifierPresentations = maps.Clone(a.ModifierPresentations)
			for statement, v := range a.ModifierPresentations {
				if missing {
					delete(a.ModifierPresentations, statement)
				} else {
					a.ModifierPresentations[statement] = v.With(func(v *stored.ViewParts) { v.Frames = nil })
				}
			}
		})
		bad := stored.NewRoutineProgram([]stored.RuleAction{a, p.Action(1)})
		if err := bad.ValidateMetadata(read); err == nil {
			t.Fatal("missing modifier metadata accepted", missing)
		}
		if err := bad.ValidateAction(read, 0); err == nil {
			t.Fatal("missing modifier action accepted", missing)
		}
	}
}

func TestGroupDependencyRoutineReturn(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE gd_routine_return(id int PRIMARY KEY,s text)")
	enginetest.Run(t, e, "CREATE FUNCTION gd_routine_return_f() RETURNS text LANGUAGE SQL RETURN (SELECT s FROM gd_routine_return GROUP BY id)")
	read := e.CatalogRead()
	_, p := groupDependencyRoutine(t, read, "gd_routine_return_f")
	if len(groupDependencyRoutineEdges(t, read, p)) != 1 {
		t.Fatal("RETURN root lost proof")
	}
}

// A mutation's scalar query is a separate frozen frame. Rewriting only the
// mutation's unary query graph does not enter that expression-owned query.
// Corrupt its actual retained capture while leaving the binding/query graph
// untouched, so validation must authenticate both owners and their association.
func groupDependencyRoutineEditedScalarFrame(t *testing.T, original *stored.View, edit func(*plan.AggregateNode)) (*stored.View, *plan.AggregateNode, *plan.AggregateNode) {
	t.Helper()
	predecessor := groupDependencyStoredAggregate(t, original.Query().Root)
	frame := predecessor.Dependencies[0].Frame
	project := original.Frames()[frame]
	if project == nil {
		t.Fatal("missing accepted scalar frame", frame)
	}
	scalar := original.With(func(v *stored.ViewParts) {
		query := *v.Query
		query.Root = project
		v.Query = &query
	})
	var corrupted *plan.AggregateNode
	edits := 0
	changed := groupDependencyEditedView(t, scalar, func(n *plan.AggregateNode) {
		edits++
		edit(n)
		corrupted = n
	})
	if edits != 1 || corrupted == predecessor || changed.Query().Root == project {
		t.Fatal("fixture did not replace the retained scalar grouping capture", edits)
	}
	replacement := changed.Query().Root.(*plan.ProjectNode)
	return original.With(func(v *stored.ViewParts) {
		v.Frames = maps.Clone(v.Frames)
		v.Queries = maps.Clone(v.Queries)
		v.Frames[frame] = replacement
		v.Queries[project.Syntax] = replacement
	}), predecessor, corrupted
}

func TestGroupDependencyRoutineModifierAuthentication(t *testing.T) {
	for _, kind := range []string{"foreign", "constraint", "layout", "source", "source_node", "forged_payload", "missing_proof", "missing_payload", "missing_both", "omitted_graph"} {
		t.Run(kind, func(t *testing.T) {
			_, read, d, p := groupDependencyRoutineFixture(t, "WITH q AS (UPDATE gd_routine_sink SET s=(SELECT s FROM gd_routine_t GROUP BY id) RETURNING s) SELECT NULL::text")
			beforeDependencies := slices.Clone(p.Dependencies())
			beforeEdges := groupDependencyRoutineEdges(t, read, p)
			var predecessor, corrupted *plan.AggregateNode
			var beforeProof plan.GroupDependency
			var beforeAttributes []int32
			var beforeSchema plan.QuerySchema
			var beforePayload plan.ColumnExpr
			action := p.Action(0).With(func(a *stored.RuleActionParts) {
				a.ModifierPresentations = maps.Clone(a.ModifierPresentations)
				for statement, presentation := range a.ModifierPresentations {
					if kind == "omitted_graph" {
						a.ModifierPresentations[statement] = p.Action(1).Presentation()
						continue
					}
					predecessor = groupDependencyStoredAggregate(t, presentation.Query().Root)
					beforeProof = predecessor.Dependencies[0]
					beforeAttributes = slices.Clone(beforeProof.Attributes)
					beforeSchema = slices.Clone(predecessor.QuerySchema)
					beforePayload = predecessor.Dependent[0].(plan.ColumnExpr)
					var changed *stored.View
					changed, predecessor, corrupted = groupDependencyRoutineEditedScalarFrame(t, presentation, func(n *plan.AggregateNode) {
						switch kind {
						case "foreign":
							n.Dependencies[0].Parent = new(ids.Domain).Ref(read.Epoch(), n.Dependencies[0].Parent.Address)
						case "constraint":
							n.Dependencies[0].Constraint.ObjectID++
						case "layout":
							n.QuerySchema = n.QuerySchema[:len(n.QuerySchema)-1]
						case "source":
							n.Dependencies[0].Occurrence++
						case "source_node":
							var err error
							n.Child, err = plan.RewriteQueryExpressions(n.Child, func(bound expr.Expr) (expr.Expr, bool, error) {
								return bound, false, nil
							}, func(node plan.Node) (plan.Node, error) {
								if source, ok := node.(*plan.SourceNode); ok {
									copy := *source
									copy.Ref = new(ids.Domain).Ref(read.Epoch(), source.Ref.Address)
									return &copy, nil
								}
								return node, nil
							})
							if err != nil {
								t.Fatal(err)
							}
						case "forged_payload":
							column := n.Dependent[0].(plan.ColumnExpr)
							column.Slot++
							n.Dependent[0] = column
						case "missing_proof":
							n.Dependencies = nil
						case "missing_payload":
							n.Dependent = nil
						case "missing_both":
							n.Dependent, n.Dependencies = nil, nil
						}
					})
					if changed.Query() != presentation.Query() || changed.Frames()[beforeProof.Frame] == presentation.Frames()[beforeProof.Frame] {
						t.Fatal("fixture did not isolate the frozen scalar owner")
					}
					a.ModifierPresentations[statement] = changed
				}
			})
			bad := stored.NewRoutineProgram([]stored.RuleAction{action, p.Action(1)})
			if err := bad.ValidateMetadata(read); err == nil {
				t.Error("modifier metadata accepted", kind)
			}
			if err := bad.ValidateAction(read, 0); err == nil {
				t.Error("modifier action accepted", kind)
			}
			if err := bad.Validate(read); err == nil {
				t.Error("modifier validation accepted", kind)
			}
			copy := *d
			copy.SQLBody = bad
			if err := (stored.ProcedureProvider{}).Validate(read, &copy); err == nil {
				t.Error("modifier publication accepted", kind)
			}
			if _, err := bad.Rebind(read, nil, stored.NewRoutineRemap(read, nil, nil, nil, false)); err == nil {
				t.Error("modifier rebind accepted", kind)
			}
			if !slices.Equal(beforeDependencies, p.Dependencies()) || !slices.Equal(beforeEdges, groupDependencyRoutineEdges(t, read, p)) {
				t.Fatal("modifier predecessor dependencies mutated")
			}
			if predecessor != nil && (corrupted == predecessor || predecessor.Dependencies[0].Parent != beforeProof.Parent || predecessor.Dependencies[0].Constraint != beforeProof.Constraint || !slices.Equal(predecessor.Dependencies[0].Attributes, beforeAttributes) || !slices.Equal(predecessor.QuerySchema, beforeSchema)) {
				t.Fatal("modifier predecessor capture mutated")
			}
			if predecessor != nil && (len(predecessor.Dependent) != 1 || predecessor.Dependent[0].(plan.ColumnExpr).Slot != beforePayload.Slot || predecessor.Dependent[0].(plan.ColumnExpr).Frame != beforePayload.Frame || predecessor.Dependent[0].(plan.ColumnExpr).Origin != beforePayload.Origin) {
				t.Fatal("modifier predecessor payload mutated")
			}
			if err := p.Validate(read); err != nil {
				t.Fatal("modifier predecessor mutated", err)
			}
		})
	}
}

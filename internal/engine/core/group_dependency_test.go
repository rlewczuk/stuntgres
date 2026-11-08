package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func groupDependencyBinding(t *testing.T, e *core.Engine, sql string) analyze.Binding {
	t.Helper()
	statement := enginetest.Statement(t, sql)
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	authority, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := core.Describe(op.Ctx, e.AnalysisHost(op), statement, authority, ids.ObjectRef{}, sqltype.InputEnvironment{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func groupDependencyAggregates(root plan.Node) []*plan.AggregateNode {
	var aggregates []*plan.AggregateNode
	seen := make(map[*plan.AggregateNode]bool)
	plan.VisitCTEQuery(root, func(node plan.Node) {
		if aggregate, ok := node.(*plan.AggregateNode); ok && !seen[aggregate] {
			seen[aggregate] = true
			aggregates = append(aggregates, aggregate)
		}
	}, func(expr.Expr) {}, true)
	return aggregates
}

func TestGroupDependencyPayloadLayout(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE gd_layout(id int PRIMARY KEY,payload json,s text,hidden int)")
	binding := groupDependencyBinding(t, e, "SELECT id,payload,payload,(SELECT s),count(*) FROM gd_layout GROUP BY id HAVING hidden>0 ORDER BY s")
	project := binding.Query.Root.(*plan.ProjectNode)
	aggregate := groupDependencyAggregates(project)[0]
	if len(aggregate.Keys) != 1 || len(aggregate.Grouping) != 1 || len(aggregate.Calls) != 1 || aggregate.DependentStart() != 3 || len(aggregate.Dependent) != 3 || len(aggregate.Schema()) != 6 || len(project.Schema()) != 5 {
		t.Fatal("group prefix or demanded payload width changed", aggregate, project.Schema())
	}
	if project.Expressions[0].(plan.GroupExpr).Slot != 0 || project.Expressions[1].(plan.GroupExpr).Slot != 3 || project.Expressions[2].(plan.GroupExpr).Slot != 3 || project.Expressions[4].(plan.GroupExpr).Slot != 1 || aggregate.Schema()[2].Usage.Type.ID() != sqltype.Int4 {
		t.Fatal("key/call/occurrence/payload slot layout or interning changed")
	}
	if aggregate.Dependent[0].ResultUsage().Type.ID() != sqltype.JSON {
		t.Fatal("JSON payload did not retain its genuine source usage")
	}
	var correlation plan.GroupExpr
	foundCorrelation := false
	plan.VisitCTEQuery(project, func(plan.Node) {}, func(bound expr.Expr) {
		subquery, ok := bound.(plan.SubqueryExpr)
		if !ok {
			return
		}
		inner := subquery.QueryPlan.Root.(*plan.ProjectNode)
		if grouped, ok := inner.Expressions[0].(plan.GroupExpr); ok {
			correlation, foundCorrelation = grouped, true
		}
	}, true)
	if !foundCorrelation || correlation.Frame != project.Frame || correlation.Slot != 4 {
		t.Fatal("outer reference did not address the actual outer grouped frame", correlation)
	}
	if len(aggregate.Dependencies) != 1 || aggregate.Dependencies[0].Parent != binding.QuerySources[0].Ref {
		t.Fatal("missing actual occurrence proof")
	}
	for _, dependency := range binding.Dependencies {
		if dependency.Ref.Address.ClassID == 2606 {
			t.Fatal("constraint address leaked into standalone descriptor manifest")
		}
	}
	for _, sql := range []string{
		"SELECT id,payload,count(*) FROM gd_layout GROUP BY id,s,hidden",
		"SELECT id,count(*) FROM gd_layout GROUP BY id HAVING s IS NOT NULL ORDER BY s",
		"SELECT id,(gd_layout).s,count(*) FROM gd_layout GROUP BY id",
	} {
		next := groupDependencyAggregates(groupDependencyBinding(t, e, sql).Query.Root)[0]
		if len(next.Dependent) != 1 {
			t.Fatal("superset or hidden consumers retained undemanded payload", len(next.Dependent))
		}
	}
	window := groupDependencyAggregates(groupDependencyBinding(t, e, "SELECT id,first_value(s) OVER(ORDER BY hidden),count(*) FROM gd_layout GROUP BY id").Query.Root)[0]
	if len(window.Dependent) != 2 || len(window.Schema()) != window.DependentStart()+2 {
		t.Fatal("late window demands escaped the finalized immutable aggregate")
	}
	whole := groupDependencyBinding(t, e, "SELECT p.id,q.id,q,count(*) FROM gd_layout p LEFT JOIN gd_layout q ON p.id=q.id GROUP BY p.id,q.id")
	wholeAggregate := groupDependencyAggregates(whole.Query.Root)[0]
	if len(wholeAggregate.Dependent) != 1 {
		t.Fatal("whole row did not retain one demanded representative")
	}
	row, ok := wholeAggregate.Dependent[0].(plan.RowExpr)
	if !ok || row.PresenceSlot == 0 || row.PresenceFrame != whole.Query.Root.(*plan.ProjectNode).Frame {
		t.Fatal("whole-row presence was detached from its input occurrence")
	}
	for _, expression := range whole.Query.Root.(*plan.ProjectNode).Expressions {
		plan.WalkExpression(expression, func(bound expr.Expr) {
			if row, ok := bound.(plan.RowExpr); ok && row.PresenceSlot != 0 {
				t.Fatal("source presence read remains above the aggregate")
			}
		})
	}
}

func TestGroupDependencyQueryTransforms(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE gd_transform(id int PRIMARY KEY,s text)")
	binding := groupDependencyBinding(t, e, "SELECT id,s,count(*) FROM gd_transform GROUP BY id")
	root := binding.Query.Root
	aggregate := groupDependencyAggregates(root)[0]
	payload, proof := aggregate.Dependent[0], aggregate.Dependencies[0]
	unchanged, err := plan.RewriteQueryExpressions(root, func(bound expr.Expr) (expr.Expr, bool, error) {
		return bound, false, nil
	})
	if err != nil || unchanged != root || &groupDependencyAggregates(unchanged)[0].Dependent[0] != &aggregate.Dependent[0] {
		t.Fatal("unchanged traversal copied immutable backing", err)
	}
	visits := 0
	plan.QueryExpressions(aggregate, func(bound expr.Expr) {
		if column, ok := bound.(plan.ColumnExpr); ok && column.Slot == payload.(plan.ColumnExpr).Slot {
			visits++
		}
	})
	if visits != 1 {
		t.Fatal("payload did not participate exactly once in expression visitation", visits)
	}
	changed, err := plan.RewriteQueryExpressions(root, func(bound expr.Expr) (expr.Expr, bool, error) {
		if column, ok := bound.(plan.ColumnExpr); ok && column.Slot == payload.(plan.ColumnExpr).Slot {
			column.Slot++
			return column, true, nil
		}
		return bound, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	next := groupDependencyAggregates(changed)[0]
	if changed == root || next == aggregate || &next.Dependent[0] == &aggregate.Dependent[0] || aggregate.Dependent[0].(plan.ColumnExpr).Slot != payload.(plan.ColumnExpr).Slot || &next.Dependencies[0] != &aggregate.Dependencies[0] {
		t.Fatal("changed transform mutated original or copied untouched proof backing")
	}
	comparison := analyze.QueryTargetComparison{}
	if comparison.SameQuery(&analyze.Binder{}, root, changed) {
		t.Fatal("semantic query equality ignored dependent payload")
	}
	var before, after strings.Builder
	if err := plan.AppendQueryTree(t.Context(), &before, root); err != nil {
		t.Fatal(err)
	}
	if err := plan.AppendQueryTree(t.Context(), &after, changed); err != nil || before.String() == after.String() {
		t.Fatal("structural reconstruction omitted dependent expression", err)
	}
	proofChanged, err := plan.RewriteQueryExpressions(root, func(bound expr.Expr) (expr.Expr, bool, error) {
		return bound, false, nil
	}, func(node plan.Node) (plan.Node, error) {
		if node == aggregate {
			copy := *aggregate
			copy.Dependencies = slices.Clone(aggregate.Dependencies)
			copy.Dependencies[0].Constraint.ObjectID++
			return &copy, nil
		}
		return node, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	comparison = analyze.QueryTargetComparison{}
	if comparison.SameQuery(&analyze.Binder{}, root, proofChanged) {
		t.Fatal("semantic query equality ignored exact supporting constraint")
	}
	after.Reset()
	if err := plan.AppendQueryTree(t.Context(), &after, proofChanged); err != nil || before.String() == after.String() {
		t.Fatal("structural reconstruction omitted constraint proof", err)
	}
	mapper := &stored.ViewQueryRemapper{Catalog: e.CatalogRead(), Ref: func(ref ids.ObjectRef) ids.ObjectRef { return ref }, Type: func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }}
	remapped, err := mapper.Nested(root)
	if err != nil {
		t.Fatal(err)
	}
	mappedAggregate := groupDependencyAggregates(remapped)[0]
	if &mappedAggregate.Dependent[0] != &aggregate.Dependent[0] || &mappedAggregate.Dependencies[0] != &aggregate.Dependencies[0] {
		t.Fatal("identity remapping copied untouched payload/proof backing")
	}
	for _, sql := range []string{
		"SELECT (SELECT s FROM gd_transform GROUP BY id LIMIT 1)",
		"SELECT d.s FROM (SELECT id,s,count(*) FROM gd_transform GROUP BY id) d",
		"WITH d AS MATERIALIZED (SELECT id,s,count(*) FROM gd_transform GROUP BY id) SELECT s FROM d",
		"WITH d AS MATERIALIZED (SELECT id,s,count(*) FROM gd_transform GROUP BY id) SELECT p.s,q.s FROM d p JOIN d q ON p.id=q.id",
		"SELECT p.s,q.s,count(*) FROM gd_transform p JOIN gd_transform q ON p.id=q.id GROUP BY p.id,q.id",
		"SELECT id,tableoid,count(*) FROM gd_transform GROUP BY id",
		"SELECT p.id,q.id,q,count(*) FROM gd_transform p LEFT JOIN gd_transform q ON p.id=q.id GROUP BY p.id,q.id",
	} {
		query := groupDependencyBinding(t, e, sql).Query.Root
		originals := groupDependencyAggregates(query)
		mapping := stored.NewRuleTreeMapper(e.CatalogRead(), func(frame uint32) uint32 { return frame + 10 }, 7, nil)
		mapped, err := mapping.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		mappedNodes := groupDependencyAggregates(mapped)
		if len(originals) == 0 || len(mappedNodes) != len(originals) {
			t.Fatal("nested aggregate traversal lost roots")
		}
		for i, original := range originals {
			for j, old := range original.Dependencies {
				got := mappedNodes[i].Dependencies[j]
				if got.Frame != old.Frame+10 || got.Occurrence != old.Occurrence+7 || got.Parent != old.Parent || got.Constraint != old.Constraint || original.Dependencies[j].Frame != old.Frame {
					t.Fatal("nested proof remap lost frame/alias identity", got)
				}
			}
		}
		comparison := analyze.QueryTargetComparison{}
		if !comparison.SameQuery(&analyze.Binder{}, query, mapped) {
			t.Fatalf("equivalent frame/occurrence remap changed query semantics: %s", sql)
		}
		if len(mappedNodes[0].Dependencies) == 2 {
			// Both aliases use the same catalog parent. Swapping only their
			// proof occurrences must not pass as an equivalent remapping.
			broken, err := plan.RewriteQueryExpressions(mapped, func(bound expr.Expr) (expr.Expr, bool, error) {
				return bound, false, nil
			}, func(node plan.Node) (plan.Node, error) {
				if node != mappedNodes[0] {
					return node, nil
				}
				copy := *mappedNodes[0]
				copy.Dependencies = slices.Clone(copy.Dependencies)
				copy.Dependencies[0].Occurrence, copy.Dependencies[1].Occurrence = copy.Dependencies[1].Occurrence, copy.Dependencies[0].Occurrence
				return &copy, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			comparison := analyze.QueryTargetComparison{}
			if comparison.SameQuery(&analyze.Binder{}, query, broken) {
				t.Fatal("query comparison merged distinct alias proof occurrences")
			}
		}
		scans := 0
		split, err := plan.RewriteQueryExpressions(mapped, func(bound expr.Expr) (expr.Expr, bool, error) {
			return bound, false, nil
		}, func(node plan.Node) (plan.Node, error) {
			scan, ok := node.(*plan.DerivedNode)
			if !ok || scan.CTE == nil {
				return node, nil
			}
			scans++
			if scans != 2 {
				return node, nil
			}
			copy, definition := *scan, *scan.CTE
			copy.CTE = &definition
			return &copy, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if scans == 2 {
			comparison := analyze.QueryTargetComparison{}
			if comparison.SameQuery(&analyze.Binder{}, query, split) {
				t.Fatal("query comparison lost materialized CTE producer sharing")
			}
		}
	}
	if aggregate.Dependencies[0].Constraint != proof.Constraint || !slices.Equal(aggregate.Dependencies[0].Attributes, proof.Attributes) {
		t.Fatal("transforms changed accepted proof authority")
	}
}

func TestGroupDependencyPreparedInvalidation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE gd_prepared(id int PRIMARY KEY,s text)")
	enginetest.SessionSQL(t, s, "INSERT INTO gd_prepared VALUES(1,'one')")
	enginetest.SessionSQL(t, s, "SET extra_float_digits=3")
	enginetest.SessionSQL(t, s, "BEGIN")
	defer enginetest.SessionSQL(t, s, "ROLLBACK")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) {
		enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn)
	}
	command(func(scope *core.StatementScope) error {
		// Aggregate inputs are genuinely foldable before grouping; a scalar
		// wrapper above the group is deliberately marked stage-dependent.
		_, err := scope.ParseProtocol("gd_owned", "SELECT id,s,count(*),min((point '0.10000000000000002,1')::text) FROM gd_prepared GROUP BY id", nil)
		return err
	})
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "gd_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("gd_owned")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing actual parsed definition")
	}
	owner := definition.Description.Folds
	if !owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse did not retain an unevaluated definition owner")
	}
	bind("before")
	portal := registry.Binding("before")
	if portal == nil || portal.Resource == nil || portal.Resource.Root == nil || portal.Folds == nil || portal.Entry.Description.Folds != owner {
		t.Fatal("missing genuinely accepted binding graph/folds")
	}
	oldGraph, oldAccepted := portal.Resource.Root, portal.Folds
	oldGroup := *oldAccepted
	oldAggregate := groupDependencyAggregates(oldGraph)[0]
	oldProof := oldAggregate.Dependencies[0]
	oldPayload := oldAggregate.Dependent[0]
	analysis := portal.Entry.Description.Analysis
	accepted := owner.Current()
	entries := slices.Clone(accepted.Entries)
	dependencies := slices.Clone(analysis.Dependencies)
	if owner.Analysis != analysis || len(entries) == 0 || len(oldGroup.Entries) == 0 || oldGroup.Generation != accepted.Generation || &accepted.Entries[0] != &oldGroup.Entries[0] {
		t.Fatal("missing genuinely accepted definition owner/generation/backing")
	}
	saved := enginetest.SnapshotFoldDescription(portal.Entry.Description)
	enginetest.PreparedFoldEntry(t, portal.Entry.Description, sqltype.Text, "(0.10000000000000002,1)")
	execute := func(name string) {
		command(func(scope *core.StatementScope) error {
			out, err := scope.ExecuteProtocolPortal(name, 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
			if err == nil && (out.Count != 1 || out.Suspended) {
				t.Fatal("accepted grouped producer did not execute completely", out)
			}
			return err
		})
	}
	bind("reused")
	reused := registry.Binding("reused")
	if reused.Resource.Root == oldGraph || reused.Entry.Description.Folds != owner || reused.Folds.Generation != accepted.Generation || &reused.Folds.Entries[0] != &accepted.Entries[0] || owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("unchanged Bind replaced accepted backing or shared its private graph")
	}
	execute("reused")
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("reused") })
	capture := func() (catalog.Read, core.PreparedEnvironment) {
		op, err := s.Acquire(t.Context())
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
		return auth.Catalog, environment
	}
	read, environment := capture()
	parent, _ := read.Relation(oldProof.Parent.Address)
	stamp := core.DefinitionStamp(parent, environment)
	parentCaptured := false
	for _, dependency := range analysis.Dependencies {
		parentCaptured = parentCaptured || dependency.Ref == oldProof.Parent && dependency.Revision == stamp
	}
	if !parentCaptured {
		t.Fatal("analysis did not capture the exact supporting parent stamp")
	}
	stale, err := core.BeginFoldPublication(&portal.Entry.Description, definition.Body, environment, read, nil)
	if err != nil || stale == nil {
		t.Fatal("missing actual candidate fold publication", err)
	}
	enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
	enginetest.SessionSQL(t, s, "ALTER TABLE gd_prepared DROP CONSTRAINT gd_prepared_pkey")
	read, environment = capture()
	withoutKey, _ := read.Relation(parent.Address())
	if core.DefinitionStamp(withoutKey, environment) == stamp || analysis.Valid(read, environment) {
		t.Fatal("primary-key removal did not invalidate real analysis")
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("rejected", "gd_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		if diagnostic, ok := err.(*sqlstate.Error); !ok || diagnostic.Code != "42803" {
			t.Fatal("failed rebind did not reject the missing key", err)
		}
		return nil
	})
	if registry.Binding("rejected") != nil || portal.Entry.Description.Analysis != analysis || owner.Analysis != analysis || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("failed rebind published a replacement analysis or resource")
	}
	rejected := owner.Current()
	if err := stale.Accept(t.Context(), read, environment, read.Epoch()); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("stale accepted authority was republished", err)
	}
	if owner.Current().Generation != rejected.Generation || &owner.Current().Entries[0] != &rejected.Entries[0] {
		t.Fatal("rejected stale publication changed the definition generation/backing")
	}
	enginetest.SessionSQL(t, s, "ALTER TABLE gd_prepared ADD PRIMARY KEY(id)")
	bind("after")
	fresh := registry.Binding("after")
	current, environment := capture()
	replacement, _ := current.Relation(parent.Address())
	if core.DefinitionStamp(replacement, environment) == core.DefinitionStamp(withoutKey, environment) {
		t.Fatal("primary-key replacement did not advance the real parent stamp")
	}
	freshProof := groupDependencyAggregates(fresh.Resource.Root)[0].Dependencies[0]
	freshAnalysis, freshAccepted := fresh.Entry.Description.Analysis, owner.Current()
	if fresh.Resource.Root == oldGraph || freshAnalysis == analysis || owner.Analysis != freshAnalysis || !freshAnalysis.Valid(current, environment) || definition.Description.Folds != owner || fresh.Entry.Description.Folds != owner || fresh.Folds.Generation != freshAccepted.Generation || freshAccepted.Generation <= rejected.Generation || len(freshAccepted.Entries) == 0 || &fresh.Folds.Entries[0] != &freshAccepted.Entries[0] || &freshAccepted.Entries[0] == &accepted.Entries[0] || freshProof.Constraint == oldProof.Constraint || freshProof.Parent.Address != oldProof.Parent.Address {
		t.Fatal("replacement failed to acquire new actual constraint authority")
	}
	enginetest.PreparedFoldEntry(t, fresh.Entry.Description, sqltype.Text, "(0.1,1)")
	enginetest.PreparedFoldEntry(t, saved, sqltype.Text, "(0.10000000000000002,1)")
	execute("after")
	if portal.Resource.Root != oldGraph || portal.Folds != oldAccepted || oldAccepted.Generation != oldGroup.Generation || !reflect.DeepEqual(accepted.Entries, entries) || !reflect.DeepEqual(analysis.Dependencies, dependencies) || &oldAccepted.Entries[0] != &oldGroup.Entries[0] || oldAggregate.Dependent[0].(plan.ColumnExpr).Slot != oldPayload.(plan.ColumnExpr).Slot || oldAggregate.Dependencies[0].Constraint != oldProof.Constraint {
		t.Fatal("rebind/rejection mutated genuine old graph, stamps, or immutable fold backing")
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("before") })
	if !portal.Retired || portal.Folds != nil || oldAccepted.Generation != oldGroup.Generation || &oldAccepted.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(oldAccepted.Entries, entries) || oldAggregate.Dependencies[0].Constraint != oldProof.Constraint {
		t.Fatal("terminal live-resource cleanup mutated the saved accepted artifacts")
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("after") })
}

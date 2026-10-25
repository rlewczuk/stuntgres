package engine

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func ruleUnitCatalog(t *testing.T) (*Engine, AuthorizationView, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := New()
	base := e.CatalogRead()
	typ, ok := base.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("INT4 unavailable")
	}
	makeRelation := func(id ids.OID, name string) *catalog.Relation {
		return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: name, ObjectRevision: 1, Columns: []catalog.Column{{Name: "id", Usage: typ.Usage()}, {Name: "v", Usage: typ.Usage()}}, Index: map[string]int{"id": 0, "v": 1}, Attributes: []int32{1, 2}, ColumnMeta: catalog.ColumnMetadata{HighWater: 2}, RelationKind: catalog.TableRelation}
	}
	src, sink := makeRelation(900001, "src"), makeRelation(900002, "sink")
	next := catalog.CloneState(e.Catalog.State)
	next.SetDescriptor(src.Address(), src)
	next.SetDescriptor(sink.Address(), sink)
	e.Catalog.PublishLocked(next)
	return e, e.AuthorityView(access.View{Owner: &e.Catalog.Owner, Catalog: e.Catalog.Read(next)}), src, sink
}

func ruleUnitRef(e *Engine, d *catalog.Relation) ids.ObjectRef {
	return e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
}
func ruleUnitInt(d *catalog.Relation, n int64) expr.Expr {
	value, err := sqltype.Integer(d.Columns[0].Usage.Type, n)
	if err != nil {
		panic(err)
	}
	return plan.LiteralExpr{Info: expr.Info{Usage: d.Columns[0].Usage, Pos: parser.Position{Offset: -1}}, Value: value}
}
func ruleUnitInsert(e *Engine, d *catalog.Relation, id, v int64) stored.RuleAction {
	binding := analyze.Binding{Relation: d, Ref: ruleUnitRef(e, d), Targets: []int{0, 1}, Expressions: [][]expr.Expr{{ruleUnitInt(d, id), ruleUnitInt(d, v)}}}
	presentation := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(d.Columns), Child: &plan.SingletonNode{}, Expressions: binding.Expressions[0]}}})
	return stored.NewRuleAction(stored.RuleActionParts{Syntax: &parser.Insert{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}}, Binding: binding, Presentation: presentation})
}

func TestRuleTemplateSubstitution(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	original := ruleUnitInsert(e, src, 1, 10).Binding()
	original.Expressions = append(original.Expressions, []expr.Expr{ruleUnitInt(src, 2), ruleUnitInt(src, 20)})
	ref := ruleUnitRef(e, src)
	ref.Address.SubID = 1
	pseudo := plan.ColumnExpr{Info: view.RuleColumnInfo(src.Columns[0]), Origin: ref, Scope: analyze.RulePseudoScope, Frame: 2, Slot: 0}
	action := ruleUnitInsert(e, sink, 0, 0)
	action.Binding().Expressions[0][0] = pseudo
	composed, err := view.ComposeRuleAction(auth, original, "INSERT", stored.Rule{}, action)
	if err != nil {
		t.Fatal(err)
	}
	project := composed.Query.Root.(*plan.ProjectNode)
	column, ok := project.Expressions[0].(plan.ColumnExpr)
	if !ok || column.Scope == analyze.RulePseudoScope || column.Slot != 0 {
		t.Fatal("NEW was not substituted into typed producer")
	}
	join, ok := project.Child.(*plan.JoinNode)
	if !ok || len(join.Left.(*plan.ValuesNode).Rows) != 2 {
		t.Fatal("lost VALUES producer multiplicity")
	}
	if action.Binding().Expressions[0][0].(plan.ColumnExpr).Scope != analyze.RulePseudoScope {
		t.Fatal("mutated stored template")
	}
	condition := stored.NewRule(stored.RuleParts{Event: "INSERT", Instead: true, Condition: pseudo})
	condition = condition.With(func(p *stored.RuleParts) {
		p.Condition = plan.LiteralExpr{Info: expr.Info{Usage: func() sqltype.Usage { typ, _ := auth.Catalog.LookupType(sqltype.Bool); return typ.Usage() }()}, Value: func() sqltype.Value { typ, _ := auth.Catalog.LookupType(sqltype.Bool); return sqltype.Null(typ) }()}
	})
	residual, err := view.RuleResidual(auth, original, "INSERT", []stored.Rule{condition})
	if err != nil {
		t.Fatal(err)
	}
	predicate := residual.Query.Root.(*plan.ProjectNode).Child.(*plan.FilterNode).Predicate
	value, err := predicate.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil {
		t.Fatal(err)
	}
	truth, err := plan.TruthOf(value)
	if err != nil || truth != plan.True {
		t.Fatalf("NULL inverse=%v error=%v want TRUE", truth, err)
	}
	t.Run("computed_order_recipe", func(t *testing.T) {
		pseudoV := pseudo
		pseudoV.Origin.Address.SubID = 2
		pseudoV.Slot = 1
		provider, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, src.Columns[1].Usage.Type, src.Columns[1].Usage.Type, src.Columns[1].Usage.Type)
		if err != nil {
			t.Fatal(err)
		}
		output := plan.ArithmeticExpr{Info: view.RuleColumnInfo(src.Columns[1]), Left: pseudoV, Right: ruleUnitInt(src, 10), Provider: provider}
		projection := []expr.Expr{pseudo, output}
		order, err := (*analyze.Binder)(nil).FinishOrder(&plan.SingletonNode{}, []plan.SortKey{{Expression: output, Slot: -1, Cache: -1, TargetIdentity: 2}}, projection)
		if err != nil {
			t.Fatal(err)
		}
		template := ruleUnitInsert(e, sink, 0, 0)
		template = template.With(func(p *stored.RuleActionParts) {
			p.Binding.Expressions = nil
			p.Binding.CommandQuery = true
			p.Binding.Query = &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(sink.Columns), Child: order, Expressions: projection, Frame: 1}}
		})
		rewritten, err := view.ComposeRuleAction(auth, original, "INSERT", stored.Rule{}, template)
		if err != nil {
			t.Fatal(err)
		}
		result := rewritten.Query.Root.(*plan.ProjectNode)
		sorted, ok := result.Child.(*plan.SortNode)
		if !ok || sorted.Keys[0].Slot != -1 || sorted.Keys[0].Cache != 0 || sorted.CacheWidth != 1 {
			t.Fatalf("computed ordering schedule: %#v", result.Child)
		}
		cached, ok := result.Expressions[1].(plan.CachedOrderExpr)
		if !ok || cached.Slot != 0 {
			t.Fatal("projection lost the canonical ORDER cache")
		}
		if order.(*plan.SortNode).Keys[0].Cache != 0 || template.Binding().Query.Root.(*plan.ProjectNode).Child != order {
			t.Fatal("template schedule mutated")
		}
	})
	t.Run("select_producer_ownership", func(t *testing.T) {
		binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		random, err := binder.Bind(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "random"}}}}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(err)
		}
		schema := slices.Clone(src.Columns)
		schema[1].Usage = random.ResultUsage()
		source := &plan.ProjectNode{QuerySchema: plan.QuerySchema(schema), Child: &plan.SingletonNode{}, Expressions: []expr.Expr{ruleUnitInt(src, 1), random}, Frame: 2}
		selectBinding := original
		selectBinding.Query, selectBinding.Expressions = &plan.Query{Root: source}, nil
		selectBinding.Insert.SubquerySource = true
		producer, err := view.MakeRuleProducer(auth.Catalog, selectBinding, "INSERT", []int{0})
		if err != nil {
			t.Fatal(err)
		}
		derived, ok := producer.Root.(*plan.DerivedNode)
		if !ok || derived.Query.Root != source {
			t.Fatal("volatile projected output was pruned or producer project lifted")
		}
		supplied := producer.New[1].(plan.ColumnExpr)
		if supplied.Scope != 1 || supplied.Frame != 2 || supplied.Slot != 1 {
			t.Fatal("NEW lost derived output identity")
		}
		pure := *source
		pure.Expressions = []expr.Expr{source.Expressions[0], ruleUnitInt(src, 99)}
		pure.QuerySchema = plan.QuerySchema(src.Columns)
		selectBinding.Query = &plan.Query{Root: &pure}
		producer, err = view.MakeRuleProducer(auth.Catalog, selectBinding, "INSERT", []int{0})
		if err != nil {
			t.Fatal(err)
		}
		pruned := producer.Root.(*plan.DerivedNode).Query.Root.(*plan.ProjectNode)
		if pruned == &pure || !pruned.Expressions[1].(plan.LiteralExpr).Value.IsNull() || pure.Expressions[1].(plan.LiteralExpr).Value.IsNull() {
			t.Fatal("pure unused projection demand or template immutability lost")
		}
		template := ruleUnitInsert(e, sink, 0, 0)
		template.Binding().Expressions[0][0] = pseudo
		rewritten, err := view.ComposeRuleAction(auth, selectBinding, "INSERT", stored.Rule{}, template)
		if err != nil {
			t.Fatal(err)
		}
		outer := rewritten.Query.Root.(*plan.ProjectNode)
		inner := outer.Child.(*plan.JoinNode).Left.(*plan.DerivedNode).Query.Root.(*plan.ProjectNode)
		output := outer.Expressions[0].(plan.ColumnExpr)
		if inner.Frame == outer.Frame || output.Frame != outer.Frame || output.Slot != 0 || pure.Frame != 2 {
			t.Fatal("producer lexical frame leaked into action frame or template changed")
		}
		valuesBinding := selectBinding
		valuesBinding.Insert.SubquerySource = false
		producer, err = view.MakeRuleProducer(auth.Catalog, valuesBinding, "INSERT")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := producer.Root.(*plan.DerivedNode); ok {
			t.Fatal("VALUES provenance became SELECT materialization")
		}
	})
	t.Run("pseudo_metadata_namespace", func(t *testing.T) {
		binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		binder.Query.Auth = &auth
		binder.Target.RuleNamespace = &analyze.RuleNamespace{Relation: src, Ref: ruleUnitRef(e, src), Event: "INSERT"}
		binder.Query.Frame, binder.Query.NextQueryFrame = 1, 1
		statements, err := parser.Parse("SELECT NEW.id,NEW.v")
		if err != nil {
			t.Fatal(err)
		}
		query, err := binder.BindSelect(statements[0].(*parser.Select), true)
		if err != nil {
			t.Fatal(err)
		}
		project := query.Root.(*plan.ProjectNode)
		if len(project.Schema()) != 2 || project.Schema()[0].Usage != src.Columns[0].Usage || project.Schema()[1].Usage != src.Columns[1].Usage {
			t.Fatal("pseudo frame borrowed unrelated FROM metadata")
		}
		for _, expression := range project.Expressions {
			if expression.(plan.ColumnExpr).Scope != analyze.RulePseudoScope {
				t.Fatal("pseudo scope identity erased during analysis")
			}
		}
		row, matched, err := binder.RuleColumn(parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "new"}}}})
		if err != nil || !matched {
			t.Fatalf("typed NEW whole row: matched=%v error=%v", matched, err)
		}
		if len(row.(plan.RowExpr).Fields) != len(src.Columns) {
			t.Fatal("NEW whole row lost stable members")
		}
	})
	t.Run("domain_truth_barrier", func(t *testing.T) {
		leaf := plan.DomainValueExpr{Info: expr.Info{Usage: src.Columns[0].Usage, Property: expr.Properties{DomainDependent: true, RuntimeDomainConversion: true}}}
		properties := stored.RewrittenIntrinsicProperties(leaf)
		if !properties.DomainDependent || !properties.RuntimeDomainConversion {
			t.Fatal("rewrite erased runtime domain authority")
		}
		properties.ParameterDependent = true
		mapped, _, err := stored.MapExpression(auth.Catalog, leaf, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
			copy := expr.(plan.DomainValueExpr)
			copy.Property = properties
			return copy, true, nil
		})
		if err != nil || mapped.Properties() != properties || leaf.Properties().ParameterDependent {
			t.Fatal("domain leaf property copy lost authority or mutated template", err)
		}
	})
	t.Run("generic_function_properties", func(t *testing.T) {
		binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		binder.Target.RuleNamespace = &analyze.RuleNamespace{Relation: src, Ref: ruleUnitRef(e, src), Event: "INSERT"}
		statements, err := parser.Parse("SELECT abs(NEW.v),random(1,2),format('%s',NEW.v)")
		if err != nil {
			t.Fatal(err)
		}
		bind := func(index int) expr.Expr {
			node, err := binder.Bind(statements[0].(*parser.Select).Targets[index], analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			return node
		}
		absolute, draw, format := bind(0), bind(1), bind(2)
		if draw.Properties().Volatility != expr.Volatile {
			t.Fatal("random provider lost intrinsic volatility")
		}
		replaceFormal := func(expr expr.Expr) (expr.Expr, bool, error) {
			if column, ok := expr.(plan.ColumnExpr); ok && column.Scope == analyze.RulePseudoScope {
				return draw, true, nil
			}
			return expr, false, nil
		}
		identityType := func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }
		for _, parent := range []expr.Expr{absolute, format} {
			mapped, _, err := stored.MapExpression(auth.Catalog, parent, identityType, nil, nil, replaceFormal)
			if err != nil || mapped.Properties().Volatility != expr.Volatile || mapped.Properties().RowDependent {
				t.Fatalf("generic parent lost fresh child demand: %v %v", mapped, err)
			}
			if !parent.Properties().RowDependent {
				t.Fatal("mutated original formal-dependent function")
			}
			if _, formatting := mapped.(functions.Format); formatting {
				stable, _, err := stored.MapExpression(auth.Catalog, mapped, identityType, nil, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
					if _, random := expr.(functions.Random); random {
						return ruleUnitInt(src, 11), true, nil
					}
					return expr, false, nil
				})
				if err != nil || stable.Properties().Volatility != expr.Stable {
					t.Fatalf("generic format lost intrinsic STABLE authority: %v %v", stable, err)
				}
			}
		}
	})
	t.Run("single_values_macro", func(t *testing.T) {
		single := ruleUnitInsert(e, src, 1, 10).Binding()
		producer, err := view.MakeRuleProducer(auth.Catalog, single, "INSERT")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := producer.Root.(*plan.SingletonNode); !ok {
			t.Fatal("single VALUES became a materialized supplied row")
		}
		for i, expression := range single.Expressions[0] {
			if !analyze.SameGroupExpression(producer.New[i], expression) {
				t.Fatal("single VALUES recipe replaced by an output Var")
			}
		}
	})
	t.Run("event_source_identity", func(t *testing.T) {
		f := viewWriteMechanics(t)
		original := analyze.Binding{Relation: f.base, Ref: f.baseRef, Storage: f.e.Storage.Relations[f.base.Address()], Targets: []int{0}, UsedColumns: []int{0}, Update: &analyze.UpdateBinding{Assignments: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: f.integer.Usage()}, Value: sqltype.Null(f.integer)}}}}
		producer, err := view.MakeRuleProducer(f.e.CatalogRead(), original, "UPDATE")
		if err != nil {
			t.Fatal(err)
		}
		if len(producer.Sources) != 1 || producer.Sources[0].Storage != original.Storage {
			t.Fatal("event producer lost original physical storage capture")
		}
		current, err := CurrentQuerySourceLocked(f.e, f.e.CatalogRead(), nil, producer.Sources[0])
		if err != nil || current != f.base {
			t.Fatal("event source fails its genuine identity/storage fence", err)
		}
		op, done := roleOperation(t, f.e, "postgres")
		defer done()
		authority := f.authorization(t, op)
		imported := producer.Sources[0]
		imported.Storage = nil
		refreshed, err := view.RefreshRuleSources(authority, analyze.Binding{QuerySources: []exec.Leaf{imported}})
		if err != nil || refreshed.QuerySources[0].Storage != original.Storage || imported.Storage != nil {
			t.Fatal("stored/imported source did not capture current physical storage immutably", err)
		}
	})
}

func TestRuleProgramOrdering(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	src.Rules = stored.RulesPayload([]stored.Rule{stored.NewRule(stored.RuleParts{OID: 900101, Name: "a", Event: "INSERT", Actions: []stored.RuleAction{ruleUnitInsert(e, sink, 1, 10), ruleUnitInsert(e, sink, 2, 20)}}), stored.NewRule(stored.RuleParts{OID: 900102, Name: "z", Event: "INSERT", Actions: []stored.RuleAction{ruleUnitInsert(e, sink, 3, 30)}})})
	original := ruleUnitInsert(e, src, 4, 40)
	p := view.RuleProgram{Count: -1, Returning: -1}
	e.Mu.Lock()
	count, err := view.RuleRewriter{Owner: &e.Catalog.Owner}.ExpandLocked(auth, original.Syntax(), original.Binding(), nil, &p)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 || len(p.Commands) != 4 {
		t.Fatalf("authority=%d commands=%d want0/4", count, len(p.Commands))
	}
	for i, want := range []int64{1, 2, 3} {
		project := p.Commands[i+1].Binding.Query.Root.(*plan.ProjectNode)
		literal, ok := project.Expressions[0].(plan.LiteralExpr)
		if !ok || !sqltype.SameDatum(literal.Value, ruleUnitInt(src, want).(plan.LiteralExpr).Value) {
			t.Fatalf("action order %d differs from %d", i, want)
		}
	}
}

func TestRuleProgramAuthority(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	src.Rules = stored.RulesPayload([]stored.Rule{stored.NewRule(stored.RuleParts{OID: 900101, Name: "a", Event: "INSERT", Instead: true, Actions: []stored.RuleAction{ruleUnitInsert(e, sink, 1, 10)}}), stored.NewRule(stored.RuleParts{OID: 900102, Name: "z", Event: "INSERT", Instead: true, Actions: []stored.RuleAction{ruleUnitInsert(e, sink, 2, 20)}})})
	original := ruleUnitInsert(e, src, 9, 90)
	p := view.RuleProgram{Count: -1, Returning: -1}
	e.Mu.Lock()
	count, err := view.RuleRewriter{Owner: &e.Catalog.Owner}.ExpandLocked(auth, original.Syntax(), original.Binding(), nil, &p)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(p.Commands) != 2 {
		t.Fatalf("last same-event authority=%d commands=%d want1/2", count, len(p.Commands))
	}
	src.Rules = stored.RulesPayload([]stored.Rule{stored.NewRule(stored.RuleParts{OID: 900101, Name: "nothing", Event: "INSERT", Instead: true})})
	p = view.RuleProgram{Count: -1, Returning: -1}
	e.Mu.Lock()
	count, err = view.RuleRewriter{Owner: &e.Catalog.Owner}.ExpandLocked(auth, original.Syntax(), original.Binding(), nil, &p)
	e.Mu.Unlock()
	if err != nil || count != -1 || len(p.Commands) != 0 {
		t.Fatalf("NOTHING authority=%d commands=%d error=%v", count, len(p.Commands), err)
	}
	if len(p.Events) != 1 || p.Events[0].Ref != ruleUnitRef(e, src) || p.Events[0].Revision != src.Revision() {
		t.Fatal("NOTHING lost rule event locking/prepared dependency authority")
	}
	t.Run("layered_boundary", func(t *testing.T) {
		f := viewWriteMechanics(t)
		op, done := roleOperation(t, f.e, "postgres")
		defer done()
		auth := f.authorization(t, op)
		f.base.RelationKind, f.base.VirtualView = catalog.ViewRelation, true
		f.base.Rules = stored.RulesPayload([]stored.Rule{stored.NewRule(stored.RuleParts{OID: 900201, Name: "inner_stop", Event: "UPDATE", Instead: true})})
		f.view.View = stored.ViewOf(f.view).With(func(p *stored.ViewParts) { p.CheckOption = stored.CheckCascaded })
		project := stored.ViewOf(f.view).Query().Root.(*plan.ProjectNode)
		predicate := plan.LiteralExpr{Info: expr.Info{Usage: f.boolean.Usage()}, Value: sqltype.Boolean(f.boolean, true)}
		project.Child = &plan.FilterNode{QuerySchema: plan.QuerySchema(f.base.Columns), Child: project.Child, Predicate: predicate}
		original := analyze.Binding{Relation: f.view, Ref: f.viewRef, Targets: []int{0}, Update: &analyze.UpdateBinding{Assignments: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: f.integer.Usage()}, Value: sqltype.Null(f.integer)}}}}
		mapped := original
		if err := view.BindViewWrite(&f.e.Catalog.Owner, auth.Catalog, &mapped, &parser.Update{}); err != nil {
			t.Fatal(err)
		}
		if mapped.Ref != f.viewRef || mapped.Relation != f.view || view.PhysicalBinding(mapped).Ref != f.baseRef || mapped.Targets[0] != 1 {
			t.Fatal("layered outer authority or inner event mapping lost")
		}
		if len(view.ViewWriteOf(mapped.ViewWrite).Checks) != 0 || len(view.ViewWriteOf(mapped.ViewWrite).Predicates) == 0 {
			t.Fatal("rewrite boundary conflated CHECK enforcement with selection")
		}
		if original.ViewWrite != nil || original.Targets[0] != 0 || original.Ref != f.viewRef {
			t.Fatal("original binding mutated")
		}
		program := view.RuleProgram{Count: -1, Returning: -1}
		f.e.Mu.Lock()
		count, err := view.RuleRewriter{Owner: &f.e.Catalog.Owner}.ExpandLocked(auth, &parser.Update{}, mapped, nil, &program)
		f.e.Mu.Unlock()
		if err != nil || count != -1 || len(program.Commands) != 0 {
			t.Fatalf("inner unconditional boundary must stop before automatic validation: count=%d commands=%d error=%v", count, len(program.Commands), err)
		}
	})
}

func TestRuleProgramRecursion(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	src.Rules = stored.RulesPayload([]stored.Rule{stored.NewRule(stored.RuleParts{OID: 900101, Name: "cycle", Event: "INSERT", Actions: []stored.RuleAction{ruleUnitInsert(e, sink, 1, 10)}})})
	sink.Rules = stored.RulesPayload([]stored.Rule{stored.NewRule(stored.RuleParts{OID: 900102, Name: "cycle", Event: "INSERT", Actions: []stored.RuleAction{ruleUnitInsert(e, src, 1, 10)}})})
	original := ruleUnitInsert(e, src, 9, 90)
	p := view.RuleProgram{}
	e.Mu.Lock()
	_, err := view.RuleRewriter{Owner: &e.Catalog.Owner}.ExpandLocked(auth, original.Syntax(), original.Binding(), nil, &p)
	e.Mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "infinite recursion detected in rules for relation") {
		t.Fatalf("recursion error=%v", err)
	}
	sink.Rules = nil
	stored.RulesOf(src)[0] = stored.RulesOf(src)[0].With(func(p *stored.RuleParts) { p.Actions = append(p.Actions, ruleUnitInsert(e, sink, 2, 20)) })
	p = view.RuleProgram{}
	e.Mu.Lock()
	_, err = view.RuleRewriter{Owner: &e.Catalog.Owner}.ExpandLocked(auth, original.Syntax(), original.Binding(), nil, &p)
	e.Mu.Unlock()
	if err != nil || len(p.Commands) != 3 {
		t.Fatalf("sibling target reuse: commands=%d error=%v", len(p.Commands), err)
	}
}

func TestRuleProgramAtomicStage(t *testing.T) {
	e, _, src, sink := ruleUnitCatalog(t)
	start := time.Unix(10, 0)
	expected := &StatementDescription{}
	s := &StatementScope{Op: &session.Operation{}, statementStart: start, Expected: expected}
	s.LockedRef, s.LockedDescriptor = ruleUnitRef(e, src), src
	s.self = s
	s.Stage.self = &s.Stage
	s.Stage.scope = s
	s.Stage.Rows = map[ids.ObjectAddress][]storage.RowVersion{src.Address(): {{Row: 1, Version: 1}}}
	before := s.RuleScratch()
	stageRows := s.Stage.Rows
	s.InstallRuleScratch(RuleCommandScratch{Ref: ruleUnitRef(e, sink), Descriptor: sink})
	if s.Stage.self != &s.Stage || s.Stage.scope != s || len(s.Stage.Rows[src.Address()]) != 1 || s.Expected != expected || s.statementStart != start {
		t.Fatal("action scratch changed original envelope/stage")
	}
	s.InstallRuleScratch(before)
	if s.LockedRef.Address != src.Address() || !maps.EqualFunc(stageRows, s.Stage.Rows, func(a, b []storage.RowVersion) bool { return len(a) == len(b) && a[0].Row == b[0].Row }) {
		t.Fatal("scratch restoration lost original stage")
	}
}

func TestRuleDefinitionDependencies(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	t.Run("unused_owned_modifier", func(t *testing.T) {
		outer := ruleUnitInsert(e, src, 1, 10)
		auxiliary := ruleUnitInsert(e, sink, 7, 70)
		auxiliaryBinding := auxiliary.Binding()
		outer = outer.With(func(p *stored.RuleActionParts) {
			p.Binding.CTEs = []*plan.CTEDefinition{{Statement: auxiliary.Syntax(), Mutation: &auxiliaryBinding, Owner: 1}}
		})
		refs, err := view.RuleReferences(context.Background(), auth.Catalog, stored.NewRule(stored.RuleParts{Actions: []stored.RuleAction{outer}}))
		if err != nil {
			t.Fatal(err)
		}
		for _, attribute := range sink.Attributes {
			address := sink.Address()
			address.SubID = attribute
			if !slices.Contains(refs, address) {
				t.Fatalf("missing unused modifier target dependency %v", address)
			}
		}
	})
	ref := ruleUnitRef(e, src)
	ref.Address.SubID = 1
	condition := plan.ColumnExpr{Info: view.RuleColumnInfo(src.Columns[0]), Origin: ref, Scope: analyze.RulePseudoScope, Frame: 2}
	r := stored.NewRule(stored.RuleParts{OID: 900101, Name: "r", Event: "INSERT", Condition: condition, Actions: []stored.RuleAction{ruleUnitInsert(e, sink, 1, 10)}})
	refs, err := view.RuleReferences(context.Background(), auth.Catalog, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []ids.ObjectAddress{ref.Address, {ClassID: ids.RelationClassID, ObjectID: sink.Address().ObjectID, SubID: 1}, {ClassID: ids.RelationClassID, ObjectID: sink.Address().ObjectID, SubID: 2}} {
		if !slices.Contains(refs, want) {
			t.Fatalf("missing dependency %v in %v", want, refs)
		}
	}
	r = r.With(func(p *stored.RuleParts) { p.References = refs })
	src.Rules = stored.RulesPayload([]stored.Rule{r})
	edges, err := pgcatalog.CollectDepend(context.Background(), auth.Catalog, EngineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	owner := ids.ObjectAddress{ClassID: 2618, ObjectID: r.OID}
	if !slices.Contains(edges, pgcatalog.DependEdge{owner, src.Address(), 'a'}) {
		t.Fatal("rule lacks AUTO event edge")
	}
	t.Run("mixed_drop_closure", func(t *testing.T) {
		stage := e.BeginCatalogStage(auth.Catalog)
		defer e.DiscardCatalogStage(stage)
		dependents, err := DomainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{sink.Address()})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, dependent := range dependents {
			if dependent.Address == owner && dependent.Relation == src && dependent.Target == sink.Address() && !dependent.Automatic {
				found = true
			}
		}
		if !found {
			t.Fatal("normal rule target absent from central mixed DROP closure")
		}
	})
	reserved := make(map[ids.OID]bool)
	catalog.ReserveDefinitionOIDs(reserved, src)
	if !reserved[r.OID] || !catalog.DefinitionOIDUsed(src, r.OID) {
		t.Fatal("rule OID omitted from identity space")
	}
}

func TestRuleDefinitionRemap(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	oldRef := ruleUnitRef(e, src)
	oldRef.Address.SubID = 1
	src.Rules = stored.RulesPayload([]stored.Rule{stored.NewRule(stored.RuleParts{OID: 900101, Name: "r", Event: "INSERT", Condition: plan.ColumnExpr{Info: view.RuleColumnInfo(src.Columns[0]), Origin: oldRef, Scope: analyze.RulePseudoScope, Frame: 2}, References: []ids.ObjectAddress{oldRef.Address}, Actions: []stored.RuleAction{ruleUnitInsert(e, sink, 1, 10)}})})
	newSrc, newSink := *src, *sink
	newSrc.ObjectAddress.ObjectID += 10
	newSink.ObjectAddress.ObjectID += 10
	next := catalog.CloneState(auth.Catalog.State())
	next.SetDescriptor(newSrc.Address(), &newSrc)
	next.SetDescriptor(newSink.Address(), &newSink)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{src.Address(): newSrc.Address(), sink.Address(): newSink.Address()}
	ref := func(old ids.ObjectRef) ids.ObjectRef {
		whole := old.Address
		whole.SubID = 0
		if a, ok := mapping[whole]; ok {
			a.SubID = old.Address.SubID
			old.Address = a
		}
		old = e.Catalog.Domain.Ref(old.Epoch, old.Address)
		old.Epoch = next.Epoch()
		return old
	}
	mapped, err := stored.RemapRules(e.Catalog.Read(next), src, mapping, ref, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RulesOf(mapped)[0].Condition().(plan.ColumnExpr).Origin.Address.ObjectID != newSrc.Address().ObjectID || stored.RulesOf(mapped)[0].Actions()[0].Binding().Ref.Address != newSink.Address() {
		t.Fatal("nested rule references not remapped")
	}
	if stored.RulesOf(src)[0].Condition().(plan.ColumnExpr).Origin != oldRef || stored.RulesOf(src)[0].Actions()[0].Binding().Ref.Address != sink.Address() {
		t.Fatal("remapping mutated original template")
	}
}

func TestRuleViewReturnReplacement(t *testing.T) {
	_, _, src, _ := ruleUnitCatalog(t)
	if err := view.RuleResultCheck(src, slices.Clone(src.Columns), true); err != nil {
		t.Fatal(err)
	}
	if err := view.RuleResultCheck(src, src.Columns[:1], true); err == nil || !strings.Contains(err.Error(), "SELECT rule's target list has too few entries") {
		t.Fatalf("arity error=%v", err)
	}
	bad := slices.Clone(src.Columns)
	bad[0].Name = "other"
	if err := view.RuleResultCheck(src, bad, true); err == nil || !strings.Contains(err.Error(), "different column name") {
		t.Fatalf("name error=%v", err)
	}
	if err := view.RuleResultCheck(src, bad, false); err != nil {
		t.Fatal("RETURNING incorrectly requires event column names:", err)
	}
}

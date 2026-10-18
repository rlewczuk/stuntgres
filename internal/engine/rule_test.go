package engine

import (
	"context"
	"maps"
	"slices"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func ruleUnitCatalog(t *testing.T) (*Engine, AuthorizationView, *relationDescriptor, *relationDescriptor) {
	t.Helper()
	e := New()
	base := e.CatalogRead()
	typ, ok := base.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("INT4 unavailable")
	}
	makeRelation := func(id ids.OID, name string) *relationDescriptor {
		return &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: name, ObjectRevision: 1, Columns: []ColumnDescriptor{{Name: "id", Usage: typ.Usage()}, {Name: "v", Usage: typ.Usage()}}, Index: map[string]int{"id": 0, "v": 1}, Attributes: []int32{1, 2}, AttributeHighWater: 2, RelationKind: tableRelation}
	}
	src, sink := makeRelation(900001, "src"), makeRelation(900002, "sink")
	next := cloneCatalog(e.catalog.state)
	next.setDescriptor(src.Address(), src)
	next.setDescriptor(sink.Address(), sink)
	e.catalog.state = next
	return e, AuthorizationView{engine: e, catalog: e.catalog.read(next)}, src, sink
}

func ruleUnitRef(e *Engine, d *relationDescriptor) ids.ObjectRef {
	return e.catalog.Domain.Ref(e.catalog.state.Epoch(), d.Address())
}
func ruleUnitInt(d *relationDescriptor, n int64) expr.Expr {
	value, err := sqltype.Integer(d.Columns[0].Usage.Type, n)
	if err != nil {
		panic(err)
	}
	return literalExpression{Info: expr.Info{Usage: d.Columns[0].Usage, Pos: parser.Position{Offset: -1}}, value: value}
}
func ruleUnitInsert(e *Engine, d *relationDescriptor, id, v int64) ruleAction {
	return ruleAction{syntax: &parser.Insert{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}}, binding: binding{relation: d, ref: ruleUnitRef(e, d), targets: []int{0, 1}, expressions: [][]expr.Expr{{ruleUnitInt(d, id), ruleUnitInt(d, v)}}}}
}

func TestRuleTemplateSubstitution(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	original := ruleUnitInsert(e, src, 1, 10).binding
	original.expressions = append(original.expressions, []expr.Expr{ruleUnitInt(src, 2), ruleUnitInt(src, 20)})
	ref := ruleUnitRef(e, src)
	ref.Address.SubID = 1
	pseudo := columnExpression{Info: ruleColumnInfo(src.Columns[0]), origin: ref, scope: rulePseudoScope, frame: 2, slot: 0}
	action := ruleUnitInsert(e, sink, 0, 0)
	action.binding.expressions[0][0] = pseudo
	composed, err := composeRuleAction(auth, original, "INSERT", ruleDefinition{}, action)
	if err != nil {
		t.Fatal(err)
	}
	project := composed.query.Root.(*projectNode)
	column, ok := project.expressions[0].(columnExpression)
	if !ok || column.scope == rulePseudoScope || column.slot != 0 {
		t.Fatal("NEW was not substituted into typed producer")
	}
	join, ok := project.child.(*joinNode)
	if !ok || len(join.left.(*valuesNode).rows) != 2 {
		t.Fatal("lost VALUES producer multiplicity")
	}
	if action.binding.expressions[0][0].(columnExpression).scope != rulePseudoScope {
		t.Fatal("mutated stored template")
	}
	condition := ruleDefinition{event: "INSERT", instead: true, condition: pseudo}
	condition.condition = literalExpression{Info: expr.Info{Usage: func() sqltype.Usage { typ, _ := auth.catalog.LookupType(sqltype.Bool); return typ.Usage() }()}, value: func() sqltype.Value { typ, _ := auth.catalog.LookupType(sqltype.Bool); return sqltype.Null(typ) }()}
	residual, err := ruleResidual(auth, original, "INSERT", []ruleDefinition{condition})
	if err != nil {
		t.Fatal(err)
	}
	predicate := residual.query.Root.(*projectNode).child.(*filterNode).predicate
	value, err := predicate.Eval(&EvalContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	truth, err := TruthOf(value)
	if err != nil || truth != True {
		t.Fatalf("NULL inverse=%v error=%v want TRUE", truth, err)
	}
	t.Run("computed_order_recipe", func(t *testing.T) {
		pseudoV := pseudo
		pseudoV.origin.Address.SubID = 2
		pseudoV.slot = 1
		provider, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, src.Columns[1].Usage.Type, src.Columns[1].Usage.Type, src.Columns[1].Usage.Type)
		if err != nil {
			t.Fatal(err)
		}
		output := arithmeticExpression{Info: ruleColumnInfo(src.Columns[1]), left: pseudoV, right: ruleUnitInt(src, 10), provider: provider}
		projection := []expr.Expr{pseudo, output}
		order, err := (*expressionBinder)(nil).finishOrder(&singletonNode{}, []sortKey{{expression: output, slot: -1, cache: -1, targetIdentity: 2}}, projection)
		if err != nil {
			t.Fatal(err)
		}
		template := ruleUnitInsert(e, sink, 0, 0)
		template.binding.expressions = nil
		template.binding.commandQuery = true
		template.binding.query = &BoundQuery{Root: &projectNode{querySchema: querySchema(sink.Columns), child: order, expressions: projection, frame: 1}}
		rewritten, err := composeRuleAction(auth, original, "INSERT", ruleDefinition{}, template)
		if err != nil {
			t.Fatal(err)
		}
		result := rewritten.query.Root.(*projectNode)
		sorted, ok := result.child.(*sortNode)
		if !ok || sorted.keys[0].slot != -1 || sorted.keys[0].cache != 0 || sorted.cacheWidth != 1 {
			t.Fatalf("computed ordering schedule: %#v", result.child)
		}
		cached, ok := result.expressions[1].(cachedOrderExpression)
		if !ok || cached.slot != 0 {
			t.Fatal("projection lost the canonical ORDER cache")
		}
		if order.(*sortNode).keys[0].cache != 0 || template.binding.query.Root.(*projectNode).child != order {
			t.Fatal("template schedule mutated")
		}
	})
	t.Run("select_producer_ownership", func(t *testing.T) {
		binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		random, err := binder.bind(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "random"}}}}, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		schema := slices.Clone(src.Columns)
		schema[1].Usage = random.ResultUsage()
		source := &projectNode{querySchema: querySchema(schema), child: &singletonNode{}, expressions: []expr.Expr{ruleUnitInt(src, 1), random}, frame: 2}
		selectBinding := original
		selectBinding.query, selectBinding.expressions = &BoundQuery{Root: source}, nil
		selectBinding.insertSubquerySource = true
		producer, err := makeRuleProducer(auth.catalog, selectBinding, "INSERT", []int{0})
		if err != nil {
			t.Fatal(err)
		}
		derived, ok := producer.root.(*derivedNode)
		if !ok || derived.query.Root != source {
			t.Fatal("volatile projected output was pruned or producer project lifted")
		}
		supplied := producer.new[1].(columnExpression)
		if supplied.scope != 1 || supplied.frame != 2 || supplied.slot != 1 {
			t.Fatal("NEW lost derived output identity")
		}
		pure := *source
		pure.expressions = []expr.Expr{source.expressions[0], ruleUnitInt(src, 99)}
		pure.querySchema = querySchema(src.Columns)
		selectBinding.query = &BoundQuery{Root: &pure}
		producer, err = makeRuleProducer(auth.catalog, selectBinding, "INSERT", []int{0})
		if err != nil {
			t.Fatal(err)
		}
		pruned := producer.root.(*derivedNode).query.Root.(*projectNode)
		if pruned == &pure || !pruned.expressions[1].(literalExpression).value.IsNull() || pure.expressions[1].(literalExpression).value.IsNull() {
			t.Fatal("pure unused projection demand or template immutability lost")
		}
		template := ruleUnitInsert(e, sink, 0, 0)
		template.binding.expressions[0][0] = pseudo
		rewritten, err := composeRuleAction(auth, selectBinding, "INSERT", ruleDefinition{}, template)
		if err != nil {
			t.Fatal(err)
		}
		outer := rewritten.query.Root.(*projectNode)
		inner := outer.child.(*joinNode).left.(*derivedNode).query.Root.(*projectNode)
		output := outer.expressions[0].(columnExpression)
		if inner.frame == outer.frame || output.frame != outer.frame || output.slot != 0 || pure.frame != 2 {
			t.Fatal("producer lexical frame leaked into action frame or template changed")
		}
		valuesBinding := selectBinding
		valuesBinding.insertSubquerySource = false
		producer, err = makeRuleProducer(auth.catalog, valuesBinding, "INSERT")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := producer.root.(*derivedNode); ok {
			t.Fatal("VALUES provenance became SELECT materialization")
		}
	})
	t.Run("pseudo_metadata_namespace", func(t *testing.T) {
		binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		binder.queryAuth = &auth
		binder.ruleNamespace = &ruleNamespace{relation: src, ref: ruleUnitRef(e, src), event: "INSERT"}
		binder.queryFrame, binder.nextQueryFrame = 1, 1
		statements, err := parser.Parse("SELECT NEW.id,NEW.v")
		if err != nil {
			t.Fatal(err)
		}
		query, err := binder.bindSelect(statements[0].(*parser.Select), true)
		if err != nil {
			t.Fatal(err)
		}
		project := query.Root.(*projectNode)
		if len(project.Schema()) != 2 || project.Schema()[0].Usage != src.Columns[0].Usage || project.Schema()[1].Usage != src.Columns[1].Usage {
			t.Fatal("pseudo frame borrowed unrelated FROM metadata")
		}
		for _, expression := range project.expressions {
			if expression.(columnExpression).scope != rulePseudoScope {
				t.Fatal("pseudo scope identity erased during analysis")
			}
		}
		row, matched, err := binder.ruleColumn(parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "new"}}}})
		if err != nil || !matched {
			t.Fatalf("typed NEW whole row: matched=%v error=%v", matched, err)
		}
		if len(row.(rowExpression).fields) != len(src.Columns) {
			t.Fatal("NEW whole row lost stable members")
		}
	})
	t.Run("domain_truth_barrier", func(t *testing.T) {
		leaf := domainValueExpression{Info: expr.Info{Usage: src.Columns[0].Usage, Property: expr.Properties{DomainDependent: true, RuntimeDomainConversion: true}}}
		properties := rewrittenIntrinsicProperties(leaf)
		if !properties.DomainDependent || !properties.RuntimeDomainConversion {
			t.Fatal("rewrite erased runtime domain authority")
		}
		properties.ParameterDependent = true
		mapped, _, err := mapBoundExpression(auth.catalog, leaf, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
			copy := expr.(domainValueExpression)
			copy.Property = properties
			return copy, true, nil
		})
		if err != nil || mapped.Properties() != properties || leaf.Properties().ParameterDependent {
			t.Fatal("domain leaf property copy lost authority or mutated template", err)
		}
	})
	t.Run("generic_function_properties", func(t *testing.T) {
		binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		binder.ruleNamespace = &ruleNamespace{relation: src, ref: ruleUnitRef(e, src), event: "INSERT"}
		statements, err := parser.Parse("SELECT abs(NEW.v),random(1,2),format('%s',NEW.v)")
		if err != nil {
			t.Fatal(err)
		}
		bind := func(index int) expr.Expr {
			node, err := binder.bind(statements[0].(*parser.Select).Targets[index], expressionPolicy{context: projectionContext})
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
			if column, ok := expr.(columnExpression); ok && column.scope == rulePseudoScope {
				return draw, true, nil
			}
			return expr, false, nil
		}
		identityType := func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }
		for _, parent := range []expr.Expr{absolute, format} {
			mapped, _, err := mapBoundExpression(auth.catalog, parent, identityType, nil, nil, replaceFormal)
			if err != nil || mapped.Properties().Volatility != expr.Volatile || mapped.Properties().RowDependent {
				t.Fatalf("generic parent lost fresh child demand: %v %v", mapped, err)
			}
			if !parent.Properties().RowDependent {
				t.Fatal("mutated original formal-dependent function")
			}
			if _, formatting := mapped.(functions.Format); formatting {
				stable, _, err := mapBoundExpression(auth.catalog, mapped, identityType, nil, nil, func(expr expr.Expr) (expr.Expr, bool, error) {
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
		single := ruleUnitInsert(e, src, 1, 10).binding
		producer, err := makeRuleProducer(auth.catalog, single, "INSERT")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := producer.root.(*singletonNode); !ok {
			t.Fatal("single VALUES became a materialized supplied row")
		}
		for i, expression := range single.expressions[0] {
			if !sameGroupExpression(producer.new[i], expression) {
				t.Fatal("single VALUES recipe replaced by an output Var")
			}
		}
	})
	t.Run("event_source_identity", func(t *testing.T) {
		f := viewWriteMechanics(t)
		original := binding{relation: f.base, ref: f.baseRef, storage: f.e.storage[f.base.Address()], targets: []int{0}, usedColumns: []int{0}, update: &boundUpdate{assignments: []expr.Expr{literalExpression{Info: expr.Info{Usage: f.integer.Usage()}, value: sqltype.Null(f.integer)}}}}
		producer, err := makeRuleProducer(f.e.CatalogRead(), original, "UPDATE")
		if err != nil {
			t.Fatal(err)
		}
		if len(producer.sources) != 1 || producer.sources[0].storage != original.storage {
			t.Fatal("event producer lost original physical storage capture")
		}
		current, err := currentQuerySourceLocked(f.e, f.e.CatalogRead(), nil, producer.sources[0])
		if err != nil || current != f.base {
			t.Fatal("event source fails its genuine identity/storage fence", err)
		}
		op, done := roleOperation(t, f.e, "postgres")
		defer done()
		authority := f.authorization(t, op)
		imported := producer.sources[0]
		imported.storage = nil
		refreshed, err := refreshRuleSources(authority, binding{querySources: []queryLeaf{imported}})
		if err != nil || refreshed.querySources[0].storage != original.storage || imported.storage != nil {
			t.Fatal("stored/imported source did not capture current physical storage immutably", err)
		}
	})
}

func TestRuleProgramOrdering(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	src.Rules = rulesPayloadOf([]ruleDefinition{{OID: 900101, Name: "a", event: "INSERT", actions: []ruleAction{ruleUnitInsert(e, sink, 1, 10), ruleUnitInsert(e, sink, 2, 20)}}, {OID: 900102, Name: "z", event: "INSERT", actions: []ruleAction{ruleUnitInsert(e, sink, 3, 30)}}})
	original := ruleUnitInsert(e, src, 4, 40)
	p := ruleProgram{count: -1, returning: -1}
	e.mu.Lock()
	count, err := e.expandRulesLocked(nil, auth, original.syntax, original.binding, nil, &p)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 || len(p.commands) != 4 {
		t.Fatalf("authority=%d commands=%d want0/4", count, len(p.commands))
	}
	for i, want := range []int64{1, 2, 3} {
		project := p.commands[i+1].binding.query.Root.(*projectNode)
		literal, ok := project.expressions[0].(literalExpression)
		if !ok || !sqltype.SameDatum(literal.value, ruleUnitInt(src, want).(literalExpression).value) {
			t.Fatalf("action order %d differs from %d", i, want)
		}
	}
}

func TestRuleProgramAuthority(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	src.Rules = rulesPayloadOf([]ruleDefinition{{OID: 900101, Name: "a", event: "INSERT", instead: true, actions: []ruleAction{ruleUnitInsert(e, sink, 1, 10)}}, {OID: 900102, Name: "z", event: "INSERT", instead: true, actions: []ruleAction{ruleUnitInsert(e, sink, 2, 20)}}})
	original := ruleUnitInsert(e, src, 9, 90)
	p := ruleProgram{count: -1, returning: -1}
	e.mu.Lock()
	count, err := e.expandRulesLocked(nil, auth, original.syntax, original.binding, nil, &p)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(p.commands) != 2 {
		t.Fatalf("last same-event authority=%d commands=%d want1/2", count, len(p.commands))
	}
	src.Rules = rulesPayloadOf([]ruleDefinition{{OID: 900101, Name: "nothing", event: "INSERT", instead: true}})
	p = ruleProgram{count: -1, returning: -1}
	e.mu.Lock()
	count, err = e.expandRulesLocked(nil, auth, original.syntax, original.binding, nil, &p)
	e.mu.Unlock()
	if err != nil || count != -1 || len(p.commands) != 0 {
		t.Fatalf("NOTHING authority=%d commands=%d error=%v", count, len(p.commands), err)
	}
	if len(p.events) != 1 || p.events[0].Ref != ruleUnitRef(e, src) || p.events[0].Revision != src.Revision() {
		t.Fatal("NOTHING lost rule event locking/prepared dependency authority")
	}
	t.Run("layered_boundary", func(t *testing.T) {
		f := viewWriteMechanics(t)
		op, done := roleOperation(t, f.e, "postgres")
		defer done()
		auth := f.authorization(t, op)
		f.base.RelationKind, f.base.VirtualView = viewRelation, true
		f.base.Rules = rulesPayloadOf([]ruleDefinition{{OID: 900201, Name: "inner_stop", event: "UPDATE", instead: true}})
		viewOf(f.view).checkOption = viewCheckCascaded
		project := viewOf(f.view).query.Root.(*projectNode)
		predicate := literalExpression{Info: expr.Info{Usage: f.boolean.Usage()}, value: sqltype.Boolean(f.boolean, true)}
		project.child = &filterNode{querySchema: querySchema(f.base.Columns), child: project.child, predicate: predicate}
		original := binding{relation: f.view, ref: f.viewRef, targets: []int{0}, update: &boundUpdate{assignments: []expr.Expr{literalExpression{Info: expr.Info{Usage: f.integer.Usage()}, value: sqltype.Null(f.integer)}}}}
		mapped := original
		if err := f.e.bindViewWrite(&mapped, auth, &parser.Update{}); err != nil {
			t.Fatal(err)
		}
		if mapped.ref != f.viewRef || mapped.relation != f.view || mapped.physicalBinding().ref != f.baseRef || mapped.targets[0] != 1 {
			t.Fatal("layered outer authority or inner event mapping lost")
		}
		if len(mapped.viewWrite.checks) != 0 || len(mapped.viewWrite.predicates) == 0 {
			t.Fatal("rewrite boundary conflated CHECK enforcement with selection")
		}
		if original.viewWrite != nil || original.targets[0] != 0 || original.ref != f.viewRef {
			t.Fatal("original binding mutated")
		}
		program := ruleProgram{count: -1, returning: -1}
		f.e.mu.Lock()
		count, err := f.e.expandRulesLocked(nil, auth, &parser.Update{}, mapped, nil, &program)
		f.e.mu.Unlock()
		if err != nil || count != -1 || len(program.commands) != 0 {
			t.Fatalf("inner unconditional boundary must stop before automatic validation: count=%d commands=%d error=%v", count, len(program.commands), err)
		}
	})
}

func TestRuleProgramRecursion(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	src.Rules = rulesPayloadOf([]ruleDefinition{{OID: 900101, Name: "cycle", event: "INSERT", actions: []ruleAction{ruleUnitInsert(e, sink, 1, 10)}}})
	sink.Rules = rulesPayloadOf([]ruleDefinition{{OID: 900102, Name: "cycle", event: "INSERT", actions: []ruleAction{ruleUnitInsert(e, src, 1, 10)}}})
	original := ruleUnitInsert(e, src, 9, 90)
	p := ruleProgram{}
	e.mu.Lock()
	_, err := e.expandRulesLocked(nil, auth, original.syntax, original.binding, nil, &p)
	e.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "infinite recursion detected in rules for relation") {
		t.Fatalf("recursion error=%v", err)
	}
	sink.Rules = nil
	rulesOf(src)[0].actions = append(rulesOf(src)[0].actions, ruleUnitInsert(e, sink, 2, 20))
	p = ruleProgram{}
	e.mu.Lock()
	_, err = e.expandRulesLocked(nil, auth, original.syntax, original.binding, nil, &p)
	e.mu.Unlock()
	if err != nil || len(p.commands) != 3 {
		t.Fatalf("sibling target reuse: commands=%d error=%v", len(p.commands), err)
	}
}

func TestRuleProgramAtomicStage(t *testing.T) {
	e, _, src, sink := ruleUnitCatalog(t)
	start := time.Unix(10, 0)
	expected := &StatementDescription{}
	s := &StatementScope{op: &Operation{}, statementStart: start, Expected: expected}
	s.lockedRef, s.lockedDescriptor = ruleUnitRef(e, src), src
	s.self = s
	s.stage.self = &s.stage
	s.stage.scope = s
	s.stage.rows = map[ids.ObjectAddress][]rowVersion{src.Address(): {{row: 1, version: 1}}}
	before := s.ruleScratch()
	stageRows := s.stage.rows
	s.installRuleScratch(ruleCommandScratch{ref: ruleUnitRef(e, sink), descriptor: sink})
	if s.stage.self != &s.stage || s.stage.scope != s || len(s.stage.rows[src.Address()]) != 1 || s.Expected != expected || s.statementStart != start {
		t.Fatal("action scratch changed original envelope/stage")
	}
	s.installRuleScratch(before)
	if s.lockedRef.Address != src.Address() || !maps.EqualFunc(stageRows, s.stage.rows, func(a, b []rowVersion) bool { return len(a) == len(b) && a[0].row == b[0].row }) {
		t.Fatal("scratch restoration lost original stage")
	}
}

func TestRuleDefinitionDependencies(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	t.Run("unused_owned_modifier", func(t *testing.T) {
		outer := ruleUnitInsert(e, src, 1, 10)
		auxiliary := ruleUnitInsert(e, sink, 7, 70)
		outer.binding.ctes = []*cteDefinition{{statement: auxiliary.syntax, mutation: &auxiliary.binding, owner: 1}}
		refs, err := ruleReferences(context.Background(), auth.catalog, ruleDefinition{actions: []ruleAction{outer}})
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
	condition := columnExpression{Info: ruleColumnInfo(src.Columns[0]), origin: ref, scope: rulePseudoScope, frame: 2}
	r := ruleDefinition{OID: 900101, Name: "r", event: "INSERT", condition: condition, actions: []ruleAction{ruleUnitInsert(e, sink, 1, 10)}}
	refs, err := ruleReferences(context.Background(), auth.catalog, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []ids.ObjectAddress{ref.Address, {ClassID: ids.RelationClassID, ObjectID: sink.Address().ObjectID, SubID: 1}, {ClassID: ids.RelationClassID, ObjectID: sink.Address().ObjectID, SubID: 2}} {
		if !slices.Contains(refs, want) {
			t.Fatalf("missing dependency %v in %v", want, refs)
		}
	}
	r.references = refs
	src.Rules = rulesPayloadOf([]ruleDefinition{r})
	edges, err := collectPGDepend(context.Background(), auth.catalog)
	if err != nil {
		t.Fatal(err)
	}
	owner := ids.ObjectAddress{ClassID: 2618, ObjectID: r.OID}
	if !slices.Contains(edges, pgDependEdge{owner, src.Address(), 'a'}) {
		t.Fatal("rule lacks AUTO event edge")
	}
	t.Run("mixed_drop_closure", func(t *testing.T) {
		stage := e.BeginCatalogStage(auth.catalog)
		defer e.DiscardCatalogStage(stage)
		dependents, err := domainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{sink.Address()})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, dependent := range dependents {
			if dependent.address == owner && dependent.relation == src && dependent.target == sink.Address() && !dependent.automatic {
				found = true
			}
		}
		if !found {
			t.Fatal("normal rule target absent from central mixed DROP closure")
		}
	})
	reserved := make(map[ids.OID]bool)
	reserveDefinitionOIDs(reserved, src)
	if !reserved[r.OID] || !definitionOIDUsed(src, r.OID) {
		t.Fatal("rule OID omitted from identity space")
	}
}

func TestRuleDefinitionRemap(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	oldRef := ruleUnitRef(e, src)
	oldRef.Address.SubID = 1
	src.Rules = rulesPayloadOf([]ruleDefinition{{OID: 900101, Name: "r", event: "INSERT", condition: columnExpression{Info: ruleColumnInfo(src.Columns[0]), origin: oldRef, scope: rulePseudoScope, frame: 2}, references: []ids.ObjectAddress{oldRef.Address}, actions: []ruleAction{ruleUnitInsert(e, sink, 1, 10)}}})
	newSrc, newSink := *src, *sink
	newSrc.ObjectAddress.ObjectID += 10
	newSink.ObjectAddress.ObjectID += 10
	next := cloneCatalog(auth.catalog.State())
	next.setDescriptor(newSrc.Address(), &newSrc)
	next.setDescriptor(newSink.Address(), &newSink)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{src.Address(): newSrc.Address(), sink.Address(): newSink.Address()}
	ref := func(old ids.ObjectRef) ids.ObjectRef {
		whole := old.Address
		whole.SubID = 0
		if a, ok := mapping[whole]; ok {
			a.SubID = old.Address.SubID
			old.Address = a
		}
		old = e.catalog.Domain.Ref(old.Epoch, old.Address)
		old.Epoch = next.Epoch()
		return old
	}
	mapped, err := remapRules(e.catalog.read(next), src, mapping, ref, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rulesOf(mapped)[0].condition.(columnExpression).origin.Address.ObjectID != newSrc.Address().ObjectID || rulesOf(mapped)[0].actions[0].binding.ref.Address != newSink.Address() {
		t.Fatal("nested rule references not remapped")
	}
	if rulesOf(src)[0].condition.(columnExpression).origin != oldRef || rulesOf(src)[0].actions[0].binding.ref.Address != sink.Address() {
		t.Fatal("remapping mutated original template")
	}
}

func TestRuleViewReturnReplacement(t *testing.T) {
	_, _, src, _ := ruleUnitCatalog(t)
	if err := ruleResultCheck(src, slices.Clone(src.Columns), true); err != nil {
		t.Fatal(err)
	}
	if err := ruleResultCheck(src, src.Columns[:1], true); err == nil || !strings.Contains(err.Error(), "SELECT rule's target list has too few entries") {
		t.Fatalf("arity error=%v", err)
	}
	bad := slices.Clone(src.Columns)
	bad[0].Name = "other"
	if err := ruleResultCheck(src, bad, true); err == nil || !strings.Contains(err.Error(), "different column name") {
		t.Fatalf("name error=%v", err)
	}
	if err := ruleResultCheck(src, bad, false); err != nil {
		t.Fatal("RETURNING incorrectly requires event column names:", err)
	}
}

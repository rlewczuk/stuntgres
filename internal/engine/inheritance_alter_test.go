package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestInheritanceAlterOrigins(t *testing.T) {
	for _, kind := range []string{"column", "notnull", "check"} {
		for _, only := range []bool{false, true} {
			for _, other := range []bool{false, true} {
				for _, local := range []bool{false, true} {
					e := New()
					p := inheritanceUnitRelation(e, "p", 22000, "a")
					q := inheritanceUnitRelation(e, "q", 22001, "a")
					c := inheritanceUnitRelation(e, "c", 22002, "extra", "a")
					c.attributes = []int32{2, 7}
					c.attributeHighWater = 7
					parents := []ObjectAddress{p.address}
					origins := []inheritanceOrigin{{parent: p.address, attribute: 1}}
					if other {
						parents = append(parents, q.address)
						origins = append(origins, inheritanceOrigin{parent: q.address, attribute: 1})
					}
					c.inheritance = &tableInheritance{parents: parents, columns: map[int32]inheritedColumn{2: {local: true}, 7: {local: local, origins: origins}}, notNull: map[int32]inheritedConstraint{}, checks: map[string]inheritedConstraint{}}
					original := cloneInheritance(c.inheritance)
					next := *p
					next.revision++
					action := parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: "a"}}
					switch kind {
					case "column":
						next.columns = nil
						next.attributes = nil
						next.index = map[string]int{}
					case "notnull":
						p.notNullConstraints = []notNullConstraint{{OID: 23000, Name: "fact", Attribute: 1}}
						q.notNullConstraints = []notNullConstraint{{OID: 23001, Name: "fact", Attribute: 1}}
						c.notNullConstraints = []notNullConstraint{{OID: 23002, Name: "fact", Attribute: 7}}
						nn := append([]inheritanceOrigin(nil), origins...)
						for i := range nn {
							nn[i].constraint = 23000 + OID(i)
						}
						c.inheritance.notNull[7] = inheritedConstraint{local: local, origins: nn}
						original = cloneInheritance(c.inheritance)
						next.notNullConstraints = nil
						action = parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: "fact"}}
					case "check":
						p.checkConstraints = []checkConstraint{{OID: 23000, Name: "fact"}}
						q.checkConstraints = []checkConstraint{{OID: 23001, Name: "fact"}}
						c.checkConstraints = []checkConstraint{{OID: 23002, Name: "fact"}}
						checks := append([]inheritanceOrigin(nil), origins...)
						for i := range checks {
							checks[i].attribute = 0
							checks[i].constraint = 23000 + OID(i)
						}
						c.inheritance.checks["fact"] = inheritedConstraint{local: local, origins: checks}
						original = cloneInheritance(c.inheritance)
						next.checkConstraints = nil
						action = parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: "fact"}}
					}
					view := inheritanceUnitView(e, p, q, c)
					stage := e.BeginCatalogStage(view)
					stage.descriptors[p.address] = &next
					state := &inheritanceAlterState{stage: stage, root: p, targets: []*relationDescriptor{p, c}, selected: map[ObjectAddress]bool{p.address: true, c.address: true}, only: only, actions: []parser.AlterTableAction{action}, events: make(map[ObjectAddress][]inheritanceAlterEvent)}
					(&StatementScope{inheritanceAlter: state}).recordInheritanceAlterAction(p, &next, action)
					actions, graph, err := e.inheritedAlterActions(state, c)
					if err != nil {
						t.Fatal(kind, only, other, local, err)
					}
					wantLocal := local || (only && (!other || kind == "column"))
					wantOrigins := 0
					if other {
						wantOrigins = 1
					}
					actualLocal := graph.columns[7].local
					actualOrigins := graph.columns[7].origins
					if kind == "notnull" {
						actualLocal = graph.notNull[7].local
						actualOrigins = graph.notNull[7].origins
					}
					if kind == "check" {
						actualLocal = graph.checks["fact"].local
						actualOrigins = graph.checks["fact"].origins
					}
					if actualLocal != wantLocal || len(actualOrigins) != wantOrigins {
						t.Fatal("wrong retained provenance", kind, only, other, local, graph)
					}
					if other && (actualOrigins[0].parent != q.address || kind != "check" && actualOrigins[0].attribute != 1) {
						t.Fatal("surviving parent identity/attribute changed", actualOrigins)
					}
					wantDrop := !wantLocal && !other
					if (len(actions) == 1) != wantDrop || len(actions) > 1 {
						t.Fatal("wrong physical target selection", kind, only, other, local, actions)
					}
					if !reflect.DeepEqual(c.inheritance, original) {
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
	parentStore, childStore := e.storage[p.address], e.storage[c.address]
	parentRows := append([]rowVersion(nil), parentStore.versions...)
	childRows := append([]rowVersion(nil), childStore.versions...)
	stage := e.BeginCatalogStage(before)
	defer e.DiscardCatalogStage(stage)
	for _, old := range []*relationDescriptor{p, c} {
		next := *old
		next.revision++
		next.comment = "candidate"
		store := e.storage[old.address]
		ref := ObjectRef{owner: e.domain, Epoch: before.Epoch(), Address: old.address}
		if err := stage.alterRelation(context.Background(), ref, &next, store, store, len(store.versions), store.generation, store.ends); err != nil {
			t.Fatal(err)
		}
	}
	// A real later physical store observation changes after both descriptor
	// candidates were admitted. Final validation must reject the entire graph.
	childStore.generation++
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
		t.Fatal("stale later child observation validated")
	}
	if e.CatalogRead().state != before.state || e.storage[p.address] != parentStore || e.storage[c.address] != childStore {
		t.Fatal("late child veto partially published catalog/store identities")
	}
	if !reflect.DeepEqual(parentRows, parentStore.versions) || !reflect.DeepEqual(childRows, childStore.versions) {
		t.Fatal("late child veto changed physical rows")
	}
}

func TestInheritanceAlterDefinitionAuthority(t *testing.T) {
	e := New()
	p := inheritanceUnitRelation(e, "p", 22000, "a")
	c := inheritanceUnitRelation(e, "c", 22001, "a")
	usage := testUsage(e, sqltype.Int4)
	expression := func(n int64) BoundExpr {
		return literalExpression{expressionInfo: expressionInfo{usage: usage}, value: inheritanceQueryInt(e, n)}
	}
	p.defaults = map[int32]columnDefault{1: {expression: expression(1)}}
	c.defaults = map[int32]columnDefault{1: {expression: expression(9)}}
	provenance := inheritedColumn{origins: []inheritanceOrigin{{parent: p.address, attribute: 1}}}
	view := inheritanceUnitView(e, p, c)
	retained, err := retainInheritedRecipes(view, c, 1, provenance)
	if err != nil || retained.local || !retained.explicitDefault || !reflect.DeepEqual(retained.origins, provenance.origins) {
		t.Fatal("default authority changed attribute locality/origins", retained, err)
	}
	c.defaults = nil
	tombstone, err := retainInheritedRecipes(view, c, 1, provenance)
	if err != nil || tombstone.local || !tombstone.explicitDefault {
		t.Fatal("default absence lost explicit authority", tombstone, err)
	}
	p.defaults = nil
	plain, err := retainInheritedRecipes(view, c, 1, provenance)
	if err != nil || plain.explicitDefault || plain.local {
		t.Fatal("ordinary inherited absence fabricated authority", plain, err)
	}
	p = inheritanceUnitRelation(e, "gp", 22002, "a", "g")
	c = inheritanceUnitRelation(e, "gc", 22003, "a", "g")
	p.generations = map[int32]columnGeneration{2: {OID: 24000, kind: generationVirtual, expression: expression(1)}}
	c.generations = map[int32]columnGeneration{2: {OID: 24001, kind: generationVirtual, expression: expression(2)}}
	c.inheritance = &tableInheritance{parents: []ObjectAddress{p.address}, columns: map[int32]inheritedColumn{1: {origins: []inheritanceOrigin{{parent: p.address, attribute: 1}}}, 2: {origins: []inheritanceOrigin{{parent: p.address, attribute: 2}}}}, notNull: map[int32]inheritedConstraint{}, checks: map[string]inheritedConstraint{}}
	view = inheritanceUnitView(e, p, c)
	if err := validateInheritance(view, c); err != nil {
		t.Fatal("legal ONLY recipe divergence invalidated column origins", err)
	}
	effective, err := retainInheritedRecipes(view, c, 2, c.inheritance.columns[2])
	if err != nil || effective.local || !effective.explicitGeneration || c.inheritance.columns[2].explicitGeneration {
		t.Fatal("capture failed to derive retained recipe without retagging live child", effective, err)
	}
	for _, kind := range []string{"kind", "usage", "origin"} {
		next := *c
		next.columns = append([]ColumnDescriptor(nil), c.columns...)
		next.inheritance = cloneInheritance(c.inheritance)
		switch kind {
		case "kind":
			next.generations = map[int32]columnGeneration{2: {OID: 24001, kind: generationStored, expression: expression(2)}}
		case "usage":
			next.columns[1].Usage = testUsage(e, sqltype.Int8)
		case "origin":
			next.inheritance.columns[2] = inheritedColumn{origins: []inheritanceOrigin{{parent: p.address, attribute: 1}}}
		}
		if err := validateInheritance(view, &next); err == nil {
			t.Fatal("recipe equality relaxation bypassed independent invariant", kind)
		}
	}
	foreign := New()
	definition := c.generations[2]
	definition.dependencies = []ObjectRef{{owner: foreign.domain, Epoch: view.Epoch(), Address: ObjectAddress{ClassID: TypeClassID, ObjectID: OID(sqltype.Int4)}}}
	invalid := *c
	invalid.generations = map[int32]columnGeneration{2: definition}
	if err := validateGenerationProviders(view, &invalid); err == nil {
		t.Fatal("foreign generation provider gained authority")
	}
	if err := validateGenerationPlan(view, c, literalExpression{}); err == nil {
		t.Fatal("missing own expression Usage passed validation")
	}
}

func TestInheritanceAlterOnlyMixedActions(t *testing.T) {
	expectInts := func(t *testing.T, e *Engine, query string, want [][]int64) {
		t.Helper()
		rows := run(t, e, query).Rows
		values := make([]sqltype.Value, len(rows.Schema()))
		for i, expected := range want {
			present, err := rows.Next(context.Background(), values)
			if err != nil || !present {
				t.Fatal(query, i, present, err)
			}
			for j, n := range expected {
				actual, err := values[j].Integer()
				if err != nil || actual != n {
					t.Fatal(query, i, j, actual, n, err)
				}
			}
		}
		if present, err := rows.Next(context.Background(), values); err != nil || present {
			t.Fatal(query, "extra row", present, err)
		}
	}
	expectError := func(t *testing.T, e *Engine, query, code string) {
		t.Helper()
		_, err := testExecute(e, context.Background(), statement(t, query), nil)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != code {
			t.Fatal(query, err, code)
		}
	}
	t.Run("drop_then_root_expression", func(t *testing.T) {
		e := New()
		for _, q := range []string{
			"CREATE TABLE p(a INT,b INT,g INT GENERATED ALWAYS AS(a+1) STORED)",
			"CREATE TABLE c() INHERITS(p)",
			"INSERT INTO p(a,b) VALUES(1,9)", "INSERT INTO c(a,b) VALUES(2,8)",
		} {
			ownershipUnitExec(t, e, q)
		}
		before := retiredUnitTable(t, e, "c")
		g := before.attributes[before.index["g"]]
		generation := before.generations[g]
		ownershipUnitExec(t, e, "ALTER TABLE ONLY p DROP COLUMN b,ALTER g SET EXPRESSION AS(a+10)")
		parent := retiredUnitTable(t, e, "p")
		child := retiredUnitTable(t, e, "c")
		if _, exists := parent.index["b"]; exists {
			t.Fatal("root column survived actual DROP")
		}
		provenance := child.inheritance.columns[child.attributes[child.index["b"]]]
		if !provenance.local || len(provenance.origins) != 0 || child.generations[g].OID != generation.OID {
			t.Fatal("ONLY later recipe changed child definition", provenance, child.generations[g])
		}
		expectInts(t, e, "SELECT a,g FROM ONLY p", [][]int64{{1, 11}})
		expectInts(t, e, "SELECT a,b,g FROM ONLY c", [][]int64{{2, 8, 3}})
	})
	t.Run("drop_then_physical_unique", func(t *testing.T) {
		e := New()
		for _, q := range []string{"CREATE TABLE p(a INT,b INT)", "CREATE TABLE c() INHERITS(p)", "INSERT INTO p VALUES(1,9)", "INSERT INTO c VALUES(2,8)"} {
			ownershipUnitExec(t, e, q)
		}
		ownershipUnitExec(t, e, "ALTER TABLE ONLY p DROP COLUMN b,ADD CONSTRAINT uq UNIQUE(a)")
		parent := retiredUnitTable(t, e, "p")
		child := retiredUnitTable(t, e, "c")
		if len(parent.uniqueConstraints) != 1 || parent.uniqueConstraints[0].Name != "uq" || len(child.uniqueConstraints) != 0 {
			t.Fatal("physical key propagated or was skipped")
		}
		expectError(t, e, "INSERT INTO p VALUES(1)", "23505")
		ownershipUnitExec(t, e, "INSERT INTO c VALUES(1,7)")
		expectInts(t, e, "SELECT a FROM ONLY p", [][]int64{{1}})
		expectInts(t, e, "SELECT a,b FROM ONLY c ORDER BY a", [][]int64{{1, 7}, {2, 8}})
	})
	for _, kind := range []string{"notnull", "check"} {
		t.Run("only_drop_add_"+kind, func(t *testing.T) {
			e := New()
			declaration := "a INT CONSTRAINT fact NOT NULL"
			addition := "ADD CONSTRAINT replacement NOT NULL a"
			if kind == "check" {
				declaration = "a INT CONSTRAINT fact CHECK(a>0)"
				addition = "ADD CONSTRAINT fact CHECK(a>1)"
			}
			ownershipUnitExec(t, e, "CREATE TABLE p("+declaration+")")
			ownershipUnitExec(t, e, "CREATE TABLE c() INHERITS(p)")
			before := e.CatalogRead()
			parent := retiredUnitTable(t, e, "p")
			child := retiredUnitTable(t, e, "c")
			expectError(t, e, "ALTER TABLE ONLY p DROP CONSTRAINT fact,"+addition, "42P16")
			if kind == "check" {
				expectError(t, e, "ALTER TABLE ONLY p DROP CONSTRAINT fact,ADD CONSTRAINT fact CHECK(missing>0)", "42703")
			}
			if e.CatalogRead().state != before.state || retiredUnitTable(t, e, "p") != parent || retiredUnitTable(t, e, "c") != child {
				t.Fatal("ONLY phase-local prohibition published partial DROP")
			}
		})
	}
}

func TestInheritanceAlterCheckProvenanceAtomicity(t *testing.T) {
	e := New()
	for _, query := range []string{"CREATE TABLE p(a INT)", "CREATE TABLE c() INHERITS(p)", "CREATE TABLE leaf() INHERITS(c)", "INSERT INTO p VALUES(2)", "INSERT INTO c VALUES(3)", "INSERT INTO leaf VALUES(4)", "ALTER TABLE p ADD CONSTRAINT ck CHECK(a>0)"} {
		ownershipUnitExec(t, e, query)
	}
	before := e.CatalogRead()
	originals := make(map[string]*relationDescriptor)
	graphs := make(map[string]*tableInheritance)
	for _, name := range []string{"p", "c", "leaf"} {
		table := retiredUnitTable(t, e, name)
		originals[name] = table
		graphs[name] = cloneInheritance(table.inheritance)
		if name != "p" {
			fact := table.inheritance.checks["ck"]
			if fact.local || len(fact.origins) != 1 {
				t.Fatal("recursive new CHECK acquired local contribution", name, fact)
			}
		}
	}
	_, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE p DROP CONSTRAINT ck,ADD CONSTRAINT ck CHECK(a<4)"), nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" || diagnostic.Table != "leaf" {
		t.Fatal("actual later CHECK veto missing", err)
	}
	if e.CatalogRead().state != before.state {
		t.Fatal("failed CHECK replacement published catalog")
	}
	for name, original := range originals {
		if retiredUnitTable(t, e, name) != original || !reflect.DeepEqual(original.inheritance, graphs[name]) {
			t.Fatal("failed binding mutated captured CHECK provenance", name)
		}
	}
	ownershipUnitExec(t, e, "ALTER TABLE p DROP CONSTRAINT ck,ADD CONSTRAINT ck CHECK(a>1)")
	for name, original := range originals {
		table := retiredUnitTable(t, e, name)
		if len(table.checkConstraints) != 1 || table.checkConstraints[0].OID == original.checkConstraints[0].OID {
			t.Fatal("genuine DROP/ADD did not replace physical CHECK", name)
		}
		if name != "p" {
			fact := table.inheritance.checks["ck"]
			if fact.local || len(fact.origins) != 1 {
				t.Fatal("replacement localized inherited CHECK", name, fact)
			}
		}
	}
}

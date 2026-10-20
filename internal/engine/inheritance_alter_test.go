package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
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
					c.Attributes = []int32{2, 7}
					c.ColumnMeta.HighWater = 7
					parents := []ids.ObjectAddress{p.Address()}
					origins := []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}}
					if other {
						parents = append(parents, q.Address())
						origins = append(origins, catalog.InheritanceOrigin{Parent: q.Address(), Attribute: 1})
					}
					c.Inheritance = &catalog.TableInheritance{Parents: parents, Columns: map[int32]catalog.InheritedColumn{2: {Local: true}, 7: {Local: local, Origins: origins}}, NotNull: map[int32]catalog.InheritedConstraint{}, Checks: map[string]catalog.InheritedConstraint{}}
					original := cloneInheritance(c.Inheritance)
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
						original = cloneInheritance(c.Inheritance)
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
						original = cloneInheritance(c.Inheritance)
						next.Constraints.Check = nil
						action = parser.AlterTableAction{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: "fact"}}
					}
					view := inheritanceUnitView(e, p, q, c)
					stage := e.BeginCatalogStage(view)
					stage.UpdateDescriptor(&next)
					state := &inheritanceAlterState{stage: stage, root: p, targets: []*catalog.Relation{p, c}, selected: map[ids.ObjectAddress]bool{p.Address(): true, c.Address(): true}, only: only, actions: []parser.AlterTableAction{action}, events: make(map[ids.ObjectAddress][]inheritanceAlterEvent)}
					(&StatementScope{inheritance: inheritanceScope{alter: state}}).recordInheritanceAlterAction(p, &next, action)
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
	parentStore, childStore := e.storage.Relations[p.Address()], e.storage.Relations[c.Address()]
	parentRows := append([]storage.RowVersion(nil), parentStore.Versions...)
	childRows := append([]storage.RowVersion(nil), childStore.Versions...)
	stage := e.BeginCatalogStage(before)
	defer e.DiscardCatalogStage(stage)
	for _, old := range []*catalog.Relation{p, c} {
		next := *old
		next.ObjectRevision++
		next.Comment = "candidate"
		store := e.storage.Relations[old.Address()]
		ref := e.catalog.Domain.Ref(before.Epoch(), old.Address())
		if err := stage.alterRelation(context.Background(), ref, &next, store, store, len(store.Versions), store.Generation, store.Ends); err != nil {
			t.Fatal(err)
		}
	}
	// A real later physical store observation changes after both descriptor
	// candidates were admitted. Final validation must reject the entire graph.
	childStore.Generation++
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err == nil {
		t.Fatal("stale later child observation validated")
	}
	if e.CatalogRead().State() != before.State() || e.storage.Relations[p.Address()] != parentStore || e.storage.Relations[c.Address()] != childStore {
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
		return literalExpression{Info: expr.Info{Usage: usage}, Value: inheritanceQueryInt(e, n)}
	}
	p.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {Expression: expression(1)}}
	c.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {Expression: expression(9)}}
	provenance := catalog.InheritedColumn{Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}}}
	view := inheritanceUnitView(e, p, c)
	retained, err := retainInheritedRecipes(view, c, 1, provenance)
	if err != nil || retained.Local || !retained.ExplicitDefault || !reflect.DeepEqual(retained.Origins, provenance.Origins) {
		t.Fatal("default authority changed attribute locality/origins", retained, err)
	}
	c.ColumnMeta.Defaults = nil
	tombstone, err := retainInheritedRecipes(view, c, 1, provenance)
	if err != nil || tombstone.Local || !tombstone.ExplicitDefault {
		t.Fatal("default absence lost explicit authority", tombstone, err)
	}
	p.ColumnMeta.Defaults = nil
	plain, err := retainInheritedRecipes(view, c, 1, provenance)
	if err != nil || plain.ExplicitDefault || plain.Local {
		t.Fatal("ordinary inherited absence fabricated authority", plain, err)
	}
	p = inheritanceUnitRelation(e, "gp", 22002, "a", "g")
	c = inheritanceUnitRelation(e, "gc", 22003, "a", "g")
	p.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {OID: 24000, Kind: catalog.GenerationVirtual, Expression: expression(1)}}
	c.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {OID: 24001, Kind: catalog.GenerationVirtual, Expression: expression(2)}}
	c.Inheritance = &catalog.TableInheritance{Parents: []ids.ObjectAddress{p.Address()}, Columns: map[int32]catalog.InheritedColumn{1: {Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}}}, 2: {Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 2}}}}, NotNull: map[int32]catalog.InheritedConstraint{}, Checks: map[string]catalog.InheritedConstraint{}}
	view = inheritanceUnitView(e, p, c)
	if err := validateInheritance(view, c); err != nil {
		t.Fatal("legal ONLY recipe divergence invalidated column origins", err)
	}
	effective, err := retainInheritedRecipes(view, c, 2, c.Inheritance.Columns[2])
	if err != nil || effective.Local || !effective.ExplicitGeneration || c.Inheritance.Columns[2].ExplicitGeneration {
		t.Fatal("capture failed to derive retained recipe without retagging live child", effective, err)
	}
	for _, kind := range []string{"kind", "usage", "origin"} {
		next := *c
		next.Columns = append([]catalog.Column(nil), c.Columns...)
		next.Inheritance = cloneInheritance(c.Inheritance)
		switch kind {
		case "kind":
			next.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {OID: 24001, Kind: catalog.GenerationStored, Expression: expression(2)}}
		case "usage":
			next.Columns[1].Usage = testUsage(e, sqltype.Int8)
		case "origin":
			next.Inheritance.Columns[2] = catalog.InheritedColumn{Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}}}
		}
		if err := validateInheritance(view, &next); err == nil {
			t.Fatal("recipe equality relaxation bypassed independent invariant", kind)
		}
	}
	foreign := New()
	definition := c.ColumnMeta.Generations[2]
	definition.Dependencies = []ids.ObjectRef{foreign.catalog.Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})}
	invalid := *c
	invalid.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: definition}
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
		g := before.Attributes[before.Index["g"]]
		generation := before.ColumnMeta.Generations[g]
		ownershipUnitExec(t, e, "ALTER TABLE ONLY p DROP COLUMN b,ALTER g SET EXPRESSION AS(a+10)")
		parent := retiredUnitTable(t, e, "p")
		child := retiredUnitTable(t, e, "c")
		if _, exists := parent.Index["b"]; exists {
			t.Fatal("root column survived actual DROP")
		}
		provenance := child.Inheritance.Columns[child.Attributes[child.Index["b"]]]
		if !provenance.Local || len(provenance.Origins) != 0 || child.ColumnMeta.Generations[g].OID != generation.OID {
			t.Fatal("ONLY later recipe changed child definition", provenance, child.ColumnMeta.Generations[g])
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
		if len(parent.Constraints.Unique) != 1 || parent.Constraints.Unique[0].Name != "uq" || len(child.Constraints.Unique) != 0 {
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
			if e.CatalogRead().State() != before.State() || retiredUnitTable(t, e, "p") != parent || retiredUnitTable(t, e, "c") != child {
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
	originals := make(map[string]*catalog.Relation)
	graphs := make(map[string]*catalog.TableInheritance)
	for _, name := range []string{"p", "c", "leaf"} {
		table := retiredUnitTable(t, e, name)
		originals[name] = table
		graphs[name] = cloneInheritance(table.Inheritance)
		if name != "p" {
			fact := table.Inheritance.Checks["ck"]
			if fact.Local || len(fact.Origins) != 1 {
				t.Fatal("recursive new CHECK acquired local contribution", name, fact)
			}
		}
	}
	_, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE p DROP CONSTRAINT ck,ADD CONSTRAINT ck CHECK(a<4)"), nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" || diagnostic.Table != "leaf" {
		t.Fatal("actual later CHECK veto missing", err)
	}
	if e.CatalogRead().State() != before.State() {
		t.Fatal("failed CHECK replacement published catalog")
	}
	for name, original := range originals {
		if retiredUnitTable(t, e, name) != original || !reflect.DeepEqual(original.Inheritance, graphs[name]) {
			t.Fatal("failed binding mutated captured CHECK provenance", name)
		}
	}
	ownershipUnitExec(t, e, "ALTER TABLE p DROP CONSTRAINT ck,ADD CONSTRAINT ck CHECK(a>1)")
	for name, original := range originals {
		table := retiredUnitTable(t, e, name)
		if len(table.Constraints.Check) != 1 || table.Constraints.Check[0].OID == original.Constraints.Check[0].OID {
			t.Fatal("genuine DROP/ADD did not replace physical CHECK", name)
		}
		if name != "p" {
			fact := table.Inheritance.Checks["ck"]
			if fact.Local || len(fact.Origins) != 1 {
				t.Fatal("replacement localized inherited CHECK", name, fact)
			}
		}
	}
}

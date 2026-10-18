package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func alterGenerationAction(expr parser.Expr) parser.AlterTableAction {
	return parser.AlterTableAction{Action: parser.SetExpression, Column: parser.Identifier{Text: "b"}, Generation: &parser.GenerationDefinition{Expression: expr}}
}

func alterGenerationLiteral(n int64) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}}
}

func alterGenerationStatement(action parser.AlterTableAction) *parser.AlterTable {
	return &parser.AlterTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "g"}}}, Actions: []parser.AlterTableAction{action}}
}

func TestAlterGeneratedExpressionCandidate(t *testing.T) {
	e, admin, _, ref := generatedAuthorizationFixture(t)
	elements := []parser.TableElement{{Column: &parser.Column{Name: "a", TypeName: parser.TypeName{Name: "int4"}}}}
	for i, name := range []string{"b", "c"} {
		expr := alterGenerationOperator("+", alterGenerationColumn("a"), alterGenerationLiteral(int64(i+1)))
		if name == "c" {
			expr = alterGenerationLiteral(17)
		}
		elements = append(elements, parser.TableElement{Column: &parser.Column{Name: name, TypeName: parser.TypeName{Name: "int4"}, Constraints: []parser.ColumnConstraint{{Generation: &parser.GenerationDefinition{Mode: parser.IdentityAlways, Kind: "stored", Expression: expr}}}}})
	}
	if _, err := e.Execute(admin, &parser.CreateTable{Name: alterGenerationName("g2"), Elements: elements}, 0, nil); err != nil {
		t.Fatal(err)
	}
	ref = catalogRef(t, e, e.CatalogRead(), "g2")
	auth, err := admin.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(admin)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(errors.New("candidate-only unit"))
	old := e.catalog.state.MustRelation(ref.Address)
	original := old.Generations[2]
	retained := auth.catalog
	unaffected := old.Generations[3]
	next, attribute, err := e.generationCandidate(scope, admin, auth, old, alterGenerationAction(alterGenerationLiteral(8)))
	if err != nil || attribute != 2 || next == old || next.Generations[2].OID != 0 || next.Generations[2].Kind != generationStored {
		t.Fatal(next, attribute, err)
	}
	e.mu.Lock()
	err = e.assignDefinitionOIDsLocked(next)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if next.Generations[2].OID == 0 || next.Generations[2].OID == original.OID || !reflect.DeepEqual(old.Generations[2], original) || retained.Get(ref.Address) != old || !slices.Equal(old.Attributes, next.Attributes) || &old.Columns[0] != &next.Columns[0] {
		t.Fatal("source/shape identity changed")
	}
	if next.Generations[3].OID != unaffected.OID || !reflect.DeepEqual(next.Generations[3], unaffected) || !reflect.DeepEqual(old.Generations[3], unaffected) {
		t.Fatal("unaffected definition reminted/mutated")
	}
	drop := parser.AlterTableAction{Action: parser.DropExpression, Column: parser.Identifier{Text: "b"}}
	dropped, changed, err := e.generationCandidate(scope, admin, auth, next, drop)
	if err != nil || changed != 0 || len(dropped.Generations) != 1 || len(next.Generations) != 2 || dropped.Columns[1] != old.Columns[1] || dropped.Generations[3].OID != unaffected.OID {
		t.Fatal(dropped, changed, err)
	}
	target := ref.Address
	target.SubID = 1
	originalDrop, _, err := e.generationCandidate(scope, admin, auth, old, drop)
	if err != nil || !generationDepends(old, target) || generationDepends(next, target) || generationDepends(originalDrop, target) || originalDrop.Generations[3].OID != unaffected.OID {
		t.Fatal("SET/DROP dependency edges or unaffected definition wrong", err)
	}
	drop.IfExists = true
	same, _, err := e.generationCandidate(scope, admin, auth, dropped, drop)
	if err != nil || same != dropped || same.revision != dropped.revision {
		t.Fatal("no-op changed descriptor", err)
	}
	if e.catalog.state.Get(ref.Address) != old {
		t.Fatal("candidate published early")
	}
}

func TestAlterGeneratedExpressionCompletedRow(t *testing.T) {
	_, scope, _, base := alterAddUnitScope(t)
	u := base.Columns[0].Usage
	integer := func(n int64) sqltype.Value {
		t.Helper()
		v, err := sqltype.Integer(u.Type, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	old := *base
	old.Columns = []ColumnDescriptor{{Name: "a", Usage: u, Nullable: true}, {Name: "b", Usage: u, Nullable: true}, {Name: "untouched", Usage: u, Nullable: true}}
	old.Attributes = []int32{1, 2, 3}
	old.Index = map[string]int{"a": 0, "b": 1, "untouched": 2}
	next := old
	next.Columns = slices.Clone(old.Columns)
	next.Columns = append(next.Columns, ColumnDescriptor{Name: "c", Usage: u, Nullable: true})
	next.Attributes = []int32{1, 2, 3, 4}
	next.AttributeHighWater = 4
	next.Index = maps.Clone(old.Index)
	next.Index["c"] = 3
	calls := 0
	generated := queryProbeExpression{Info: expr.Info{Usage: u}, eval: func(ec *EvalContext) (sqltype.Value, error) {
		calls++
		if len(ec.Row) != 4 || ec.Row[0] != integer(31) || ec.Row[3] != integer(5) {
			t.Fatal("generation evaluated before completed tuple", ec.Row)
		}
		return integer(36), nil
	}}
	untouched := queryProbeExpression{Info: expr.Info{Usage: u}, eval: func(*EvalContext) (sqltype.Value, error) {
		t.Fatal("unaffected stored generation demanded")
		return sqltype.Value{}, errors.New("unexpected evaluation")
	}}
	old.Generations = map[int32]columnGeneration{2: {Kind: generationStored, Planned: untouched}, 3: {Kind: generationStored, Planned: untouched}}
	next.Generations = map[int32]columnGeneration{2: {Kind: generationStored, Planned: generated}, 3: {Kind: generationStored, Planned: untouched}}
	row := func(id ids.RowID, version uint64) rowVersion {
		return rowVersion{row: id, version: ids.VersionID(version), born: 1, values: []sqltype.Value{integer(1), integer(2), integer(99)}}
	}
	store := &relationStorage{generation: 1, versions: []rowVersion{row(1, 1), row(2, 2), row(3, 3), row(4, 4)}, ends: &deletionRoot{rows: map[ids.RowID]rowEnd{2: {version: 2, end: 2}}}}
	own := row(3, 5)
	own.base = 3
	own.born = 0
	insert := row(5, 6)
	insert.born = 0
	scope.ownWrites = map[ids.ObjectAddress][]rowVersion{old.address: {own, insert}}
	scope.ownDeletes = map[ids.ObjectAddress]*privateDeletionRoot{old.address: {rows: map[ids.RowID]deleteCandidate{4: {row: 4, version: 4}}}}
	transform := queryProbeExpression{Info: expr.Info{Usage: u}, eval: func(ec *EvalContext) (sqltype.Value, error) {
		if len(ec.Row) != 3 || ec.Row[0] != integer(1) {
			t.Fatal("TYPE lost old row", ec.Row)
		}
		return integer(31), nil
	}}
	additions := []addedColumn{{attribute: 4, column: &parser.Column{Name: "c"}, value: integer(5)}}
	replacement, writes, deletes, err := rewriteTypeRows(scope, scope.op, &old, &next, store, 2, []columnTransformation{{attribute: 1, expression: transform, rewrite: true}}, additions, []int32{2}, newAlterRowValidator(&next, ""))
	if err != nil || calls != 3 || len(replacement.versions) != 1 || len(writes) != 2 || deletes != nil || replacement.generation != 2 {
		t.Fatal(replacement, writes, calls, err)
	}
	if replacement.versions[0].row != 1 || writes[0].row != 3 || writes[0].base != 0 || writes[1].row != 5 {
		t.Fatal("ended/deleted/predecessor resurrected", replacement, writes)
	}
	for _, r := range append(slices.Clone(replacement.versions), writes...) {
		if !slices.Equal(r.values, []sqltype.Value{integer(31), integer(36), integer(99), integer(5)}) {
			t.Fatal(r.values)
		}
	}
	for _, r := range store.versions {
		if len(r.values) != 3 || r.values[0] != integer(1) || r.values[1] != integer(2) || r.values[2] != integer(99) {
			t.Fatal("source changed", r)
		}
	}
	if scope.ownWrites[old.address][0].base != 3 || scope.ownWrites[old.address][0].values[1] != integer(2) {
		t.Fatal("own source changed")
	}
}

func TestAlterGeneratedExpressionCancellation(t *testing.T) {
	e, scope, _, base := alterAddUnitScope(t)
	u := base.Columns[0].Usage
	value, err := sqltype.Integer(u.Type, 2)
	if err != nil {
		t.Fatal(err)
	}
	old := *base
	old.Columns = append(slices.Clone(base.Columns), ColumnDescriptor{Name: "b", Usage: u, Nullable: true})
	old.Attributes = []int32{1, 2}
	old.Index = map[string]int{"a": 0, "b": 1}
	old.Generations = map[int32]columnGeneration{2: {Kind: generationStored}}
	next := old
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	expression := queryProbeExpression{Info: expr.Info{Usage: u}, eval: func(ec *EvalContext) (sqltype.Value, error) {
		calls++
		if !e.mu.TryLock() {
			t.Fatal("generation evaluation held Engine.mu")
		}
		e.mu.Unlock()
		cancel()
		return value, nil
	}}
	next.Generations = map[int32]columnGeneration{2: {Kind: generationStored, Planned: expression}}
	store := &relationStorage{generation: 7, versions: []rowVersion{{row: 1, version: 1, born: 1, values: []sqltype.Value{value, value}}, {row: 2, version: 2, born: 1, values: []sqltype.Value{value, value}}}}
	op, cleanup, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	replacement, writes, deletes, err := rewriteTypeRows(scope, op, &old, &next, store, 1, nil, nil, []int32{2}, newAlterRowValidator(&next, ""))
	if !errors.Is(err, context.Canceled) || calls != 1 || replacement != nil || writes != nil || deletes != nil || store.generation != 7 || len(store.versions[0].values) != 2 || store.versions[0].values[1] != value {
		t.Fatal("real rewrite cancellation leaked candidate", replacement, writes, deletes, calls, err)
	}
}
